package e2e

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// jumpEnvelope is jump's envelope, in the parts these tests check.
type jumpEnvelope struct {
	OK     bool `json:"ok"`
	Result struct {
		Actions []struct {
			Operation string `json:"operation"`
			Input     struct {
				Session string `json:"session"`
			} `json:"input"`
			Output struct {
				OK     bool `json:"ok"`
				Result struct {
					Verified bool `json:"verified"`
				} `json:"result"`
				Error *struct {
					Kind string `json:"kind"`
				} `json:"error"`
			} `json:"output"`
		} `json:"actions"`
	} `json:"result"`
	Error *struct {
		Kind    string         `json:"kind"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// startJump runs sesshin jump in a terminal, with the fzf in fzfDir (none
// when ""), and the person's FZF_DEFAULT_OPTS that would end fzf without
// them.
func startJump(t *testing.T, h *Harness, fzfDir string) (*term, *syncBuf) {
	t.Helper()
	stdout := &syncBuf{}
	cmd := exec.Command(h.SesshinPath, "jump")
	cmd.Env = h.Environ()
	if fzfDir != "" {
		cmd.Env = withFzf(cmd.Env, fzfDir)
	}
	cmd.Env = append(cmd.Env, "FZF_DEFAULT_OPTS=--select-1 --exit-0 --expect=esc --print-query")
	cmd.Stdout = stdout
	return startTerm(t, cmd, 24, 100), stdout
}

// exitedNow reports whether the process has exited, after giving it a moment
// to.
func (tm *term) exitedNow(wait time.Duration) bool {
	select {
	case <-tm.exited:
		return true
	case <-time.After(wait):
		return false
	}
}

func decodeJump(t *testing.T, stdout *syncBuf) jumpEnvelope {
	t.Helper()
	var env jumpEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil || strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("stdout %q: %v", stdout.String(), err)
	}
	return env
}

// Enter focuses the pick; Esc focuses nothing, and ends at once. Against each
// fzf under test.
func TestJumpFzf(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		t.Run("preview", func(t *testing.T) {
			// The pane below the list shows the attention row of the line
			// under the cursor.
			h := New(t)
			liveSession(t, h)
			tm, stdout := startJump(t, h, fzfDir)
			tm.waitScreen("#1")
			tm.waitScreen("attention:")
			tm.send(keyEsc)
			if code := tm.wait(); code != 1 {
				t.Fatalf("exit %d, stdout %q; screen:\n%s", code, stdout.String(), tm.screen())
			}
		})
		t.Run("enter", func(t *testing.T) {
			h := New(t)
			cmd := liveSession(t, h)
			h.Kitten(kittenLSFor(cmd.Process.Pid))
			tm, stdout := startJump(t, h, fzfDir)
			tm.waitScreen("#1")
			tm.send(keyEnter)
			if code := tm.wait(); code != 0 {
				t.Fatalf("exit %d, stdout %q; screen:\n%s", code, stdout.String(), tm.screen())
			}
			env := decodeJump(t, stdout)
			if !env.OK || len(env.Result.Actions) != 1 {
				t.Fatalf("stdout %q", stdout.String())
			}
			a := env.Result.Actions[0]
			if a.Operation != "focus" || a.Input.Session != sid || !a.Output.OK || !a.Output.Result.Verified {
				t.Errorf("action %+v", a)
			}
			calls := h.KittenCalls()
			if len(calls) == 0 || !strings.Contains(strings.Join(calls[len(calls)-1], " "), "focus-window") {
				t.Errorf("kitten calls %q", calls)
			}
		})
		t.Run("esc", func(t *testing.T) {
			h := New(t)
			liveSession(t, h)
			tm, stdout := startJump(t, h, fzfDir)
			tm.waitScreen("#1")
			tm.send(keyEsc)
			// Cancelled: no failure is shown, and no key waited for.
			if code := tm.wait(); code != 1 {
				t.Fatalf("exit %d, stdout %q; screen:\n%s", code, stdout.String(), tm.screen())
			}
			env := decodeJump(t, stdout)
			if env.OK || env.Error == nil || env.Error.Kind != "cancelled" {
				t.Errorf("stdout %q", stdout.String())
			}
			if calls := h.KittenCalls(); len(calls) != 0 {
				t.Errorf("kitten calls %q", calls)
			}
		})
		t.Run("a focus that fails is shown, and waits for a key", func(t *testing.T) {
			h := New(t)
			liveSession(t, h) // the fake kitten is unconfigured: it fails every call
			tm, stdout := startJump(t, h, fzfDir)
			tm.waitScreen("#1")
			tm.send(keyEnter)
			tm.waitScreen("kitty: exit status 1")
			if tm.exitedNow(500 * time.Millisecond) {
				t.Fatalf("exited without waiting for a key; screen:\n%s", tm.screen())
			}
			// The envelope was written before the wait.
			env := decodeJump(t, stdout)
			if !env.OK || len(env.Result.Actions) != 1 || env.Result.Actions[0].Output.OK {
				t.Errorf("stdout %q", stdout.String())
			}
			tm.send("\x03") // ctrl-c is a key here
			if code := tm.wait(); code != 0 {
				t.Errorf("exit %d after the key; screen:\n%s", code, tm.screen())
			}
		})
	})
}

// A failure before fzf is shown too, in the overlay that would otherwise
// close on it.
func TestJumpUnavailableShown(t *testing.T) {
	h := New(t)
	liveSession(t, h)
	h.Setenv("PATH", t.TempDir()) // no fzf
	tm, stdout := startJump(t, h, "")
	tm.waitScreen("fzf not found on PATH")
	if tm.exitedNow(500 * time.Millisecond) {
		t.Fatalf("exited without waiting for a key; screen:\n%s", tm.screen())
	}
	env := decodeJump(t, stdout)
	if env.OK || env.Error == nil || env.Error.Details["reason"] != "fzf-missing" {
		t.Errorf("stdout %q", stdout.String())
	}
	tm.send("q")
	if code := tm.wait(); code != 1 {
		t.Errorf("exit %d; screen:\n%s", code, tm.screen())
	}
}
