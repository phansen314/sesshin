package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/pick"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/placementtest"
)

// restartCommand is the real restart command with an operation that echoes
// the input it was given.
func restartCommand(t *testing.T) []Command {
	return echoCommand(t, "restart", pick.DecodeInput, func(in pick.Input) map[string]any {
		return map[string]any{"query": in.Query, "args": in.Args}
	})
}

func runRestart(t *testing.T, stdin string, args ...string) (argResult, int) {
	t.Helper()
	return runEcho(t, restartCommand(t), "restart", stdin, args)
}

func TestRestartArguments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
		want  map[string]any
	}{
		{"nothing", "", nil, map[string]any{"query": "", "args": []any{}}},
		{"a query", "", []string{"--query", "killed"}, map[string]any{"query": "killed", "args": []any{}}},
		{"claude args", "", []string{"--", "--model", "opus"}, map[string]any{"query": "", "args": []any{"--model", "opus"}}},
		{"both", "", []string{"--query", "a b", "--", "--query", "x", "-i", "f"},
			map[string]any{"query": "a b", "args": []any{"--query", "x", "-i", "f"}}},
		{"bare --", "", []string{"--"}, map[string]any{"query": "", "args": []any{}}},
		{"--input", `{"query":"q","args":["--x"]}`, []string{"-i", "-"}, map[string]any{"query": "q", "args": []any{"--x"}}},
	} {
		r, code := runRestart(t, tc.stdin, tc.args...)
		if code != ExitOK || !r.OK || !reflect.DeepEqual(r.Result, tc.want) {
			t.Errorf("%s: %d %+v", tc.name, code, r)
		}
	}
}

func TestRestartUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"an argument", "", []string{"killed"}},
		{"--input with --query", `{}`, []string{"-i", "-", "--query", "x"}},
		{"--input with claude args", `{}`, []string{"-i", "-", "--", "x"}},
		{"an unknown option", "", []string{"--job", "x"}},
	} {
		r, code := runRestart(t, tc.stdin, tc.args...)
		if code != ExitUsage || r.OK || r.Error.Kind != "usage" {
			t.Errorf("%s: %d %+v", tc.name, code, r)
		}
	}
	r, code := runRestart(t, `{"query":1,"session":"1"}`, "-i", "-")
	if code != ExitError || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/query", "/session"}) {
		t.Errorf("%d %+v", code, r)
	}
}

func TestRestartHelp(t *testing.T) {
	var out bytes.Buffer
	env := Env{Stdout: &out, Stderr: &bytes.Buffer{}}
	o, code, _ := execute(commands, []string{"restart", "--help"}, env)
	if code != ExitOK || !strings.Contains(string(o), "sesshin restart [flags] [-- <claude args...>]") {
		t.Errorf("%d: %s", code, o)
	}
}

// The real command runs the real picker against the pick.Env the Env gives
// it: a cancelled pick is an error envelope and exit 1, a pick with nothing
// to offer a success that never opens fzf.
func TestRestartRuns(t *testing.T) {
	home := t.TempDir()
	vars := map[string]string{"HOME": home, "KITTY_LISTEN_ON": "unix:/k", "KITTY_WINDOW_ID": "3", "SHELL": "/bin/zsh"}
	getenv := func(k string) string { return vars[k] }
	read := ops.OSReadEnv()
	read.Getenv = getenv
	read.Backends = []placement.Backend{placementtest.Kitty{LaunchFn: func(placement.LaunchSpec) (int64, error) { return 9, nil }}}
	status, ran := 130, 0
	pe := pick.Env{
		SpawnEnv: ops.SpawnEnv{
			ReadEnv: read,
			Token:   func() string { return strings.Repeat("ab", 16) },
			Sleep:   func(time.Duration) {},
		},
		Sys: pick.System{
			LookPath:        func(string) (string, error) { return "/fzf", nil },
			Output:          func(string, []string, []string) ([]byte, []byte, int, error) { return []byte("0.74.4\n"), nil, 0, nil },
			Environ:         func() []string { return nil },
			OpenTTY:         func() error { return nil },
			RunFzf:          func(string, []string, []string, []byte) ([]byte, int, error) { ran++; return nil, status, nil },
			CatchInterrupts: func() func() { return func() {} },
		},
	}
	run := func() (string, string, int) {
		var out, errOut bytes.Buffer
		code := Run([]string{"restart"}, Env{Stdout: &out, Stderr: &errOut, Pick: &pe})
		return out.String(), errOut.String(), code
	}

	out, errOut, code := run()
	if code != ExitOK || errOut != "" || ran != 0 || !strings.Contains(out, `"actions":[]`) {
		t.Fatalf("no candidates: exit %d, ran %d, stdout %q, stderr %q", code, ran, out, errOut)
	}
	checkLine(t, out, "restart-output")

	const id = "0b6c5a3e-1f7e-4c2b-9a51-6d2f0e8b7c11"
	dir := filepath.Join(stateDir(t, home), "sessions", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	end := model.Timestamp("2026-10-04T10:05:00Z")
	b, _ := json.Marshal(model.LifecycleFile{
		SessionID: id, StartedAt: "2026-10-04T10:00:00Z", LastStartAt: "2026-10-04T10:00:00Z", Status: "idle",
		LastEventType: "stop", LastEventAt: "2026-10-04T10:00:00Z", EventSeq: 1, EndedAt: &end,
	})
	if err := os.WriteFile(filepath.Join(dir, "lifecycle.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code = run()
	if code != ExitError || ran != 1 || !strings.Contains(out, `"kind":"cancelled"`) || !strings.Contains(out, `"details":{}`) || errOut != "sesshin: cancelled: nothing was resumed\n" {
		t.Errorf("cancelled: exit %d, ran %d, stdout %q, stderr %q", code, ran, out, errOut)
	}
	checkLine(t, out, "")

	status = 2
	out, _, code = run()
	if code != ExitError || !strings.Contains(out, `"kind":"unavailable"`) || !strings.Contains(out, `"reason":"fzf-failed"`) || !strings.Contains(out, `"status":2`) {
		t.Errorf("failed: exit %d, stdout %q", code, out)
	}
	checkLine(t, out, "")
}
