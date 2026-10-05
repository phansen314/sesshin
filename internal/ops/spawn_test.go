package ops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
	"github.com/phansen314/sesshin/internal/schematest"
)

// spawnFixture is a temp HOME run in a kitty window, with a fake backend.
type spawnFixture struct {
	*pruneFixture
	// cwd is an existing directory.
	cwd string
	// vars are the caller's environment beyond HOME.
	vars map[string]string

	launches  []kitty.LaunchSpec
	launchID  int64
	launchErr error
	// onLaunch runs as the backend is asked, before it answers.
	onLaunch func()

	tokens int
	// sleeps are the pauses the wait asked for; onSleep runs at each, with
	// the number of pauses so far.
	sleeps  []time.Duration
	onSleep func(n int)
}

func newSpawnFixture(t *testing.T) *spawnFixture {
	t.Helper()
	return &spawnFixture{
		pruneFixture: newPruneFixture(t),
		cwd:          t.TempDir(),
		vars:         map[string]string{"KITTY_LISTEN_ON": "unix:/kitty", "KITTY_WINDOW_ID": "3", "SHELL": "/bin/zsh"},
		launchID:     7,
	}
}

func (f *spawnFixture) spawnEnv() SpawnEnv {
	re := f.pruneFixture.env()
	re.Getenv = func(k string) string {
		if v, ok := f.vars[k]; ok {
			return v
		}
		return f.getenv(k)
	}
	return SpawnEnv{
		ReadEnv: re,
		Launch: func(spec kitty.LaunchSpec) (int64, error) {
			f.launches = append(f.launches, spec)
			if f.onLaunch != nil {
				f.onLaunch()
			}
			return f.launchID, f.launchErr
		},
		Token: func() string { f.tokens++; return fmt.Sprintf("%032x", f.tokens) },
		Sleep: func(d time.Duration) {
			f.sleeps = append(f.sleeps, d)
			f.now = f.now.Add(d)
			if f.onSleep != nil {
				f.onSleep(len(f.sleeps))
			}
		},
	}
}

// token is the nth token the fake issues.
func token(n int) string { return fmt.Sprintf("%032x", n) }

// input is spawn's input: cwd, and the members given, as JSON.
func (f *spawnFixture) input(members ...string) string {
	cwd, _ := json.Marshal(f.cwd)
	return "{" + strings.Join(append([]string{`"cwd":` + string(cwd)}, members...), ",") + "}"
}

// spawnRaw runs spawn on the JSON input as given.
func (f *spawnFixture) spawnRaw(in string) Envelope {
	f.t.Helper()
	si, e := DecodeInput([]byte(in), DecodeSpawnInput)
	if e != nil {
		return Failed(e)
	}
	env := Spawn(si, f.spawnEnv())
	checkEnvelope(f.t, env, "spawn-output")
	return env
}

func (f *spawnFixture) spawn(members ...string) Envelope {
	f.t.Helper()
	return f.spawnRaw(f.input(members...))
}

// spawned is a spawn that must have succeeded.
func (f *spawnFixture) spawned(members ...string) (SpawnOutput, []Warning) {
	f.t.Helper()
	env := f.spawn(members...)
	if !env.OK {
		f.t.Fatalf("spawn failed: %+v", env.Error)
	}
	return env.Result.(SpawnOutput), env.Warnings
}

// reservation reads reservations/<job>.json, and whether it is there.
func (f *spawnFixture) reservation(job string) (model.ReservationFile, bool) {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.loc.ReservationsDir(), job+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return model.ReservationFile{}, false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	r, res := model.ReadReservation(b, job)
	if !res.Usable {
		f.t.Fatalf("reservation unusable: %s", res.Reason())
	}
	return r, true
}

func enc(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonio.MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

const launched = `{"terminal":"kitty","socket":"unix:/kitty","window_id":7}`

func (f *spawnFixture) removeReservation(job string) {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.loc.ReservationsDir(), job+".json")); err != nil {
		f.t.Fatal(err)
	}
}

// Every input check, at its field; and what the schema also says.
func TestSpawnInputChecks(t *testing.T) {
	f := newSpawnFixture(t)
	long := strings.Repeat("a", 64)
	for _, tc := range []struct {
		in     string
		field  string // "": accepted
		schema bool   // the published schema rejects it too
	}{
		{`{"cwd":"/w"}`, "", false},
		{`{"cwd":"/w","job":"api","type":"split","name":"t","prompt":"p","args":["--model","opus"],"vars":{"A":"1","_b2":""},"start_timeout_secs":120}`, "", false},
		{`{"cwd":"/w","type":"os-window","start_timeout_secs":0,"args":[],"vars":{}}`, "", false},
		{`{"cwd":"/w","job":"` + long + `"}`, "", false},
		{`{"cwd":"/w","vars":{"` + strings.Repeat("K", 64) + `":"x"}}`, "", false},
		{`{"cwd":"/w","prompt":"-x $(y) 'z' *","args":["--","$(a)",""]}`, "", false},
		{`{}`, "/cwd", true},
		{`{"cwd":1}`, "/cwd", true},
		{`{"cwd":"w"}`, "/cwd", false},
		{`{"cwd":""}`, "/cwd", false},
		{`{"cwd":"~/w"}`, "/cwd", false},
		{`{"cwd":"/w\u0000"}`, "/cwd", false},
		{`{"cwd":"/w","job":"12"}`, "/job", true},
		{`{"cwd":"/w","job":"Api_"}`, "/job", true},
		{`{"cwd":"/w","job":"-api"}`, "/job", true},
		{`{"cwd":"/w","job":"api-"}`, "/job", true},
		{`{"cwd":"/w","job":""}`, "/job", true},
		{`{"cwd":"/w","job":"` + long + `a"}`, "/job", true},
		{`{"cwd":"/w","job":null}`, "/job", true},
		{`{"cwd":"/w","type":"window"}`, "/type", true},
		{`{"cwd":"/w","type":"Tab"}`, "/type", true},
		{`{"cwd":"/w","type":1}`, "/type", true},
		{`{"cwd":"/w","name":1}`, "/name", true},
		{`{"cwd":"/w","name":"a\u0000b"}`, "/name", false},
		{`{"cwd":"/w","name":""}`, "/name", true},
		{`{"cwd":"/w","prompt":["x"]}`, "/prompt", true},
		{`{"cwd":"/w","prompt":"a\u0000b"}`, "/prompt", false},
		{`{"cwd":"/w","args":"x"}`, "/args", true},
		{`{"cwd":"/w","args":["a",1]}`, "/args/1", true},
		{`{"cwd":"/w","args":["a","b\u0000"]}`, "/args/1", false},
		{`{"cwd":"/w","vars":[]}`, "/vars", true},
		{`{"cwd":"/w","vars":{"A":1}}`, "/vars/A", true},
		{`{"cwd":"/w","vars":{"1a":"x"}}`, "/vars/1a", false},
		{`{"cwd":"/w","vars":{"a-b":"x"}}`, "/vars/a-b", false},
		{`{"cwd":"/w","vars":{"":"x"}}`, "/vars/", false},
		{`{"cwd":"/w","vars":{"` + strings.Repeat("K", 65) + `":"x"}}`, "/vars/" + strings.Repeat("K", 65), false},
		{`{"cwd":"/w","vars":{"A":"x\u0000"}}`, "/vars/A", false},
		{`{"cwd":"/w","start_timeout_secs":-1}`, "/start_timeout_secs", true},
		{`{"cwd":"/w","start_timeout_secs":121}`, "/start_timeout_secs", true},
		{`{"cwd":"/w","start_timeout_secs":1.5}`, "/start_timeout_secs", true},
		{`{"cwd":"/w","start_timeout_secs":"5"}`, "/start_timeout_secs", true},
		{`{"cwd":"/w","extra":1}`, "/extra", true},
	} {
		_, e := DecodeInput([]byte(tc.in), DecodeSpawnInput)
		switch {
		case tc.field == "" && e != nil:
			t.Errorf("%s: %+v", tc.in, e)
		case tc.field != "":
			if e == nil {
				t.Errorf("%s: accepted", tc.in)
				continue
			}
			checkEnvelope(t, Failed(e), "")
			if ps := e.Details["problems"].([]model.Problem); e.Kind != KindInvalidInput || ps[0].Field != tc.field {
				t.Errorf("%s: %+v", tc.in, e)
			}
		}
		if ok, _ := schematest.Check(t, "spawn-input", []byte(tc.in)); ok == (tc.field != "" && tc.schema) {
			t.Errorf("%s: the schema says %v", tc.in, ok)
		}
	}

	// Every problem at once, not the first; and nothing is read or locked.
	env := f.spawnRaw(`{"job":"X_","type":"y","start_timeout_secs":-1,"args":[1]}`)
	wantKind(t, env, KindInvalidInput)
	var fields []string
	for _, p := range env.Error.Details["problems"].([]model.Problem) {
		fields = append(fields, p.Field)
	}
	if want := []string{"/args/0", "/cwd", "/job", "/start_timeout_secs", "/type"}; !slices.Equal(fields, want) {
		t.Errorf("problems at %v, want %v", fields, want)
	}
	if len(f.launches) != 0 {
		t.Error("launched")
	}
}

// Defaults are filled in by the decoder.
func TestSpawnInputDefaults(t *testing.T) {
	in, e := DecodeInput([]byte(`{"cwd":"/w"}`), DecodeSpawnInput)
	if e != nil {
		t.Fatal(e)
	}
	want := SpawnInput{Cwd: "/w", Type: "tab", Args: []string{}, StartTimeoutSecs: 15}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("%+v, want %+v", in, want)
	}
	in, _ = DecodeInput([]byte(`{"cwd":"/w","vars":{"B":"1","A":"2"},"args":["x"],"job":"j","name":"t","prompt":"p","type":"split","start_timeout_secs":0}`), DecodeSpawnInput)
	want = SpawnInput{Job: "j", Cwd: "/w", Type: "split", Name: "t", Prompt: "p", Args: []string{"x"},
		Vars: []kitty.Var{{Name: "B", Value: "1"}, {Name: "A", Value: "2"}}}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("%+v, want %+v", in, want)
	}
}

// The Errors table's order: invalid-input, environment, corrupt, not-found,
// terminal unavailable, busy, conflict, and then the launch's.
func TestSpawnErrorOrder(t *testing.T) {
	// Everything wrong at once, then one thing fewer at a time.
	f := newSpawnFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshinWith(uuidA, 1, "api", "spawn")
	f.config("retain_days = -1")
	f.cwd = filepath.Join(f.cwd, "missing")
	f.vars["KITTY_LISTEN_ON"] = ""
	root, err := fsys.OS{}.OpenRoot(f.loc.SessionsDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	home := f.home

	f.home = ""
	wantKind(t, f.spawnRaw(`{"job":"X_","cwd":"/w"}`), KindInvalidInput)
	wantKind(t, f.spawn(`"job":"api"`), KindEnvironment)
	f.home = home
	wantKind(t, f.spawn(`"job":"api"`), KindCorrupt)
	f.config("")
	env := f.spawn(`"job":"api"`)
	wantKind(t, env, KindNotFound)
	if !reflect.DeepEqual(env.Error.Details, map[string]any{"sessions": []string{}, "paths": []string{f.cwd}}) {
		t.Errorf("details %+v", env.Error.Details)
	}
	f.cwd = t.TempDir()
	env = f.spawn(`"job":"api"`)
	wantKind(t, env, KindTerminal)
	if env.Error.Details["reason"] != "unavailable" || env.Error.Details["terminal"] != nil || env.Error.Details["detail"] == "" {
		t.Errorf("details %+v", env.Error.Details)
	}
	f.vars["KITTY_LISTEN_ON"] = "unix:/kitty"
	wantKind(t, f.spawn(`"job":"api"`), KindBusy) // 500 ms, real
	lock.Unlock()
	env = f.spawn(`"job":"api"`)
	wantKind(t, env, KindConflict)
	if len(f.launches) != 0 {
		t.Error("launched before an error")
	}
	f.launchErr = &kitty.LaunchError{Err: errors.New("no")}
	wantKind(t, f.spawn(`"job":"other"`), KindTerminal)
}

func TestSpawnCwdNotFound(t *testing.T) {
	f := newSpawnFixture(t)
	file := filepath.Join(f.cwd, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.cwd, "link")
	if err := os.Symlink(f.cwd, link); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{filepath.Join(f.cwd, "nope"), file, filepath.Join(file, "below")} {
		cwdJSON, _ := json.Marshal(cwd)
		env := f.spawnRaw(`{"cwd":` + string(cwdJSON) + `}`)
		wantKind(t, env, KindNotFound)
		if !reflect.DeepEqual(env.Error.Details, map[string]any{"sessions": []string{}, "paths": []string{cwd}}) {
			t.Errorf("%s: %+v", cwd, env.Error.Details)
		}
	}
	// A symlink to a directory is one.
	f.cwd = link
	if _, _ = f.spawned(); len(f.launches) != 1 || f.launches[0].Cwd != link {
		t.Errorf("launches %+v", f.launches)
	}
	// Anything else the OS says is io.
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpStat {
			return syscall.EACCES
		}
		return nil
	}
	env := f.spawn()
	wantKind(t, env, KindIO)
	if env.Error.Details["code"] != "EACCES" {
		t.Errorf("%+v", env.Error)
	}
}

func TestSpawnTerminalUnavailable(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"not in kitty":        {"KITTY_LISTEN_ON": "", "KITTY_WINDOW_ID": ""},
		"remote control off":  {"KITTY_LISTEN_ON": "", "KITTY_WINDOW_ID": "3"},
		"no window":           {"KITTY_WINDOW_ID": ""},
		"bad window":          {"KITTY_WINDOW_ID": "0"},
		"tmux":                {"TMUX": "/tmp/tmux-1000/default,1,0"},
		"screen":              {"STY": "123.pts-0.host"},
		"tmux, remote on too": {"TMUX": "x", "KITTY_LISTEN_ON": "unix:/kitty"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSpawnFixture(t)
			for k, v := range vars {
				f.vars[k] = v
			}
			env := f.spawn(`"job":"api"`)
			wantKind(t, env, KindTerminal)
			if env.Error.Details["reason"] != "unavailable" || env.Error.Details["terminal"] != nil {
				t.Errorf("%+v", env.Error.Details)
			}
			if len(f.launches) != 0 {
				t.Error("launched")
			}
			if _, err := os.Stat(f.loc.StateDir); !os.IsNotExist(err) {
				t.Errorf("wrote the state directory: %v", err)
			}
		})
	}
}

// A job's launch: the request the backend gets, the reservation, and the
// result.
func TestSpawnWithJob(t *testing.T) {
	f := newSpawnFixture(t)
	out, warnings := f.spawned(`"job":"api"`, `"prompt":"fix it"`, `"args":["--model","opus"]`, `"vars":{"project":"api"}`, `"start_timeout_secs":0`)
	if len(warnings) != 0 || out.Session != nil || out.Job == nil || *out.Job != "api" || enc(t, out.Placement) != launched {
		t.Errorf("%+v, warnings %+v", out, warnings)
	}
	want := kitty.LaunchSpec{
		Socket: "unix:/kitty",
		Type:   "tab",
		Cwd:    f.cwd,
		Title:  "api",
		Vars:   []kitty.Var{{Name: "project", Value: "api"}},
		Env:    []kitty.Var{{Name: "SESSHIN_JOB", Value: "api"}, {Name: "SESSHIN_TOKEN", Value: token(1)}},
		Argv:   []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--name", "api", "--model", "opus", "--", "fix it"},
	}
	if len(f.launches) != 1 || !reflect.DeepEqual(f.launches[0], want) {
		t.Errorf("launched %+v\nwant     %+v", f.launches, want)
	}
	r, ok := f.reservation("api")
	if !ok || r.Job != "api" || r.Token != token(1) || r.CreatedAt != model.FormatTimestamp(f.now) || enc(t, r.Placement) != launched {
		t.Errorf("reservation %+v", r)
	}
	// The file is in the File format, and fresh while its window stays.
	b, _ := os.ReadFile(filepath.Join(f.loc.ReservationsDir(), "api.json"))
	if !bytes.HasPrefix(b, []byte("{\n  \"schema\": 1,\n  \"job\": \"api\",\n  \"token\": ")) || !bytes.HasSuffix(b, []byte("}\n")) {
		t.Errorf("file %q", b)
	}
	if sleeps := len(f.sleeps); sleeps != 0 {
		t.Errorf("waited with start_timeout_secs 0: %d pauses", sleeps)
	}
}

// With no job: no lock, no reservation, no state written, and the
// environment names no job.
func TestSpawnWithoutJob(t *testing.T) {
	f := newSpawnFixture(t)
	locks := 0
	f.hook = func(op fsys.Op) error {
		if op.Mutating || op.Name == fsys.OpLock {
			locks++
		}
		return nil
	}
	out, warnings := f.spawned(`"start_timeout_secs":0`)
	if len(warnings) != 0 || out.Job != nil || out.Session != nil || enc(t, out.Placement) != launched {
		t.Errorf("%+v, warnings %+v", out, warnings)
	}
	if locks != 0 {
		t.Errorf("%d locks or writes", locks)
	}
	if _, err := os.Stat(f.loc.StateDir); !os.IsNotExist(err) {
		t.Errorf("state directory: %v", err)
	}
	spec := f.launches[0]
	if len(spec.Env) != 0 || spec.Title != "" || spec.Vars != nil && len(spec.Vars) != 0 {
		t.Errorf("spec %+v", spec)
	}
	if want := []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude"}; !slices.Equal(spec.Argv, want) {
		t.Errorf("argv %q", spec.Argv)
	}
	// The job given as JSON null is invalid; absent is none, as here.
	env := f.spawn(`"job":null`)
	wantKind(t, env, KindInvalidInput)
}

// The name, else the job, is the tab title and claude's --name, before the
// args.
func TestSpawnTypeAndName(t *testing.T) {
	for _, tc := range []struct {
		members []string
		typ     string
		name    string
	}{
		{nil, "tab", ""},
		{[]string{`"job":"api"`}, "tab", "api"},
		{[]string{`"job":"api"`, `"name":"my tab"`}, "tab", "my tab"},
		{[]string{`"name":"my tab"`, `"type":"split"`}, "split", "my tab"},
		{[]string{`"type":"os-window"`, `"job":"api"`}, "os-window", "api"},
	} {
		f := newSpawnFixture(t)
		f.spawned(append(tc.members, `"args":["--model","opus"]`, `"start_timeout_secs":0`)...)
		got := f.launches[0]
		i := slices.Index(got.Argv, "claude")
		want := []string{"--model", "opus"}
		if tc.name != "" {
			want = append([]string{"--name", tc.name}, want...)
		}
		if got.Type != tc.typ || got.Title != tc.name || i < 0 || !slices.Equal(got.Argv[i+1:], want) {
			t.Errorf("%v: type %q title %q argv %q", tc.members, got.Type, got.Title, got.Argv)
		}
	}
}

// spawn_shell, else $SHELL, else /bin/sh; fish by its base name.
func TestSpawnShell(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		shell  string
		want   []string
	}{
		{"SHELL", "", "/bin/zsh", []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude"}},
		{"no SHELL", "", "", []string{"/bin/sh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude"}},
		{"relative SHELL", "", "zsh", []string{"/bin/sh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude"}},
		{"configured", `spawn_shell = ["/usr/bin/bash", "-l"]`, "/bin/zsh", []string{"/usr/bin/bash", "-l", "-c", `exec "$@"`, "sesshin", "claude"}},
		{"fish from SHELL", "", "/usr/bin/fish", []string{"/usr/bin/fish", "-l", "-i", "-c", "exec $argv", "claude"}},
		{"fish configured", `spawn_shell = ["/opt/bin/fish", "-i"]`, "/bin/zsh", []string{"/opt/bin/fish", "-i", "-c", "exec $argv", "claude"}},
	} {
		f := newSpawnFixture(t)
		f.vars["SHELL"] = tc.shell
		f.config(tc.config)
		f.spawned(`"start_timeout_secs":0`)
		if got := f.launches[0].Argv; !slices.Equal(got, tc.want) {
			t.Errorf("%s: argv %q, want %q", tc.name, got, tc.want)
		}
	}
	f := newSpawnFixture(t)
	f.config(`spawn_shell = ["sh"]`)
	wantKind(t, f.spawn(), KindCorrupt)
}

func TestShellArgv(t *testing.T) {
	posix := []string{"/bin/zsh", "-l", "-i"}
	for _, tc := range []struct {
		name   string
		shell  []string
		args   []string
		prompt string
		want   []string
	}{
		{"posix, nothing", posix, nil, "", []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude"}},
		{"posix, prompt", posix, nil, "hello", []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--", "hello"}},
		{"posix, args and prompt", posix, []string{"--model", "opus"}, "hello", []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--model", "opus", "--", "hello"}},
		{"posix, args alone", posix, []string{"--model", "opus"}, "", []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--model", "opus"}},
		{"a prompt that begins with -", posix, nil, "-p is not a flag", []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--", "-p is not a flag"}},
		{"a prompt that is --", posix, nil, "--", []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--", "--"}},
		{"args ending in an option that takes a value", posix, []string{"--model"}, "go", []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--model", "--", "go"}},
		{"fish", []string{"/usr/bin/fish", "-l"}, []string{"-x"}, "hi", []string{"/usr/bin/fish", "-l", "-c", "exec $argv", "claude", "-x", "--", "hi"}},
		{"fish alone", []string{"/usr/local/bin/fish"}, nil, "", []string{"/usr/local/bin/fish", "-c", "exec $argv", "claude"}},
		{"not fish", []string{"/bin/fishy"}, nil, "", []string{"/bin/fishy", "-c", `exec "$@"`, "sesshin", "claude"}},
		{"fish is judged by the first word", []string{"/bin/sh", "fish"}, nil, "", []string{"/bin/sh", "fish", "-c", `exec "$@"`, "sesshin", "claude"}},
		{"nothing is interpreted", posix, []string{"$(rm -rf ~)", "`x`", `"q"`, "'s'", "*", "a b", "~", "", "\n"}, "$(id) 'a' \"b\" * ~",
			[]string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "$(rm -rf ~)", "`x`", `"q"`, "'s'", "*", "a b", "~", "", "\n", "--", "$(id) 'a' \"b\" * ~"}},
	} {
		got := ShellArgv(tc.shell, tc.args, tc.prompt)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	// The inputs are not changed.
	shell := make([]string, 3, 10)
	copy(shell, posix)
	ShellArgv(shell, []string{"a"}, "p")
	if !slices.Equal(shell[:3], posix) || shell[:4][3] != "" {
		t.Errorf("shell %q", shell[:4])
	}
}

// The POSIX form, run through /bin/sh: the vector reaches claude exactly as
// given, whatever it holds, and sesshin fills $0.
func TestShellArgvRunsThroughSh(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"--model", "opus", "$(touch " + filepath.Join(dir, "pwned") + ")", "`touch " + filepath.Join(dir, "pwned") + "`",
		`"quoted"`, "'single'", "*", "~", "a b", "", "line\nbreak", "--", "-x"}
	for _, prompt := range []string{"", "-n --prompt $(id) 'x' \"y\" * ~ \n tail"} {
		argv := ShellArgv([]string{sh}, args, prompt)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		want := slices.Clone(args)
		if prompt != "" {
			want = append(want, "--", prompt)
		}
		if !slices.Equal(got, want) {
			t.Errorf("claude got %q\nwant       %q", got, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
			t.Fatal("a shell ran part of the arguments")
		}
	}
}

// The claim: what holds a job, and what doesn't.
func TestSpawnClaim(t *testing.T) {
	type setup func(f *spawnFixture, be *backend)
	for _, tc := range []struct {
		name     string
		setup    setup
		sessions []string // conflict: the sessions named; nil: claimed
		taken    bool
		asked    int // window questions
		wantFile string
	}{
		{"free", func(f *spawnFixture, be *backend) {}, nil, false, 0, ""},
		{"live holder", func(f *spawnFixture, be *backend) {
			f.running(uuidA, time.Minute, 11)
			f.sesshinWith(uuidA, 1, "api", "spawn")
		}, []string{uuidA}, true, 0, ""},
		{"unknown holder", func(f *spawnFixture, be *backend) {
			f.running(uuidA, time.Minute, 11)
			f.sesshinWith(uuidA, 1, "api", "spawn")
			f.tableErr[11] = errors.New("table unreadable")
		}, []string{uuidA}, true, 0, ""},
		{"ended holder", func(f *spawnFixture, be *backend) {
			f.session(uuidA, time.Hour)
			f.sesshinWith(uuidA, 1, "api", "spawn")
		}, nil, false, 0, ""},
		{"another job's holder", func(f *spawnFixture, be *backend) {
			f.running(uuidA, time.Minute, 11)
			f.sesshinWith(uuidA, 1, "web", "spawn")
		}, nil, false, 0, ""},
		{"fresh, not launched", func(f *spawnFixture, be *backend) { f.reserveToken("api", tokenB, 30*time.Second, "") }, nil, true, 0, ""},
		{"fresh, launched, window exists", func(f *spawnFixture, be *backend) {
			f.reserveToken("api", tokenB, time.Hour, kittyAt("unix:/s", 9))
			be.answers["unix:/s"] = []int64{9}
		}, nil, true, 1, ""},
		{"fresh, launched, no answer", func(f *spawnFixture, be *backend) {
			f.reserveToken("api", tokenB, time.Hour, kittyAt("unix:/s", 9))
		}, nil, true, 1, ""},
		{"fresh, launched, no backend", func(f *spawnFixture, be *backend) {
			f.reserveToken("api", tokenB, time.Hour, kittyAt("unix:/s", 9))
		}, nil, true, 0, ""},
		{"stranded", func(f *spawnFixture, be *backend) { f.reserveToken("api", tokenB, 121*time.Second, "") }, nil, false, 0, ""},
		{"expired", func(f *spawnFixture, be *backend) {
			f.reserveToken("api", tokenB, 2*day, kittyAt("unix:/s", 9))
			be.answers["unix:/s"] = []int64{9}
		}, nil, false, 0, ""},
		{"window gone", func(f *spawnFixture, be *backend) {
			f.reserveToken("api", tokenB, time.Hour, kittyAt("unix:/s", 9))
			be.answers["unix:/s"] = []int64{1, 2}
		}, nil, false, 1, ""},
		{"unusable", func(f *spawnFixture, be *backend) { f.writeReservation("api.json", `{"schema":1`) }, nil, false, 0, ""},
		{"unusable, wrong job", func(f *spawnFixture, be *backend) {
			f.writeReservation("api.json", fmt.Sprintf(`{"schema":1,"job":"web","token":%q,"created_at":%q,"placement":null}`, tokenB, model.FormatTimestamp(f.now)))
		}, nil, false, 0, ""},
		{"another job's reservation", func(f *spawnFixture, be *backend) { f.reserveToken("web", tokenB, 30*time.Second, "") }, nil, false, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpawnFixture(t)
			be := newBackend(map[string][]int64{})
			f.windows = be.windows
			tc.setup(f, be)
			if tc.name == "fresh, launched, no backend" {
				f.windows = nil
			}
			env := f.spawn(`"job":"api"`, `"start_timeout_secs":0`)
			if be.asked["unix:/s"] != tc.asked && tc.name != "fresh, launched, no backend" {
				t.Errorf("window questions %v, want %d", be.asked, tc.asked)
			}
			if tc.taken {
				wantKind(t, env, KindConflict)
				if env.Error.Details["rule"] != "job-taken" {
					t.Errorf("%+v", env.Error.Details)
				}
				got := []string{}
				for _, r := range env.Error.Details["sessions"].([]SessionRef) {
					got = append(got, r.SessionID)
				}
				if want := append([]string{}, tc.sessions...); !slices.Equal(got, want) {
					t.Errorf("sessions %v, want %v", got, want)
				}
				if len(f.launches) != 0 {
					t.Error("launched")
				}
				if r, ok := f.reservation("api"); ok && r.Token == token(1) {
					t.Error("claimed")
				}
				return
			}
			if !env.OK {
				t.Fatalf("%+v", env.Error)
			}
			r, ok := f.reservation("api")
			if !ok || r.Token != token(1) || enc(t, r.Placement) != launched {
				t.Errorf("reservation %+v", r)
			}
		})
	}
}

// A directory in a reservation's place is unusable like a malformed file, but
// can't be replaced by one: the OS says so, as io.
func TestSpawnClaimDirectoryInPlace(t *testing.T) {
	f := newSpawnFixture(t)
	if err := os.MkdirAll(filepath.Join(f.loc.ReservationsDir(), "api.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	wantKind(t, f.spawn(`"job":"api"`), KindIO)
	if len(f.launches) != 0 {
		t.Error("launched")
	}
}

// A job held by a live session is reported as the session is named, and a
// duplicate that readers settle (the later one reports none) doesn't hold it.
func TestSpawnClaimReportedJob(t *testing.T) {
	f := newSpawnFixture(t)
	f.running(uuidA, time.Hour, 11, startedAgo(f.pruneFixture, time.Hour))
	f.sesshinWith(uuidA, 1, "api", "spawn")
	f.running(uuidB, time.Minute, 12, startedAgo(f.pruneFixture, time.Minute)) // revived by hand: stores the job too
	f.sesshinWith(uuidB, 2, "api", "hook")
	env := f.spawn(`"job":"api"`)
	wantKind(t, env, KindConflict)
	refs := env.Error.Details["sessions"].([]SessionRef)
	if len(refs) != 1 || refs[0].SessionID != uuidA || refs[0].ID == nil || *refs[0].ID != 1 || refs[0].Name == "" {
		t.Errorf("sessions %+v", refs)
	}
	// Both ended: free.
	f2 := newSpawnFixture(t)
	f2.session(uuidA, time.Hour)
	f2.sesshinWith(uuidA, 1, "api", "spawn")
	f2.session(uuidB, time.Hour)
	f2.sesshinWith(uuidB, 2, "api", "spawn")
	f2.spawned(`"job":"api"`, `"start_timeout_secs":0`)
}

// The window answer counts only for the token and placement it was asked
// about: a reservation another claim replaced under it is fresh.
func TestSpawnWindowAnswerVoided(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replace func(f *spawnFixture)
		taken   bool
	}{
		{"unchanged", func(f *spawnFixture) {}, false},
		{"another claim, same window", func(f *spawnFixture) {
			f.reserveToken("api", tokenB, time.Hour, kittyAt("unix:/s", 9))
		}, true},
		{"another claim, not launched", func(f *spawnFixture) { f.reserveToken("api", tokenB, 10*time.Second, "") }, true},
		{"same token, another window", func(f *spawnFixture) {
			f.reserveToken("api", tokenA, time.Hour, kittyAt("unix:/s", 10))
		}, true},
		{"same token, another socket", func(f *spawnFixture) {
			f.reserveToken("api", tokenA, time.Hour, kittyAt("unix:/other", 9))
		}, true},
		{"removed", func(f *spawnFixture) { f.removeReservation("api") }, false},
		{"unusable", func(f *spawnFixture) { f.writeReservation("api.json", "{") }, false},
		{"another claim, stale", func(f *spawnFixture) { f.reserveToken("api", tokenB, 2*day, "") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpawnFixture(t)
			f.session(uuidA, time.Hour) // sessions/ exists
			be := newBackend(map[string][]int64{"unix:/s": {1, 2}, "unix:/other": {1}})
			f.windows = be.windows
			f.reserveToken("api", tokenA, time.Hour, kittyAt("unix:/s", 9)) // window 9 is gone
			f.hook = func(op fsys.Op) error {
				if op.Name == fsys.OpLock && filepath.Base(op.Root) == "sessions" && len(f.launches) == 0 {
					tc.replace(f)
				}
				return nil
			}
			env := f.spawn(`"job":"api"`, `"start_timeout_secs":0`)
			if be.asked["unix:/s"] != 1 {
				t.Errorf("asked %v", be.asked)
			}
			if tc.taken {
				wantKind(t, env, KindConflict)
				return
			}
			if !env.OK {
				t.Fatalf("%+v", env.Error)
			}
		})
	}
}

// The state lock held for the 500 ms: busy, nothing written or launched.
func TestSpawnBusy(t *testing.T) {
	f := newSpawnFixture(t)
	f.session(uuidA, time.Hour)
	root, err := fsys.OS{}.OpenRoot(f.loc.SessionsDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	start := time.Now()
	env := f.spawn(`"job":"api"`)
	wantKind(t, env, KindBusy)
	if !reflect.DeepEqual(env.Error.Details, map[string]any{"lock": "state"}) {
		t.Errorf("%+v", env.Error.Details)
	}
	if d := time.Since(start); d < 400*time.Millisecond || d > 3*time.Second {
		t.Errorf("waited %v", d)
	}
	if len(f.launches) != 0 {
		t.Error("launched")
	}
	if _, err := os.Stat(f.loc.ReservationsDir()); !os.IsNotExist(err) {
		t.Errorf("reservations/: %v", err)
	}
	// Without a job there is no lock to wait for.
	f.spawned(`"start_timeout_secs":0`)
}

// A claim creates what is missing: the state directory, sessions/ for the
// lock, reservations/.
func TestSpawnClaimCreatesDirectories(t *testing.T) {
	f := newSpawnFixture(t)
	f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
	for _, d := range []string{f.loc.SessionsDir(), f.loc.ReservationsDir()} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, st, err)
		}
	}
	// And a claim with only reservations/ missing.
	f = newSpawnFixture(t)
	f.session(uuidA, time.Hour)
	f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
	if _, ok := f.reservation("api"); !ok {
		t.Error("no reservation")
	}
}

// The launch's outcomes (operations.md, spawn, step 4).
func TestSpawnLaunchFailed(t *testing.T) {
	f := newSpawnFixture(t)
	f.launchErr = &kitty.LaunchError{Err: errors.New("exit status 1: no such socket")}
	env := f.spawn(`"job":"api"`)
	wantKind(t, env, KindTerminal)
	want := map[string]any{"reason": "launch-failed", "terminal": "kitty", "detail": "exit status 1: no such socket"}
	if !reflect.DeepEqual(env.Error.Details, want) {
		t.Errorf("details %+v", env.Error.Details)
	}
	if _, ok := f.reservation("api"); ok {
		t.Error("the reservation was kept: the job is not free")
	}
	// Free at once: the next spawn of the job claims it.
	f.launchErr = nil
	f.tokens = 5
	f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
	if r, _ := f.reservation("api"); r.Token != token(6) {
		t.Errorf("token %s", r.Token)
	}
	// An error that isn't a *LaunchError is a refusal too.
	f2 := newSpawnFixture(t)
	f2.launchErr = errors.New("odd")
	env = f2.spawn(`"job":"api"`)
	wantKind(t, env, KindTerminal)
	if env.Error.Details["reason"] != "launch-failed" {
		t.Errorf("%+v", env.Error.Details)
	}
	// No job: nothing to remove, and no lock taken.
	f3 := newSpawnFixture(t)
	f3.launchErr = &kitty.LaunchError{Err: errors.New("x")}
	wantKind(t, f3.spawn(), KindTerminal)
}

// A refused launch removes only its own reservation.
func TestSpawnLaunchFailedKeepsOthers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replace  func(f *spawnFixture)
		wantFile bool
	}{
		{"another claim", func(f *spawnFixture) { f.reserveToken("api", tokenB, 0, "") }, true},
		{"adopted", func(f *spawnFixture) { f.removeReservation("api") }, false},
		{"unusable", func(f *spawnFixture) { f.writeReservation("api.json", "{") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpawnFixture(t)
			f.launchErr = &kitty.LaunchError{Err: errors.New("x")}
			f.onLaunch = func() { tc.replace(f) }
			wantKind(t, f.spawn(`"job":"api"`), KindTerminal)
			_, err := os.Stat(filepath.Join(f.loc.ReservationsDir(), "api.json"))
			if (err == nil) != tc.wantFile {
				t.Errorf("file: %v", err)
			}
			if tc.name == "another claim" {
				if r, _ := f.reservation("api"); r.Token != tokenB {
					t.Errorf("token %s", r.Token)
				}
			}
		})
	}
}

// If the lock or the removal fails, the error is still launch-failed; the
// reservation strands in 120 seconds.
func TestSpawnLaunchFailedLockFails(t *testing.T) {
	for name, fail := range map[string]func(op fsys.Op, locks int) error{
		"lock held": func(op fsys.Op, locks int) error {
			if op.Name == fsys.OpLock && locks == 2 {
				return syscall.EAGAIN
			}
			return nil
		},
		"lock fails": func(op fsys.Op, locks int) error {
			if op.Name == fsys.OpLock && locks == 2 {
				return syscall.EIO
			}
			return nil
		},
		"remove fails": func(op fsys.Op, _ int) error {
			if op.Name == fsys.OpRemove {
				return syscall.EIO
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSpawnFixture(t)
			f.launchErr = &kitty.LaunchError{Err: errors.New("x")}
			locks := 0
			f.hook = func(op fsys.Op) error {
				if op.Name == fsys.OpLock {
					locks++
				}
				return fail(op, locks)
			}
			env := f.spawn(`"job":"api"`)
			wantKind(t, env, KindTerminal)
			if env.Error.Details["reason"] != "launch-failed" {
				t.Errorf("%+v", env.Error.Details)
			}
			if r, ok := f.reservation("api"); !ok || r.Placement != nil {
				t.Errorf("reservation %+v", r)
			}
		})
	}
}

// A launch that may have opened a window keeps its reservation, unplaced.
func TestSpawnLaunchUnknown(t *testing.T) {
	f := newSpawnFixture(t)
	f.launchErr = &kitty.LaunchError{Unknown: true, Err: errors.New("10s limit passed")}
	env := f.spawn(`"job":"api"`)
	wantKind(t, env, KindTerminal)
	want := map[string]any{"reason": "launch-unknown", "terminal": "kitty", "detail": "10s limit passed"}
	if !reflect.DeepEqual(env.Error.Details, want) {
		t.Errorf("details %+v", env.Error.Details)
	}
	r, ok := f.reservation("api")
	if !ok || r.Token != token(1) || r.Placement != nil {
		t.Errorf("reservation %+v", r)
	}
	// A retry while it is fresh is refused; once stranded it is replaced.
	wantKind(t, f.spawn(`"job":"api"`), KindConflict)
	f.now = f.now.Add(121 * time.Second)
	f.launchErr = nil
	f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
	// No job: an unknown outcome is just the error.
	f2 := newSpawnFixture(t)
	f2.launchErr = &kitty.LaunchError{Unknown: true, Err: errors.New("x")}
	env = f2.spawn()
	if env.Error.Details["reason"] != "launch-unknown" {
		t.Errorf("%+v", env.Error.Details)
	}
}

// The window is recorded only while the file still holds this token
// (step 5).
func TestSpawnRecord(t *testing.T) {
	for _, tc := range []struct {
		name    string
		onLaunc func(f *spawnFixture)
		// want is the file afterwards: "placement", "none" (null), "gone",
		// or "other" (another claim's, untouched).
		want string
	}{
		{"recorded", nil, "placement"},
		{"adopted: gone", func(f *spawnFixture) { f.removeReservation("api") }, "gone"},
		{"claimed again", func(f *spawnFixture) { f.reserveToken("api", tokenB, 0, "") }, "other"},
		{"replaced by an unusable one", func(f *spawnFixture) { f.writeReservation("api.json", "{") }, "unusable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpawnFixture(t)
			if tc.onLaunc != nil {
				f.onLaunch = func() { tc.onLaunc(f) }
			}
			out, warnings := f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
			if len(warnings) != 0 || enc(t, out.Placement) != launched {
				t.Errorf("%+v, warnings %+v", out, warnings)
			}
			path := filepath.Join(f.loc.ReservationsDir(), "api.json")
			b, err := os.ReadFile(path)
			switch tc.want {
			case "gone":
				if err == nil {
					t.Errorf("file came back: %s", b)
				}
			case "unusable":
				if string(b) != "{" {
					t.Errorf("file %q", b)
				}
			default:
				r, _ := f.reservation("api")
				switch tc.want {
				case "placement":
					if r.Token != token(1) || enc(t, r.Placement) != launched || r.CreatedAt != model.FormatTimestamp(f.now) {
						t.Errorf("reservation %+v", r)
					}
				case "other":
					if r.Token != tokenB || r.Placement != nil {
						t.Errorf("reservation %+v", r)
					}
				}
			}
		})
	}
}

// The rewrite keeps everything but the placement, and created_at is the
// claim's.
func TestSpawnRecordKeepsCreatedAt(t *testing.T) {
	f := newSpawnFixture(t)
	claimed := f.now
	f.onLaunch = func() { f.now = f.now.Add(90 * time.Second) } // a slow launch
	f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
	r, _ := f.reservation("api")
	if r.CreatedAt != model.FormatTimestamp(claimed) || r.Token != token(1) || enc(t, r.Placement) != launched {
		t.Errorf("reservation %+v", r)
	}
}

func TestSpawnPlacementNotRecorded(t *testing.T) {
	for name, fail := range map[string]func(op fsys.Op, locks, renames int) error{
		"lock held":     func(op fsys.Op, locks, _ int) error { return failAt(op, fsys.OpLock, locks, 2, syscall.EAGAIN) },
		"lock fails":    func(op fsys.Op, locks, _ int) error { return failAt(op, fsys.OpLock, locks, 2, syscall.EIO) },
		"rewrite fails": func(op fsys.Op, _, renames int) error { return failAt(op, fsys.OpRename, renames, 2, syscall.ENOSPC) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSpawnFixture(t)
			locks, renames := 0, 0
			f.hook = func(op fsys.Op) error {
				switch op.Name {
				case fsys.OpLock:
					locks++
				case fsys.OpRename:
					renames++
				}
				return fail(op, locks, renames)
			}
			env := f.spawn(`"job":"api"`, `"start_timeout_secs":0`)
			if !env.OK {
				t.Fatalf("%+v", env.Error)
			}
			out := env.Result.(SpawnOutput)
			if enc(t, out.Placement) != launched {
				t.Errorf("placement %s", enc(t, out.Placement))
			}
			if len(env.Warnings) != 1 || env.Warnings[0].Kind != "placement-not-recorded" {
				t.Fatalf("warnings %+v", env.Warnings)
			}
			d := env.Warnings[0].Details
			if d["job"] != "api" || enc(t, d["placement"]) != launched {
				t.Errorf("details %+v", d)
			}
			// The reservation keeps its claim, and goes stale 120 seconds
			// after created_at.
			r, ok := f.reservation("api")
			if !ok || r.Placement != nil || r.Token != token(1) {
				t.Errorf("reservation %+v", r)
			}
			if _, ok := f.tempFiles(); ok {
				t.Error("a temp file was left behind")
			}
		})
	}
}

func failAt(op fsys.Op, name string, count, at int, err error) error {
	if op.Name == name && count == at {
		return err
	}
	return nil
}

// tempFiles reports hidden temp files left in reservations/.
func (f *spawnFixture) tempFiles() ([]string, bool) {
	ents, _ := os.ReadDir(f.loc.ReservationsDir())
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out, len(out) > 0
}

// With a job, the wait ends when a live session reports it.
func TestSpawnWaitJob(t *testing.T) {
	f := newSpawnFixture(t)
	f.onSleep = func(n int) {
		if n == 3 {
			f.running(uuidB, 0, 21)
			f.sesshinWith(uuidB, 2, "api", "spawn")
		}
	}
	out, warnings := f.spawned(`"job":"api"`, `"start_timeout_secs":5`)
	if len(warnings) != 0 || out.Session == nil {
		t.Fatalf("%+v, warnings %+v", out, warnings)
	}
	if s := out.Session; s.SessionID != uuidB || s.ID == nil || *s.ID != 2 || s.Job == nil || *s.Job != "api" || s.Liveness != "live" {
		t.Errorf("session %+v", s)
	}
	if want := []time.Duration{pollInterval, pollInterval, pollInterval}; !slices.Equal(f.sleeps, want) {
		t.Errorf("pauses %v", f.sleeps)
	}
	// The result is show's view of that session.
	shown := f.shown("2")
	if viaSpawn := asMap(t, out.Session); !reflect.DeepEqual(viaSpawn, shown) {
		t.Errorf("view differs from show's:\n%v\n%v", viaSpawn, shown)
	}
}

// Sessions that don't count: an ended holder, another job's, a live one
// that reports none.
func TestSpawnWaitJobIgnores(t *testing.T) {
	f := newSpawnFixture(t)
	f.session(uuidA, time.Hour)
	f.sesshinWith(uuidA, 1, "api", "spawn") // ended: it holds nothing
	f.running(uuidC, 0, 22)
	f.sesshinWith(uuidC, 3, "web", "spawn")
	out, warnings := f.spawned(`"job":"api"`, `"start_timeout_secs":1`)
	if out.Session != nil || len(warnings) != 1 || warnings[0].Kind != "not-started" {
		t.Errorf("%+v, warnings %+v", out, warnings)
	}
}

// Without a job, the session is the one whose placement names the launched
// window.
func TestSpawnWaitWindow(t *testing.T) {
	f := newSpawnFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 1, `{"terminal":"kitty","socket":"unix:/kitty","window_id":3}`) // the caller's own
	f.running(uuidD, time.Minute, 14)
	f.sesshin(uuidD, 4, `{"terminal":"kitty","socket":"unix:/elsewhere","window_id":7}`) // another kitty's 7
	f.running(uuidE, time.Minute, 15)                                                    // no placement
	f.onSleep = func(n int) {
		if n == 2 {
			f.running(uuidB, 0, 21)
			f.sesshin(uuidB, 2, launched)
		}
	}
	out, warnings := f.spawned(`"start_timeout_secs":5`)
	if len(warnings) != 0 || out.Session == nil || out.Session.SessionID != uuidB || out.Job != nil {
		t.Errorf("%+v, warnings %+v", out, warnings)
	}
	if len(f.sleeps) != 2 {
		t.Errorf("pauses %v", f.sleeps)
	}
	if out.Session.Placement == nil || enc(t, out.Session.Placement) != launched {
		t.Errorf("placement %v", out.Session.Placement)
	}
}

func TestSpawnNotStarted(t *testing.T) {
	f := newSpawnFixture(t)
	out, warnings := f.spawned(`"job":"api"`, `"start_timeout_secs":2`)
	if out.Session != nil || enc(t, out.Placement) != launched || *out.Job != "api" {
		t.Errorf("%+v", out)
	}
	if len(warnings) != 1 || warnings[0].Kind != "not-started" {
		t.Fatalf("warnings %+v", warnings)
	}
	d := warnings[0].Details
	if d["job"] == nil || *d["job"].(*string) != "api" || enc(t, d["placement"]) != launched || d["waited_secs"] != int64(2) {
		t.Errorf("details %+v", d)
	}
	// Polled every 100 ms, for the 2 seconds.
	if len(f.sleeps) != 20 {
		t.Errorf("%d pauses", len(f.sleeps))
	}
	for _, s := range f.sleeps {
		if s != 100*time.Millisecond {
			t.Errorf("pause %v", s)
		}
	}
	// The reservation is still there, placed, for the session that starts.
	if r, ok := f.reservation("api"); !ok || enc(t, r.Placement) != launched {
		t.Errorf("reservation %+v", r)
	}
	// Without a job, the warning's job is null.
	f2 := newSpawnFixture(t)
	_, warnings = f2.spawned(`"start_timeout_secs":1`)
	if len(warnings) != 1 || warnings[0].Details["job"] != (*string)(nil) {
		t.Errorf("warnings %+v", warnings)
	}
	raw, _ := json.Marshal(warnings[0].Details)
	if !strings.Contains(string(raw), `"job":null`) || !strings.Contains(string(raw), `"waited_secs":1`) {
		t.Errorf("%s", raw)
	}
}

// start_timeout_secs 0 returns at once: no read, no pause, no warning.
func TestSpawnNoWait(t *testing.T) {
	f := newSpawnFixture(t)
	f.running(uuidB, 0, 21)
	f.sesshinWith(uuidB, 2, "api", "spawn") // would be found, if read
	reads := 0
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpReadFile && strings.HasSuffix(op.Path, "lifecycle.json") {
			reads++
		}
		return nil
	}
	// The job is held: use another.
	out, warnings := f.spawned(`"job":"web"`, `"start_timeout_secs":0`)
	if out.Session != nil || len(warnings) != 0 || len(f.sleeps) != 0 {
		t.Errorf("%+v, %+v, %v", out, warnings, f.sleeps)
	}
	if reads != 1 { // the claim's one read of the session
		t.Errorf("%d reads of lifecycle.json", reads)
	}
}

// The wait takes no lock, and the timeout is measured on the clock.
func TestSpawnWaitTakesNoLock(t *testing.T) {
	f := newSpawnFixture(t)
	locks := 0
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpLock {
			locks++
		}
		return nil
	}
	f.spawned(`"job":"api"`, `"start_timeout_secs":1`)
	if locks != 2 { // the claim, and the record
		t.Errorf("%d locks", locks)
	}
	if want := 10; len(f.sleeps) != want {
		t.Errorf("%d pauses, want %d", len(f.sleeps), want)
	}
}

// Unusable files: those the claim read, and those of the last read of the
// wait, each once.
func TestSpawnUnusableFiles(t *testing.T) {
	f := newSpawnFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.write(uuidA, "sesshin.json", []byte("{")) // unusable, at the claim and again after
	f.onSleep = func(n int) {
		if n == 1 {
			f.write(uuidC, "lifecycle.json", []byte("{")) // appears while waiting
		}
		if n == 2 {
			f.running(uuidB, 0, 21)
			f.sesshinWith(uuidB, 2, "api", "spawn")
		}
	}
	out, warnings := f.spawned(`"job":"api"`, `"start_timeout_secs":5`)
	if out.Session == nil {
		t.Fatalf("not started: %+v", warnings)
	}
	var paths []string
	for _, w := range warnings {
		if w.Kind != "unusable-file" {
			t.Errorf("warning %+v", w)
		}
		paths = append(paths, filepath.Base(filepath.Dir(w.Details["path"].(string)))+"/"+filepath.Base(w.Details["path"].(string)))
	}
	if want := []string{uuidA + "/sesshin.json", uuidC + "/lifecycle.json"}; !slices.Equal(paths, want) {
		t.Errorf("warnings for %v, want %v", paths, want)
	}

	// With no job there is no claim to read the sessions: the wait's read only.
	f = newSpawnFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.write(uuidA, "sesshin.json", []byte("{"))
	_, warnings = f.spawned(`"start_timeout_secs":1`)
	if got := warnKinds(Envelope{Warnings: warnings}); !slices.Equal(got, []string{"unusable-file", "not-started"}) {
		t.Errorf("warnings %v", got)
	}
	// A claim's warning goes with the conflict too.
	f = newSpawnFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshinWith(uuidA, 1, "api", "spawn")
	f.write(uuidB, "sesshin.json", []byte("{"))
	f.running(uuidB, time.Minute, 12)
	f.write(uuidB, "sesshin.json", []byte("{"))
	f.launchErr = &kitty.LaunchError{Err: errors.New("x")}
	env := f.spawn(`"job":"other"`)
	wantKind(t, env, KindTerminal)
	if got := warnKinds(env); !slices.Equal(got, []string{"unusable-file"}) {
		t.Errorf("warnings %v", got)
	}
}

// A fault reading the sessions during the wait is io.
func TestSpawnWaitIOFault(t *testing.T) {
	f := newSpawnFixture(t)
	f.hook = func(op fsys.Op) error {
		if len(f.launches) > 0 && op.Name == fsys.OpReadDir && filepath.Base(op.Root) == "sessions" {
			return syscall.EIO
		}
		return nil
	}
	f.session(uuidA, time.Hour)
	env := f.spawn(`"job":"api"`, `"start_timeout_secs":1`)
	wantKind(t, env, KindIO)
}

func TestShowNotFoundPaths(t *testing.T) {
	f := newPruneFixture(t)
	env := f.show("12", false)
	wantKind(t, env, KindNotFound)
	if !reflect.DeepEqual(env.Error.Details, map[string]any{"sessions": []string{"12"}, "paths": []string{}}) {
		t.Errorf("%+v", env.Error.Details)
	}
}

// The tokens are 32 lowercase hex characters, and differ.
func TestRandomToken(t *testing.T) {
	a, b := randomToken(), randomToken()
	if !model.IsToken(a) || !model.IsToken(b) || a == b {
		t.Errorf("%q %q", a, b)
	}
	if env := OSSpawnEnv(); env.Launch == nil || env.Token == nil || env.Sleep == nil || env.Now == nil || env.Windows == nil {
		t.Errorf("%+v", env)
	}
}

// Jobs differing only in case are one job: a spawn of api is refused while
// API is held, by a live session or a fresh reservation, and the message
// names the stored job (design-spec.md, Reservations).
func TestSpawnClaimOtherCase(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *spawnFixture)
	}{
		{"live session", func(f *spawnFixture) {
			f.running(uuidA, time.Minute, 11)
			f.sesshinWith(uuidA, 1, "API", "spawn")
		}},
		{"reservation", func(f *spawnFixture) {
			f.writeReservation("api.json", fmt.Sprintf(`{"schema":1,"job":"API","token":%q,"created_at":%q,"placement":null}`, tokenB, model.FormatTimestamp(f.ago(30*time.Second))))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpawnFixture(t)
			tc.setup(f)
			env := f.spawn(`"job":"api"`, `"start_timeout_secs":0`)
			wantKind(t, env, KindConflict)
			if env.Error.Details["rule"] != "job-taken" || !strings.HasPrefix(env.Error.Message, "the job api is held, as API") {
				t.Errorf("%+v", env.Error)
			}
			if len(f.launches) != 0 {
				t.Error("launched")
			}
		})
	}
}

// A job keeps its case in the reservation, whose file is named by its key.
func TestSpawnReservationKey(t *testing.T) {
	f := newSpawnFixture(t)
	f.spawned(`"job":"API"`, `"start_timeout_secs":0`)
	if r, ok := f.reservation("api"); !ok || r.Job != "API" || r.Token != token(1) {
		t.Errorf("reservation %+v", r)
	}
	b, err := os.ReadFile(filepath.Join(f.loc.ReservationsDir(), "api.json"))
	if err != nil || !bytes.Contains(b, []byte(`"job": "API"`)) || !bytes.Contains(b, []byte(`"window_id"`)) {
		t.Errorf("file %q, %v", b, err)
	}
}
