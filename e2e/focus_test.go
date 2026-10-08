package e2e

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

type focusEnvelope struct {
	OK     bool `json:"ok"`
	Result struct {
		Session   map[string]any  `json:"session"`
		Placement json.RawMessage `json:"placement"`
		Verified  bool            `json:"verified"`
		Attention *string         `json:"attention"`
	} `json:"result"`
	Error *struct {
		Kind    string         `json:"kind"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func sesshinFocus(t *testing.T, h *Harness, args ...string) (focusEnvelope, Result) {
	t.Helper()
	res := h.Sesshin(append([]string{"focus"}, args...)...)
	var env focusEnvelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil || strings.Count(res.Stdout, "\n") != 1 {
		t.Fatalf("exit %d, stdout %q, stderr %q: %v", res.Exit, res.Stdout, res.Stderr, err)
	}
	return env, res
}

// focus finds the window by the session's pid on the stored socket, then
// focuses it.
func TestFocus(t *testing.T) {
	t.Parallel()
	h := New(t)
	cmd := liveSession(t, h)
	h.Kitten(kittenLSFor(cmd.Process.Pid))

	env, res := sesshinFocus(t, h, "1")
	if res.Exit != 0 || res.Stderr != "" || !env.OK {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	if string(env.Result.Placement) != `{"terminal":"kitty","socket":"`+kittySocket+`","window_id":12}` ||
		!env.Result.Verified || env.Result.Attention == nil || env.Result.Session["session_id"] != sid {
		t.Errorf("result %+v", env.Result)
	}
	want := [][]string{
		{"@", "--to", kittySocket, "ls"},
		{"@", "--to", kittySocket, "focus-window", "--match", "id:12"},
	}
	if got := h.KittenCalls(); !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("kitten calls %q\nwant %q", got, want)
	}
	if in := h.KittenStdin(); len(in) != 0 {
		t.Errorf("stdin %q", in)
	}
}

// With no window running the pid, the stored window is focused unverified.
func TestFocusUnverified(t *testing.T) {
	t.Parallel()
	h := New(t)
	cmd := liveSession(t, h)
	_ = cmd
	h.Kitten(`[{"tabs":[{"windows":[{"id":3,"foreground_processes":[{"pid":1}]}]}]}]`)

	env, res := sesshinFocus(t, h, "1")
	if res.Exit != 0 || !env.OK || env.Result.Verified {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if string(env.Result.Placement) != `{"terminal":"kitty","socket":"`+kittySocket+`","window_id":7}` {
		t.Errorf("placement %s", env.Result.Placement)
	}
	calls := h.KittenCalls()
	if len(calls) == 0 || !slices.Equal(calls[len(calls)-1], []string{"@", "--to", kittySocket, "focus-window", "--match", "id:7"}) {
		t.Errorf("kitten calls %q", calls)
	}
}

func TestFocusErrors(t *testing.T) {
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
		{"no such session", []string{"99"}, nil, 1, "not-found", "", 0},
		{"bad selector", []string{"Bad Selector"}, nil, 1, "invalid-input", "", 0},
		{"no session", nil, nil, 2, "usage", "", 0},
		{"no placement", []string{"1"}, func(t *testing.T, h *Harness, _ *exec.Cmd) {
			writeSesshin(t, h, `{"schema":2,"id":1,"job":null,"source":"hook","placement":null,"extra":{}}`)
		}, 1, "conflict", "no-placement", 0},
		{"ended", []string{"1"}, func(t *testing.T, h *Harness, cmd *exec.Cmd) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}, 1, "conflict", "not-live", 0},
		{"focus fails", []string{"1"}, func(t *testing.T, h *Harness, _ *exec.Cmd) {
			h.Unsetenv(kittenEnv) // the fake kitten then exits 1
		}, 1, "terminal", "focus-failed", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			cmd := liveSession(t, h)
			h.Kitten(kittenLSFor(cmd.Process.Pid))
			if tc.setup != nil {
				tc.setup(t, h, cmd)
			}
			env, res := sesshinFocus(t, h, tc.args...)
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
		})
	}
}
