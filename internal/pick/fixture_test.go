package pick

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/placement/kitty"
	"github.com/phansen314/sesshin/internal/proc"
	"github.com/phansen314/sesshin/internal/schematest"
)

var now = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// fakeFzf is the script that stands in for fzf: for --version it prints the
// version in its directory; otherwise it records its arguments (NUL
// separated), stdin, and environment, copies the preview directory and runs
// the preview command on the first line's key, prints the selection, and
// exits with the status.
const fakeFzf = `#!/bin/sh
d=$FAKE_FZF_DIR
if [ "$1" = "--version" ]; then
	env > "$d/version-env"
	if [ -f "$d/version-status" ]; then
		cat "$d/version-err" >&2
		exit "$(cat "$d/version-status")"
	fi
	cat "$d/version"
	exit 0
fi
printf '%s\0' "$@" > "$d/argv"
cat > "$d/stdin"
env > "$d/env"
for p in "$XDG_RUNTIME_DIR"/sesshin-restart-*; do
	cp -R "$p" "$d/previews"
	ls -ld "$p" | cut -c1-10 > "$d/mode"
done
prev=
next=0
for a in "$@"; do
	if [ "$next" = 1 ]; then prev=$a; next=0; fi
	if [ "$a" = --preview ]; then next=1; fi
done
key=$(head -n1 "$d/stdin" | cut -f1)
if [ -n "$prev" ] && [ -n "$key" ]; then
	sh -c "$(printf '%s' "$prev" | sed "s/{1}/'$key'/")" > "$d/preview-out"
fi
cat "$d/selection" 2>/dev/null
exit "$(cat "$d/status")"
`

// fixture is a temp HOME with ended sessions, a fake backend, and a fake
// fzf, run in a kitty window.
type fixture struct {
	t    *testing.T
	home string
	run  string // XDG_RUNTIME_DIR, with a space and a quote in its name
	cwd  string // an existing directory
	loc  loc.Locations
	fzf  string // the directory of the fake fzf and its records
	vars map[string]string
	// environ is fzf's environment, beyond what the fake needs.
	environ []string
	noFzf   bool
	noTTY   bool
	noHome  bool

	launches  []kitty.LaunchSpec
	launchErr error
	catches   int
	tokens    int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, home: t.TempDir(), cwd: t.TempDir(), fzf: t.TempDir()}
	f.run = filepath.Join(t.TempDir(), "run dir's")
	if err := os.Mkdir(f.run, 0o700); err != nil {
		t.Fatal(err)
	}
	f.vars = map[string]string{
		"KITTY_LISTEN_ON": "unix:/kitty", "KITTY_WINDOW_ID": "3", "SHELL": "/bin/zsh",
		"XDG_RUNTIME_DIR": f.run,
	}
	l, err := loc.Resolve("linux", f.getenv)
	if err != nil {
		t.Fatal(err)
	}
	f.loc = l
	if err := os.WriteFile(filepath.Join(f.fzf, "fzf"), []byte(fakeFzf), 0o700); err != nil {
		t.Fatal(err)
	}
	f.setFile("version", "0.74.4 (fake)\n")
	f.setFile("status", "0")
	return f
}

func (f *fixture) getenv(k string) string {
	if k == "HOME" {
		if f.noHome {
			return ""
		}
		return f.home
	}
	return f.vars[k]
}

// setFile writes one of the fake fzf's inputs.
func (f *fixture) setFile(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.fzf, name), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// selects makes the fake print keys, one per line, and exit with status.
func (f *fixture) selects(status int, keys ...string) {
	f.t.Helper()
	f.setFile("selection", strings.Join(keys, "\n")+"\n")
	f.setFile("status", fmt.Sprint(status))
}

// recorded is what the fake saw; "" if it never ran as the picker.
func (f *fixture) recorded(name string) string {
	b, err := os.ReadFile(filepath.Join(f.fzf, name))
	if err != nil {
		return ""
	}
	return string(b)
}

// ran reports whether the fake ran as the picker, not just for --version.
func (f *fixture) ran() bool {
	_, err := os.Stat(filepath.Join(f.fzf, "argv"))
	return err == nil
}

// argv is the picker's arguments.
func (f *fixture) argv() []string {
	s := f.recorded("argv")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\x00"), "\x00")
}

// stdin is the lines the picker was given.
func (f *fixture) stdin() []string {
	s := strings.TrimSuffix(f.recorded("stdin"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (f *fixture) env() Env {
	f.t.Helper()
	environ := append([]string{"PATH=/usr/bin:/bin", "FAKE_FZF_DIR=" + f.fzf, "XDG_RUNTIME_DIR=" + f.run}, f.environ...)
	re := ops.ReadEnv{
		FS:        fsys.OS{},
		Getenv:    f.getenv,
		GOOS:      "linux",
		Now:       func() time.Time { return now },
		StartedAt: func(int64) (string, error) { return "", proc.ErrNoProcess },
	}
	sys := OSSystem()
	sys.Environ = func() []string { return environ }
	sys.LookPath = func(name string) (string, error) {
		if f.noFzf {
			return "", exec.ErrNotFound
		}
		return filepath.Join(f.fzf, name), nil
	}
	sys.OpenTTY = func() error {
		if f.noTTY {
			return errors.New("no tty")
		}
		return nil
	}
	sys.CatchInterrupts = func() func() { f.catches++; return func() {} }
	return Env{
		SpawnEnv: ops.SpawnEnv{
			ReadEnv: re,
			Launch: func(spec kitty.LaunchSpec) (int64, error) {
				f.launches = append(f.launches, spec)
				return int64(10 + len(f.launches)), f.launchErr
			},
			Token: func() string { f.tokens++; return fmt.Sprintf("%032x", f.tokens) },
			Sleep: func(time.Duration) {},
		},
		Sys: sys,
	}
}

// uuid is the nth session's.
func uuid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func (f *fixture) write(id, name, content string) {
	f.t.Helper()
	dir := f.loc.SessionDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// add writes session n, seen ago, ended by prompt_input_exit in the
// fixture's cwd with its transcript there, and returns its UUID. sesshin.json
// gives it sesshin ID n and no job. mod changes the lifecycle first.
func (f *fixture) add(n int, ago time.Duration, mod ...func(*model.LifecycleFile)) string {
	f.t.Helper()
	id := uuid(n)
	transcript := filepath.Join(f.cwd, id+".jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	cwd, reason := f.cwd, "prompt_input_exit"
	ts := model.FormatTimestamp(now.Add(-ago))
	l := model.LifecycleFile{
		SessionID: id, Cwd: &cwd, TranscriptPath: &transcript,
		StartedAt: ts, LastStartAt: ts, Status: "idle", LastEventType: "stop", LastEventAt: ts, EventSeq: 1,
		EndedAt: &ts, EndReason: &reason,
	}
	for _, m := range mod {
		m(&l)
	}
	b, err := json.Marshal(l)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, r := model.ReadLifecycle(b, id); !r.Usable {
		f.t.Fatalf("fixture lifecycle.json unusable: %s", r.Reason())
	}
	f.write(id, "lifecycle.json", string(b))
	f.sesshin(n, "", "")
	return id
}

// sesshin writes session n's sesshin.json with a job ("" for none) and a
// placement's JSON ("" for null).
func (f *fixture) sesshin(n int, job, placement string) {
	f.t.Helper()
	jobJSON := "null"
	if job != "" {
		jobJSON = fmt.Sprintf("%q", job)
	}
	if placement == "" {
		placement = "null"
	}
	b := fmt.Sprintf(`{"schema":1,"id":%d,"job":%s,"source":"spawn","placement":%s,"extra":{}}`, n, jobJSON, placement)
	if _, r := model.ReadSesshin([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture sesshin.json unusable: %s", r.Reason())
	}
	f.write(uuid(n), "sesshin.json", b)
}

// killed: no SessionEnd was recorded, and the process is gone.
func killed(l *model.LifecycleFile) {
	pid, started := int64(424242), "never"
	l.EndedAt, l.EndReason, l.PID, l.PIDStartedAt = nil, nil, &pid, &started
}

func reason(r string) func(*model.LifecycleFile) {
	return func(l *model.LifecycleFile) { l.EndReason = &r }
}

func title(s string) func(*model.LifecycleFile) {
	return func(l *model.LifecycleFile) { l.SessionTitle = &s }
}

func cwdOf(s string) func(*model.LifecycleFile) {
	return func(l *model.LifecycleFile) { l.Cwd = &s }
}

func noTranscript(l *model.LifecycleFile) {
	p := "/nonexistent/transcript.jsonl"
	l.TranscriptPath = &p
}

func headless(l *model.LifecycleFile) { t := true; l.Nested = &t }

// restart runs restart against the fixture and checks its envelope against
// the schemas.
func (f *fixture) restart(in Input) ops.Envelope {
	f.t.Helper()
	if in.Args == nil {
		in.Args = []string{}
	}
	env := Restart(in, f.env())
	checkEnvelope(f.t, env)
	return env
}

// checkEnvelope validates env against the schemas: the envelope's, its
// error's, its warnings', and a success's result against restart-output.
func checkEnvelope(t *testing.T, env ops.Envelope) {
	t.Helper()
	check := func(id string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if ok, fail := schematest.Check(t, id, b); !ok {
			t.Errorf("%s rejects %s at %s", id, b, fail)
		}
	}
	check("envelope", env)
	if env.OK {
		check("restart-output", env.Result)
	} else {
		check("error", env.Error)
	}
	for _, w := range env.Warnings {
		check("warning", w)
	}
}

// actions is the actions of a restart that must have succeeded.
func actions(t *testing.T, env ops.Envelope) []Action {
	t.Helper()
	if !env.OK {
		t.Fatalf("restart failed: %+v", env.Error)
	}
	a := env.Result.(Output).Actions
	if a == nil {
		t.Fatal("actions is nil, not empty")
	}
	return a
}

// wantError checks env is the error of kind, with details reason.
func wantError(t *testing.T, env ops.Envelope, kind, reason string) {
	t.Helper()
	if env.OK || env.Error.Kind != kind || (reason != "" && env.Error.Details["reason"] != reason) {
		t.Fatalf("got ok %v, error %+v, want error %s %s", env.OK, env.Error, kind, reason)
	}
}
