package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/placementtest"
)

// resumeCommand is the real resume command with an operation that echoes the
// input it was given.
func resumeCommand(t *testing.T) []Command {
	return echoCommand(t, "resume", ops.DecodeResumeInput, func(in ops.ResumeInput) map[string]any {
		return map[string]any{"session": in.Selector.Raw, "job": in.Job, "args": in.Args, "start_timeout_secs": in.StartTimeoutSecs}
	})
}

func runResume(t *testing.T, stdin string, args ...string) (argResult, int) {
	t.Helper()
	return runEcho(t, resumeCommand(t), "resume", stdin, args)
}

func TestResumeArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"an ID", []string{"12"}, map[string]any{"session": "12", "job": "", "args": []any{}, "start_timeout_secs": float64(15)}},
		{"a job", []string{"api"}, map[string]any{"session": "api", "job": "", "args": []any{}, "start_timeout_secs": float64(15)}},
		{"job:", []string{"job:deadbeef", "--job", "other", "--start-timeout-secs", "0"},
			map[string]any{"session": "job:deadbeef", "job": "other", "args": []any{}, "start_timeout_secs": float64(0)}},
		{"options after the session", []string{"12", "--start-timeout-secs", "3", "--", "--model", "opus"},
			map[string]any{"session": "12", "job": "", "args": []any{"--model", "opus"}, "start_timeout_secs": float64(3)}},
		{"options before the session", []string{"--job", "j", "12"},
			map[string]any{"session": "12", "job": "j", "args": []any{}, "start_timeout_secs": float64(15)}},
		{"nothing is an option after --", []string{"12", "--", "--job", "x", "-i", "f", "list"},
			map[string]any{"session": "12", "job": "", "args": []any{"--job", "x", "-i", "f", "list"}, "start_timeout_secs": float64(15)}},
		{"bare --", []string{"12", "--"},
			map[string]any{"session": "12", "job": "", "args": []any{}, "start_timeout_secs": float64(15)}},
		{"empty and spaces", []string{"12", "--", "", "a b", "$(x)"},
			map[string]any{"session": "12", "job": "", "args": []any{"", "a b", "$(x)"}, "start_timeout_secs": float64(15)}},
	} {
		r, code := runResume(t, "", tc.args...)
		if code != ExitOK || !r.OK || !reflect.DeepEqual(r.Result, tc.want) {
			t.Errorf("%s: %d %+v", tc.name, code, r)
		}
	}
}

func TestResumeUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"no session", "", nil},
		{"only claude args", "", []string{"--", "x"}},
		{"a second session", "", []string{"1", "2"}},
		{"a bare word before --", "", []string{"1", "extra", "--", "x"}},
		{"--input with the session", `{"session":"1"}`, []string{"-i", "-", "1"}},
		{"--input with --job", `{"session":"1"}`, []string{"-i", "-", "--job", "j"}},
		{"--input with claude args", `{"session":"1"}`, []string{"-i", "-", "--", "x"}},
		{"an unknown option", "", []string{"1", "--cwd", "/w"}},
	} {
		r, code := runResume(t, tc.stdin, tc.args...)
		if code != ExitUsage || r.OK || r.Error.Kind != "usage" {
			t.Errorf("%s: %d %+v", tc.name, code, r)
		}
	}
}

func TestResumeInput(t *testing.T) {
	r, code := runResume(t, `{"session":"job:api","args":["--model","opus"],"start_timeout_secs":0,"job":"j"}`, "-i", "-")
	want := map[string]any{"session": "job:api", "job": "j", "args": []any{"--model", "opus"}, "start_timeout_secs": float64(0)}
	if code != ExitOK || !reflect.DeepEqual(r.Result, want) {
		t.Errorf("%d %+v", code, r)
	}
	// The same checks as the command line's.
	r, code = runResume(t, `{"session":"Bad Selector"}`, "-i", "-")
	if code != ExitError || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/session"}) {
		t.Errorf("%d %+v", code, r)
	}
	r, code = runResume(t, "", "Bad Selector", "--job", "12")
	if code != ExitError || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/job", "/session"}) {
		t.Errorf("%d %+v", code, r)
	}
}

func TestResumeHelp(t *testing.T) {
	var out bytes.Buffer
	env := Env{Stdout: &out, Stderr: &bytes.Buffer{}}
	o, code, _ := execute(commands, []string{"resume", "--help"}, env)
	if code != ExitOK || !strings.Contains(string(o), "sesshin resume <session> [flags] [-- <claude args...>]") {
		t.Errorf("%d: %s", code, o)
	}
}

// The real command runs the real operation, against the SpawnEnv the Env
// gives it.
func TestResumeRuns(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	vars := map[string]string{"HOME": home, "KITTY_LISTEN_ON": "unix:/k", "KITTY_WINDOW_ID": "3", "SHELL": "/bin/zsh"}
	getenv := func(k string) string { return vars[k] }
	const id = "0b6c5a3e-1f7e-4c2b-9a51-6d2f0e8b7c11"
	dir := filepath.Join(stateDir(t, home), "sessions", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lifecycle := model.LifecycleFile{
		SessionID: id, StartedAt: "2026-10-04T10:00:00Z", LastStartAt: "2026-10-04T10:00:00Z", Status: "idle",
		LastEventType: "stop", LastEventAt: "2026-10-04T10:00:00Z", EventSeq: 1, EndedAt: ptr(model.Timestamp("2026-10-04T10:05:00Z")), Cwd: &cwd,
	}
	b, _ := json.Marshal(lifecycle)
	if err := os.WriteFile(filepath.Join(dir, "lifecycle.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

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
	code := Run([]string{"resume", "0b6c5a3e", "--start-timeout-secs", "0", "--", "--model", "opus"},
		Env{Stdout: &out, Stderr: &errOut, Spawn: &se})
	if code != ExitOK || errOut.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	checkLine(t, out.String(), "resume-output")
	want := []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--resume", id, "--model", "opus"}
	if spec.Cwd != cwd || spec.Type != "tab" || !slices.Equal(spec.Argv, want) {
		t.Errorf("launched %+v", spec)
	}
}

func ptr[T any](v T) *T { return &v }
