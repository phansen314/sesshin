package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/placementtest"
)

// spawnCommand is the real spawn command with an operation that echoes the
// input it was given, so the tests see what the command line made of it.
func spawnCommand(t *testing.T) []Command {
	t.Helper()
	for _, c := range commands {
		if c.Name == "spawn" {
			c.Run = operation(ops.DecodeSpawnInput, func(in ops.SpawnInput, _ Env) ops.Envelope {
				vars := map[string]string{}
				var order []string
				for _, v := range in.Vars {
					vars[v.Name] = v.Value
					order = append(order, v.Name)
				}
				return ops.Succeeded(map[string]any{
					"job": in.Job, "cwd": in.Cwd, "type": in.Type, "name": in.Name, "prompt": in.Prompt,
					"args": in.Args, "vars": vars, "order": order, "extra": in.Extra, "start_timeout_secs": in.StartTimeoutSecs,
				})
			})
			return []Command{c}
		}
	}
	t.Fatal("no spawn command")
	return nil
}

type spawnRun struct {
	stdin string
	env   map[string]string
	wd    string
	wdErr error
}

func (s spawnRun) run(t *testing.T, args ...string) argResult {
	t.Helper()
	var out, errOut bytes.Buffer
	wd := s.wd
	if wd == "" {
		wd = "/work/here"
	}
	se := ops.OSSpawnEnv()
	se.Getenv = func(k string) string { return s.env[k] }
	env := Env{
		Spawn:     &se,
		Stdin:     strings.NewReader(s.stdin),
		Stdout:    &out,
		Stderr:    &errOut,
		BuildInfo: func() buildinfo.Info { return buildinfo.Info{Version: buildinfo.Devel, Go: "go1.26.8"} },
		Getwd:     func() (string, error) { return wd, s.wdErr },
	}
	o, code, note := execute(spawnCommand(t), append([]string{"spawn"}, args...), env)
	deliver(env, o, code, note)
	parse(t, out.String())
	var r argResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func problemFields(r argResult) []string {
	var out []string
	for _, p := range r.Error.Details.Problems {
		out = append(out, p.Field)
	}
	return out
}

func TestSpawnVar(t *testing.T) {
	r := spawnRun{}.run(t, "--cwd", "/w", "--var", "B=x=y,z", "--var", "A=1", "--var", "E=")
	if !r.OK {
		t.Fatalf("%+v", r)
	}
	if want := map[string]any{"B": "x=y,z", "A": "1", "E": ""}; !reflect.DeepEqual(r.Result["vars"], want) {
		t.Errorf("vars %v", r.Result["vars"])
	}
	// In the order given: that is the order kitty gets them in.
	if want := []any{"B", "A", "E"}; !reflect.DeepEqual(r.Result["order"], want) {
		t.Errorf("order %v", r.Result["order"])
	}

	for _, tc := range []struct {
		name  string
		args  []string
		field string
	}{
		{"no =", []string{"--var", "A"}, "/vars"},
		{"twice", []string{"--var", "A=1", "--var", "A=2"}, "/vars"},
		{"empty token", []string{"--var", ""}, "/vars"},
		{"bad key", []string{"--var", "1x=2"}, "/vars/1x"},
		{"empty key", []string{"--var", "=2"}, "/vars/"},
		{"key with a slash", []string{"--var", "a/b=2"}, "/vars/a~1b"},
		{"NUL", []string{"--var", "A=\x00"}, "/vars/A"},
	} {
		r := spawnRun{}.run(t, append([]string{"--cwd", "/w"}, tc.args...)...)
		if r.OK || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{tc.field}) {
			t.Errorf("%s: %+v", tc.name, r)
		}
	}
	// A malformed --var and another bad value are reported together.
	r = spawnRun{}.run(t, "--cwd", "/w", "--var", "A", "--type", "nope")
	if r.OK || !reflect.DeepEqual(problemFields(r), []string{"/type", "/vars"}) {
		t.Errorf("%+v", r)
	}
}

func TestSpawnTrailingArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []any
	}{
		{"none", []string{"--cwd", "/w"}, []any{}},
		{"bare --", []string{"--cwd", "/w", "--"}, []any{}},
		{"flags and values", []string{"--cwd", "/w", "--", "--model", "opus", "-x"}, []any{"--model", "opus", "-x"}},
		{"options after the command, before --", []string{"--job", "api", "--cwd", "/w", "--", "a"}, []any{"a"}},
		{"nothing is an option after --", []string{"--cwd", "/w", "--", "--job", "x", "--cwd=/y"}, []any{"--job", "x", "--cwd=/y"}},
		{"a command name", []string{"--cwd", "/w", "--", "list", "-i"}, []any{"list", "-i"}},
		{"empty and spaces", []string{"--cwd", "/w", "--", "", "a b", "$(x)"}, []any{"", "a b", "$(x)"}},
		{"a second --", []string{"--cwd", "/w", "--", "a", "--", "b"}, []any{"a", "--", "b"}},
	} {
		r := spawnRun{}.run(t, tc.args...)
		if !r.OK || !reflect.DeepEqual(r.Result["args"], tc.want) {
			t.Errorf("%s: args %#v (%+v)", tc.name, r.Result["args"], r)
		}
	}
	if r := (spawnRun{}).run(t, "--job", "api", "--cwd", "/w", "--", "x"); r.Result["job"] != "api" {
		t.Errorf("job %v", r.Result["job"])
	}
	// A bare word before -- is not claude's: it is an extra argument.
	r := spawnRun{}.run(t, "--cwd", "/w", "extra", "--", "x")
	if r.OK || r.Error.Kind != "usage" || r.Error.Details.Problems[0].Argument == nil || *r.Error.Details.Problems[0].Argument != "extra" {
		t.Errorf("%+v", r)
	}
	// --input and claude args are both input.
	r = spawnRun{stdin: `{"cwd":"/w"}`}.run(t, "-i", "-", "--", "x")
	if r.OK || r.Error.Kind != "usage" {
		t.Errorf("%+v", r)
	}
}

func TestSpawnPrompt(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	r := spawnRun{}.run(t, "--cwd", "/w", "--prompt", "fix $(it) 'now'")
	if !r.OK || r.Result["prompt"] != "fix $(it) 'now'" {
		t.Errorf("--prompt: %+v", r)
	}

	// The file's content exactly: no trimming, a trailing newline kept.
	body := "line one\n\n  line two  \n"
	r = spawnRun{}.run(t, "--cwd", "/w", "--prompt-file", write("p.txt", body))
	if !r.OK || r.Result["prompt"] != body {
		t.Errorf("--prompt-file: %q (%+v)", r.Result["prompt"], r)
	}
	r = spawnRun{stdin: body}.run(t, "--cwd", "/w", "--prompt-file", "-")
	if !r.OK || r.Result["prompt"] != body {
		t.Errorf("--prompt-file -: %q (%+v)", r.Result["prompt"], r)
	}
	r = spawnRun{}.run(t, "--cwd", "/w", "--prompt-file", write("empty", ""))
	if !r.OK || r.Result["prompt"] != "" {
		t.Errorf("empty file: %+v", r)
	}

	// Both is a usage error, in either order.
	for _, args := range [][]string{
		{"--prompt", "x", "--prompt-file", write("a", "y")},
		{"--prompt-file", write("b", "y"), "--prompt", "x"},
	} {
		r = spawnRun{}.run(t, append([]string{"--cwd", "/w"}, args...)...)
		if r.OK || r.Error.Kind != "usage" || !strings.Contains(r.Error.Message, "same field") {
			t.Errorf("%v: %+v", args, r)
		}
	}

	// Unreadable: io, with the path as given. A directory too.
	for _, p := range []string{filepath.Join(dir, "missing"), dir} {
		r = spawnRun{}.run(t, "--cwd", "/w", "--prompt-file", p)
		if r.OK || r.Error.Kind != "io" {
			t.Errorf("%s: %+v", p, r)
		}
	}
	var out bytes.Buffer
	missing := filepath.Join(dir, "missing")
	noEnv := ops.OSSpawnEnv()
	noEnv.Getenv = func(string) string { return "" }
	env := Env{Stdout: &out, Stderr: &bytes.Buffer{}, Getwd: func() (string, error) { return "/w", nil }, Spawn: &noEnv}
	o, _, _ := execute(spawnCommand(t), []string{"spawn", "--cwd", "/w", "--prompt-file", missing}, env)
	var e struct {
		Error struct{ Details struct{ Path, Code string } }
	}
	if err := json.Unmarshal(o, &e); err != nil || e.Error.Details.Path != missing || e.Error.Details.Code != "ENOENT" {
		t.Errorf("%s", o)
	}

	// Not UTF-8: invalid-input at /prompt, as the same bytes on --input are.
	r = spawnRun{}.run(t, "--cwd", "/w", "--prompt-file", write("bad", "a\xffb"))
	if r.OK || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/prompt"}) {
		t.Errorf("%+v", r)
	}
	// And so is a name that isn't.
	r = spawnRun{}.run(t, "--cwd", "/w", "--name", "a\xffb")
	if r.OK || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/name"}) {
		t.Errorf("%+v", r)
	}
	// --prompt-file with --input is combined input.
	r = spawnRun{stdin: `{"cwd":"/w"}`}.run(t, "-i", "-", "--prompt-file", write("c", "y"))
	if r.OK || r.Error.Kind != "usage" {
		t.Errorf("%+v", r)
	}
}

func TestSpawnCwd(t *testing.T) {
	home := map[string]string{"HOME": "/home/me"}
	for _, tc := range []struct {
		name string
		run  spawnRun
		args []string
		want string
	}{
		{"default is the working directory", spawnRun{wd: "/work/here"}, nil, "/work/here"},
		{"absolute", spawnRun{}, []string{"--cwd", "/srv/api"}, "/srv/api"},
		{"absolute, left alone", spawnRun{}, []string{"--cwd", "/srv/../api/"}, "/srv/../api/"},
		{"~/", spawnRun{env: home}, []string{"--cwd", "~/code/api"}, "/home/me/code/api"},
		{"~ alone", spawnRun{env: home}, []string{"--cwd", "~"}, "/home/me"},
		{"~/ with a trailing slash in HOME", spawnRun{env: map[string]string{"HOME": "/home/me/"}}, []string{"--cwd", "~/x"}, "/home/me/x"},
		{"relative", spawnRun{wd: "/work/here"}, []string{"--cwd", "api"}, "/work/here/api"},
		{"dot", spawnRun{wd: "/work/here"}, []string{"--cwd", "."}, "/work/here/."},
		{"dot dot is left in place", spawnRun{wd: "/work/here"}, []string{"--cwd", "../there"}, "/work/here/../there"},
		{"relative under /", spawnRun{wd: "/"}, []string{"--cwd", "etc"}, "/etc"},
		{"empty is the operation's", spawnRun{wd: "/work/here"}, []string{"--cwd", ""}, ""},
		{"last wins", spawnRun{}, []string{"--cwd", "/a", "--cwd", "/b"}, "/b"},
		{"a name that only looks like ~", spawnRun{}, []string{"--cwd", "./~me"}, "/work/here/./~me"},
	} {
		r := tc.run.run(t, tc.args...)
		if tc.want == "" {
			if r.OK || !reflect.DeepEqual(problemFields(r), []string{"/cwd"}) {
				t.Errorf("%s: %+v", tc.name, r)
			}
			continue
		}
		if !r.OK || r.Result["cwd"] != tc.want {
			t.Errorf("%s: cwd %v (%+v)", tc.name, r.Result["cwd"], r)
		}
	}

	// ~user is never expanded: invalid-input at /cwd, which the operation's
	// own check of the value left as given (not absolute) joins.
	for _, cwd := range []string{"~root/x", "~root", "~x/"} {
		r := spawnRun{env: home}.run(t, "--cwd", cwd)
		if r.OK || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/cwd", "/cwd"}) ||
			!strings.Contains(r.Error.Details.Problems[1].Reason, "~user") {
			t.Errorf("%s: %+v", cwd, r)
		}
	}

	// Without a usable HOME, ~/ is environment; otherwise HOME is not read.
	for _, h := range []string{"", "home/me"} {
		r := spawnRun{env: map[string]string{"HOME": h}}.run(t, "--cwd", "~/x")
		if r.OK || r.Error.Kind != "environment" {
			t.Errorf("HOME %q: %+v", h, r)
		}
	}
	if r := (spawnRun{}).run(t, "--cwd", "/x"); !r.OK {
		t.Errorf("no HOME, absolute: %+v", r)
	}

	// A working directory that can't be determined is io, when it is needed.
	broken := spawnRun{wdErr: &os.PathError{Op: "getwd", Path: ".", Err: syscall.ENOENT}}
	for _, args := range [][]string{nil, {"--cwd", "rel"}} {
		if r := broken.run(t, args...); r.OK || r.Error.Kind != "io" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if r := broken.run(t, "--cwd", "/abs"); !r.OK {
		t.Errorf("absolute never asks for the working directory: %+v", r)
	}
	if r := (spawnRun{wdErr: errors.New("x"), env: home}).run(t, "--cwd", "~/y"); !r.OK {
		t.Errorf("~/ never asks for the working directory: %+v", r)
	}
}

// With --input, cwd is taken as given, and no default is filled in.
func TestSpawnInputFile(t *testing.T) {
	r := spawnRun{stdin: `{"cwd":"rel/dir"}`, env: map[string]string{"HOME": "/home/me"}}.run(t, "-i", "-")
	if r.OK || !reflect.DeepEqual(problemFields(r), []string{"/cwd"}) {
		t.Errorf("a relative cwd: %+v", r)
	}
	r = spawnRun{stdin: `{"cwd":"~/x"}`}.run(t, "-i", "-")
	if r.OK || !reflect.DeepEqual(problemFields(r), []string{"/cwd"}) {
		t.Errorf("~/: %+v", r)
	}
	r = spawnRun{stdin: `{}`}.run(t, "-i", "-")
	if r.OK || !reflect.DeepEqual(problemFields(r), []string{"/cwd"}) || r.Error.Details.Problems[0].Reason != "required" {
		t.Errorf("no cwd: %+v", r)
	}
	r = spawnRun{stdin: `{"cwd":"/w","args":["--model","opus"],"vars":{"A":"1"},"prompt":"hi","start_timeout_secs":0}`}.run(t, "-i", "-")
	if !r.OK || r.Result["cwd"] != "/w" || !reflect.DeepEqual(r.Result["args"], []any{"--model", "opus"}) || r.Result["start_timeout_secs"] != float64(0) {
		t.Errorf("%+v", r)
	}
	r = spawnRun{stdin: `{"cwd":"/w"}`}.run(t, "-i", "-", "--var", "A=1")
	if r.OK || r.Error.Kind != "usage" {
		t.Errorf("--var with --input: %+v", r)
	}
}

// os.Getwd takes $PWD when it names the working directory, as cli-spec.md
// says --cwd resolves against: the path the shell reports, not the one
// getcwd finds.
func TestSpawnCwdPWD(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(real)
	t.Setenv("PWD", link)
	var out bytes.Buffer
	noEnv := ops.OSSpawnEnv()
	noEnv.Getenv = func(string) string { return "" }
	env := Env{Stdout: &out, Stderr: &bytes.Buffer{}, Spawn: &noEnv}
	o, _, _ := execute(spawnCommand(t), []string{"spawn", "--cwd", "sub"}, env)
	var r struct{ Result struct{ Cwd string } }
	if err := json.Unmarshal(o, &r); err != nil || r.Result.Cwd != link+"/sub" {
		t.Errorf("cwd %q, want %q (%s)", r.Result.Cwd, link+"/sub", o)
	}
	// PWD naming another directory is not trusted.
	t.Setenv("PWD", t.TempDir())
	o, _, _ = execute(spawnCommand(t), []string{"spawn"}, env)
	wd, _ := os.Getwd()
	if err := json.Unmarshal(o, &r); err != nil || r.Result.Cwd != wd || r.Result.Cwd == os.Getenv("PWD") {
		t.Errorf("cwd %q, want %q", r.Result.Cwd, wd)
	}
}

func TestSpawnHelp(t *testing.T) {
	var out bytes.Buffer
	env := Env{Stdout: &out, Stderr: &bytes.Buffer{}}
	o, code, _ := execute(commands, []string{"spawn", "--help"}, env)
	if code != ExitOK || !strings.Contains(string(o), "sesshin spawn [flags] [-- <claude args...>]") {
		t.Errorf("%d: %s", code, o)
	}
}

// The real command runs the real operation, against the SpawnEnv the Env
// gives it.
func TestSpawnRuns(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	vars := map[string]string{"HOME": home, "KITTY_LISTEN_ON": "unix:/k", "KITTY_WINDOW_ID": "3", "SHELL": "/bin/zsh"}
	getenv := func(k string) string { return vars[k] }
	var spec placement.LaunchSpec
	read := ops.OSReadEnv()
	read.Getenv = getenv
	read.Backends = []placement.Backend{placementtest.Kitty{LaunchFn: func(s placement.LaunchSpec) (int64, error) { spec = s; return 9, nil }}}
	se := ops.SpawnEnv{
		ReadEnv: read,
		Token:   func() string { return strings.Repeat("ab", 16) },
		Sleep:   func(time.Duration) {},
	}
	var out, errOut bytes.Buffer
	code := Run([]string{"spawn", "--job", "api", "--cwd", cwd, "--var", "p=1", "--start-timeout-secs", "0", "--prompt", "go", "--", "--model", "opus"},
		Env{Stdout: &out, Stderr: &errOut, Spawn: &se})
	if code != ExitOK || errOut.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	checkLine(t, out.String(), "spawn-output")
	want := []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--name", "api", "--model", "opus", "--", "go"}
	if spec.Cwd != cwd || spec.Title != "api" || !slices.Equal(spec.Argv, want) || len(spec.Vars) != 1 || spec.Vars[0] != (placement.Var{Name: "p", Value: "1"}) {
		t.Errorf("launched %+v", spec)
	}
	if !strings.Contains(out.String(), `"placement":{"terminal":"kitty","socket":"unix:/k","window_id":9}`) {
		t.Errorf("stdout %s", out.String())
	}
}
