package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

type sendEnvelope struct {
	OK     bool `json:"ok"`
	Result struct {
		Session        map[string]any  `json:"session"`
		Placement      json.RawMessage `json:"placement"`
		Submitted      bool            `json:"submitted"`
		Status         string          `json:"status"`
		PermissionMode *string         `json:"permission_mode"`
		EventSeq       int64           `json:"event_seq"`
	} `json:"result"`
	Error *struct {
		Kind    string         `json:"kind"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func parseSend(t *testing.T, res Result) sendEnvelope {
	t.Helper()
	var env sendEnvelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil || strings.Count(res.Stdout, "\n") != 1 {
		t.Fatalf("exit %d, stdout %q, stderr %q: %v", res.Exit, res.Stdout, res.Stderr, err)
	}
	return env
}

func sesshinSend(t *testing.T, h *Harness, args ...string) (sendEnvelope, Result) {
	t.Helper()
	res := h.Sesshin(append([]string{"send"}, args...)...)
	return parseSend(t, res), res
}

// liveSession is session #1, started by the hooks in kitty window 7 and made
// live by a process of its own (the fake claude has exited by now): idle, with
// that process as its pid. It returns the process, which the test may kill.
// The harness is then in the caller's window, on another socket.
func liveSession(t *testing.T, h *Harness) *exec.Cmd {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	cmd := exec.Command(sleep, "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	started, err := proc.StartedAt(fsys.OS{}, int64(cmd.Process.Pid))
	if err != nil {
		t.Skip(err)
	}

	h.KittyWindow(kittySocket, "7")
	start(t, h, "startup", `"cwd":"/work"`)
	l := readLifecycle(t, h)
	pid := int64(cmd.Process.Pid)
	l.PID, l.PIDStartedAt = &pid, &started
	writeLifecycleFile(t, h, l)
	h.KittyWindow("unix:/e2e/kitty", "3")
	return cmd
}

func writeLifecycleFile(t *testing.T, h *Harness, l model.LifecycleFile) {
	t.Helper()
	b, err := jsonio.MarshalFile(l)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir(h), "lifecycle.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// kittenLSFor is kitten @ ls with the pid running in window 12, which is not
// the window the session was started in.
func kittenLSFor(pid int) string {
	return fmt.Sprintf(`[{"id":1,"tabs":[{"id":2,"title":"api","windows":[`+
		`{"id":3,"pid":1,"foreground_processes":[{"pid":1}]},`+
		`{"id":12,"pid":2,"foreground_processes":[{"pid":%d}]}]}]}]`, pid)
}

// send finds the window by the session's pid on the stored socket, never the
// stored window; pastes the text as one bracketed paste on stdin, and then
// presses Enter.
func TestSend(t *testing.T) {
	t.Parallel()
	h := New(t)
	cmd := liveSession(t, h)
	h.Kitten(kittenLSFor(cmd.Process.Pid))

	env, res := sesshinSend(t, h, "1", "--text", "hello there\nline 2")
	if res.Exit != 0 || res.Stderr != "" || !env.OK {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	if string(env.Result.Placement) != `{"terminal":"kitty","socket":"`+kittySocket+`","window_id":12}` ||
		!env.Result.Submitted || env.Result.Status != "idle" || env.Result.EventSeq != 1 || env.Result.PermissionMode != nil ||
		env.Result.Session["session_id"] != sid {
		t.Errorf("result %+v", env.Result)
	}
	want := [][]string{
		{"@", "--to", kittySocket, "ls"},
		{"@", "--to", kittySocket, "send-text", "--match", "id:12", "--bracketed-paste=disable", "--stdin"},
		{"@", "--to", kittySocket, "send-text", "--match", "id:12", `\r`},
	}
	if got := h.KittenCalls(); !slices.Equal(got[0], want[0]) || len(got) != 3 || !slices.Equal(got[1], want[1]) || !slices.Equal(got[2], want[2]) {
		t.Errorf("kitten calls %q\nwant %q", got, want)
	}
	if got := h.KittenStdin(); !slices.Equal(got, []string{"\x1b[200~hello there\nline 2\x1b[201~"}) {
		t.Errorf("kitten stdin %q", got)
	}
}

// --text-file - reads stdin exactly, and --submit=false makes one call.
func TestSendTextFileStdin(t *testing.T) {
	t.Parallel()
	h := New(t)
	cmd := liveSession(t, h)
	h.Kitten(kittenLSFor(cmd.Process.Pid))
	text := "git diff\n+ added $(x) 'q'\n"
	res := h.Run(shQuote(h.SesshinPath)+" send job:nope --text-file - --submit=false", text)
	if env := parseSend(t, Result{Stdout: res.Stdout}); res.Exit != 1 || env.OK || env.Error.Kind != "not-found" {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	res = h.Run(shQuote(h.SesshinPath)+" send 1 --text-file - --submit=false", text)
	if env := parseSend(t, Result{Stdout: res.Stdout}); res.Exit != 0 || !env.OK || env.Result.Submitted {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	calls := h.KittenCalls()
	if len(calls) != 2 || calls[1][3] != "send-text" {
		t.Errorf("kitten calls %q", calls)
	}
	if got := h.KittenStdin(); !slices.Equal(got, []string{"\x1b[200~" + text + "\x1b[201~"}) {
		t.Errorf("kitten stdin %q", got)
	}
}

// Nothing is typed, and often kitty is not asked, for each refusal.
func TestSendErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		args  []string
		setup func(t *testing.T, h *Harness, cmd *exec.Cmd)
		exit  int
		kind  string
		rule  string // conflict's rule, or terminal's reason
		calls int    // kitten calls
	}{
		{"no such session", []string{"99", "--text", "x"}, nil, 1, "not-found", "", 0},
		{"no such job", []string{"nope", "--text", "x"}, nil, 1, "not-found", "", 0},
		{"bad selector", []string{"Bad Selector", "--text", "x"}, nil, 1, "invalid-input", "", 0},
		{"an escape in the text", []string{"1", "--text", "a\x1b[201~b"}, nil, 1, "invalid-input", "", 0},
		{"no text", []string{"1"}, nil, 2, "usage", "", 0},
		{"mid-turn", []string{"1", "--text", "x"}, func(t *testing.T, h *Harness, _ *exec.Cmd) {
			l := readLifecycle(t, h)
			l.Status = "working"
			writeLifecycleFile(t, h, l)
		}, 1, "conflict", "mid-turn", 0},
		{"no placement", []string{"1", "--text", "x"}, func(t *testing.T, h *Harness, _ *exec.Cmd) {
			writeSesshin(t, h, `{"schema":2,"id":1,"job":null,"source":"hook","placement":null,"extra":{}}`)
		}, 1, "conflict", "no-placement", 0},
		{"ended", []string{"1", "--text", "x"}, func(t *testing.T, h *Harness, cmd *exec.Cmd) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}, 1, "conflict", "not-live", 0},
		{"no pid", []string{"1", "--text", "x"}, func(t *testing.T, h *Harness, _ *exec.Cmd) {
			l := readLifecycle(t, h)
			l.PID, l.PIDStartedAt = nil, nil
			writeLifecycleFile(t, h, l)
		}, 1, "terminal", "unreachable", 0},
		{"no such window, on both sockets", []string{"1", "--text", "x"}, func(t *testing.T, h *Harness, _ *exec.Cmd) {
			h.Kitten(`[{"tabs":[{"windows":[{"id":3,"foreground_processes":[{"pid":1}]}]}]}]`)
		}, 1, "terminal", "unreachable", 2},
		{"kitty does not answer", []string{"1", "--text", "x"}, func(t *testing.T, h *Harness, _ *exec.Cmd) {
			h.Unsetenv(kittenEnv) // the fake kitten then exits 1
		}, 1, "terminal", "unreachable", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			cmd := liveSession(t, h)
			h.Kitten(kittenLSFor(cmd.Process.Pid))
			if tc.setup != nil {
				tc.setup(t, h, cmd)
			}
			env, res := sesshinSend(t, h, tc.args...)
			if res.Exit != tc.exit || env.OK || env.Error == nil || env.Error.Kind != tc.kind {
				t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
			}
			if got := env.Error.Details["rule"]; tc.kind == "conflict" && got != tc.rule {
				t.Errorf("rule %v, want %s", got, tc.rule)
			}
			if got := env.Error.Details["reason"]; tc.kind == "terminal" && got != tc.rule {
				t.Errorf("reason %v, want %s", got, tc.rule)
			}
			if calls := h.KittenCalls(); len(calls) != tc.calls {
				t.Errorf("kitten calls %q, want %d", calls, tc.calls)
			}
			if in := h.KittenStdin(); len(in) != 0 {
				t.Errorf("text was pasted: %q", in)
			}
		})
	}
}

// force sends mid-turn, and the status it reports is the one at the time.
func TestSendForce(t *testing.T) {
	t.Parallel()
	h := New(t)
	cmd := liveSession(t, h)
	h.Kitten(kittenLSFor(cmd.Process.Pid))
	l := readLifecycle(t, h)
	l.Status = "needs_approval"
	writeLifecycleFile(t, h, l)
	env, res := sesshinSend(t, h, "1", "--text", "yes", "--force")
	if res.Exit != 0 || !env.OK || env.Result.Status != "needs_approval" {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if got := h.KittenStdin(); !slices.Equal(got, []string{"\x1b[200~yes\x1b[201~"}) {
		t.Errorf("kitten stdin %q", got)
	}
}
