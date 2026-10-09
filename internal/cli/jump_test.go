package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/pick"
)

func jumpCommand(t *testing.T) []Command {
	return echoCommand(t, "jump", pick.DecodeJumpInput, func(in pick.JumpInput) map[string]any {
		return map[string]any{"query": in.Query}
	})
}

func runJump(t *testing.T, stdin string, args ...string) (argResult, int) {
	t.Helper()
	return runEcho(t, jumpCommand(t), "jump", stdin, args)
}

func TestJumpArguments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
		want  map[string]any
	}{
		{"nothing", "", nil, map[string]any{"query": ""}},
		{"a query", "", []string{"--query", "'blocked"}, map[string]any{"query": "'blocked"}},
		{"--input", `{"query":"q"}`, []string{"-i", "-"}, map[string]any{"query": "q"}},
		{"--input, empty", `{}`, []string{"-i", "-"}, map[string]any{"query": ""}},
	} {
		r, code := runJump(t, tc.stdin, tc.args...)
		if code != ExitOK || !r.OK || !reflect.DeepEqual(r.Result, tc.want) {
			t.Errorf("%s: %d %+v", tc.name, code, r)
		}
	}
}

func TestJumpUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"an argument", "", []string{"blocked"}},
		{"claude args", "", []string{"--", "x"}},
		{"--input with --query", `{}`, []string{"-i", "-", "--query", "x"}},
		{"an unknown option", "", []string{"--job", "x"}},
	} {
		r, code := runJump(t, tc.stdin, tc.args...)
		if code != ExitUsage || r.OK || r.Error.Kind != "usage" {
			t.Errorf("%s: %d %+v", tc.name, code, r)
		}
	}
	r, code := runJump(t, `{"query":1,"args":[]}`, "-i", "-")
	if code != ExitError || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/args", "/query"}) {
		t.Errorf("%d %+v", code, r)
	}
}

func TestJumpHelp(t *testing.T) {
	var out bytes.Buffer
	env := Env{Stdout: &out, Stderr: &bytes.Buffer{}}
	o, code, _ := execute(commands, []string{"jump", "--help"}, env)
	if code != ExitOK || !strings.Contains(string(o), "sesshin jump [flags]") {
		t.Errorf("%d: %s", code, o)
	}
}

// The real command runs the real picker against the pick.JumpEnv the Env
// gives it. A failure other than a cancel is shown on the terminal, once the
// envelope has been written to stdout; nothing is shown otherwise.
func TestJumpRuns(t *testing.T) {
	home := t.TempDir()
	vars := map[string]string{"HOME": home}
	read := ops.OSReadEnv()
	read.Getenv = func(k string) string { return vars[k] }
	read.StartedAt = func(int64) (string, error) { return "started", nil }
	status, ran, noFzf := 130, 0, false
	var out, errOut bytes.Buffer
	var shown []string
	var whenShown []string // what stdout held when each message was shown
	je := pick.JumpEnv{
		FocusEnv: ops.FocusEnv{ReadEnv: read},
		Sys: pick.System{
			LookPath: func(string) (string, error) {
				if noFzf {
					return "", errors.New("not found")
				}
				return "/fzf", nil
			},
			Output:          func(string, []string, []string) ([]byte, []byte, int, error) { return []byte("0.74.4\n"), nil, 0, nil },
			Environ:         func() []string { return nil },
			OpenTTY:         func() error { return nil },
			RunFzf:          func(string, []string, []string, []byte) ([]byte, int, error) { ran++; return nil, status, nil },
			CatchInterrupts: func() func() { return func() {} },
			ShowFailure: func(msg string) error {
				shown = append(shown, msg)
				whenShown = append(whenShown, out.String())
				return nil
			},
		},
	}
	run := func() (string, int) {
		out.Reset()
		errOut.Reset()
		shown, whenShown = nil, nil
		code := Run([]string{"jump"}, Env{Stdout: &out, Stderr: &errOut, Jump: &je})
		return out.String(), code
	}

	// No candidates: ok, fzf never opened, nothing shown.
	stdout, code := run()
	if code != ExitOK || ran != 0 || len(shown) != 0 || !strings.Contains(stdout, `"actions":[]`) {
		t.Fatalf("no candidates: exit %d, ran %d, shown %q, stdout %q", code, ran, shown, stdout)
	}
	checkLine(t, stdout, "jump-output")

	// A failure before fzf is shown, after the envelope.
	noFzf = true
	stdout, code = run()
	if code != ExitError || ran != 0 || len(shown) != 1 || !strings.Contains(shown[0], "fzf not found on PATH") {
		t.Fatalf("fzf-missing: exit %d, shown %q, stdout %q", code, shown, stdout)
	}
	if whenShown[0] != stdout || !strings.Contains(stdout, `"reason":"fzf-missing"`) {
		t.Errorf("shown before the envelope was written: stdout then %q, finally %q", whenShown[0], stdout)
	}
	if errOut.String() != "" {
		t.Errorf("fzf-missing: stderr %q, but the failure is shown on the terminal", errOut.String())
	}
	showFailure := je.Sys.ShowFailure
	je.Sys.ShowFailure = nil // nothing to show it: stderr keeps its note
	_, code = run()
	if code != ExitError || !strings.HasPrefix(errOut.String(), "sesshin: unavailable: ") {
		t.Errorf("fzf-missing, no terminal: exit %d, stderr %q", code, errOut.String())
	}
	je.Sys.ShowFailure = showFailure
	noFzf = false

	// One live session to pick from.
	const id = "0b6c5a3e-1f7e-4c2b-9a51-6d2f0e8b7c11"
	dir := filepath.Join(stateDir(t, home), "sessions", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pid, started := int64(4242), "started"
	b, _ := json.Marshal(model.LifecycleFile{
		SessionID: id, StartedAt: "2026-10-04T10:00:00Z", LastStartAt: "2026-10-04T10:00:00Z", Status: "waiting",
		LastEventType: "stop", LastEventAt: "2026-10-04T10:00:00Z", EventSeq: 1, PID: &pid, PIDStartedAt: &started,
	})
	if err := os.WriteFile(filepath.Join(dir, "lifecycle.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	// Cancelled: an error envelope and exit 1, and nothing shown.
	stdout, code = run()
	if code != ExitError || ran != 1 || len(shown) != 0 || !strings.Contains(stdout, `"kind":"cancelled"`) || errOut.String() != "sesshin: cancelled: nothing was focused\n" {
		t.Errorf("cancelled: exit %d, ran %d, shown %q, stdout %q, stderr %q", code, ran, shown, stdout, errOut.String())
	}
	checkLine(t, stdout, "")

	// fzf failed: shown.
	status = 2
	stdout, code = run()
	if code != ExitError || len(shown) != 1 || !strings.Contains(shown[0], "status 2") || whenShown[0] != stdout {
		t.Errorf("failed: exit %d, shown %q, stdout %q", code, shown, stdout)
	}
	if errOut.String() != "" {
		t.Errorf("failed: stderr %q", errOut.String())
	}

	// Focus failed (the session has no placement): jump ran, exit 0, and the
	// failure is shown.
	status = 0
	je.Sys.RunFzf = func(string, []string, []string, []byte) ([]byte, int, error) {
		return []byte(id + "\tline\n"), 0, nil
	}
	stdout, code = run()
	if code != ExitOK || len(shown) != 1 || !strings.Contains(shown[0], "it has no placement") || whenShown[0] != stdout {
		t.Errorf("focus failed: exit %d, shown %q, stdout %q", code, shown, stdout)
	}
	checkLine(t, stdout, "jump-output")
	if !strings.Contains(stdout, `"operation":"focus"`) || !strings.Contains(stdout, `"rule":"no-placement"`) {
		t.Errorf("stdout %q", stdout)
	}

	// Nothing is shown when the envelope was not delivered.
	errOut.Reset()
	shown = nil
	code = Run([]string{"jump"}, Env{Stdout: failingWriter{}, Stderr: &errOut, Jump: &je})
	if code != ExitNotDelivered || len(shown) != 0 {
		t.Errorf("undelivered: exit %d, shown %q", code, shown)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
