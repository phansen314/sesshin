package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/model"
)

// restartEnvelope is restart's envelope, in the parts these tests check.
type restartEnvelope struct {
	OK     bool `json:"ok"`
	Result struct {
		Actions []struct {
			Operation string `json:"operation"`
			Input     struct {
				Session string   `json:"session"`
				Args    []string `json:"args"`
			} `json:"input"`
			Output struct {
				OK bool `json:"ok"`
			} `json:"output"`
		} `json:"actions"`
	} `json:"result"`
	Error *struct {
		Kind    string         `json:"kind"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

const otherSid = "1c1d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3f"

// twoEnded is the harness with session #1 ended by a signal (killed, "api
// review") and session #2, older, ended by /exit.
func twoEnded(t *testing.T) *Harness {
	t.Helper()
	h := New(t)
	endedSession(t, h, "")
	seen := model.FormatTimestamp(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	cwd, reason := h.Home, "prompt_input_exit"
	b, err := json.Marshal(model.LifecycleFile{
		SessionID: otherSid, Cwd: &cwd, StartedAt: seen, LastStartAt: seen, Status: "idle",
		LastEventType: "stop", LastEventAt: seen, EventSeq: 1, EndedAt: &seen, EndReason: &reason,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.Loc.StateDir, "sessions", otherSid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sesshin := `{"schema":2,"id":2,"job":null,"source":"hook","placement":null,"extra":{}}`
	for name, content := range map[string]string{"lifecycle.json": string(b), "sesshin.json": sesshin} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// startRestart runs sesshin restart in a terminal, with the fzf in fzfDir, and
// the person's FZF_DEFAULT_OPTS that would end fzf without them.
func startRestart(t *testing.T, h *Harness, fzfDir string, args ...string) (*term, *bytes.Buffer) {
	t.Helper()
	var stdout bytes.Buffer
	cmd := exec.Command(h.SesshinPath, append([]string{"restart"}, args...)...)
	cmd.Env = append(withFzf(h.Environ(), fzfDir), "FZF_DEFAULT_OPTS=--select-1 --exit-0 --expect=esc --print-query")
	cmd.Stdout = &stdout
	return startTerm(t, cmd, 24, 100), &stdout
}

// The reboot's recovery: type a query, ctrl-a, Enter resumes every match, in
// a new tab; Esc resumes nothing. Against each fzf under test.
func TestRestartFzf(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		t.Run("query, ctrl-a, enter", func(t *testing.T) {
			h := twoEnded(t)
			tm, stdout := startRestart(t, h, fzfDir, "--", "--model", "opus")
			tm.waitScreen("exited")
			tm.waitScreen("killed")
			tm.send("killed")
			tm.waitScreen("> killed")
			tm.send(keyCtrlA)
			tm.waitScreen("(1)") // one marked: fzf's count in the info line
			tm.send(keyEnter)
			if code := tm.wait(); code != 0 {
				t.Fatalf("exit %d, stdout %q; screen:\n%s", code, stdout.String(), tm.screen())
			}
			var env restartEnvelope
			if err := json.Unmarshal(stdout.Bytes(), &env); err != nil || strings.Count(stdout.String(), "\n") != 1 {
				t.Fatalf("stdout %q: %v", stdout.String(), err)
			}
			if !env.OK || len(env.Result.Actions) != 1 {
				t.Fatalf("stdout %q", stdout.String())
			}
			a := env.Result.Actions[0]
			if a.Operation != "resume" || a.Input.Session != sid || !a.Output.OK || !slices.Equal(a.Input.Args, []string{"--model", "opus"}) {
				t.Errorf("action %+v", a)
			}
			calls := h.KittenCalls()
			if len(calls) != 1 || !slices.Equal(calls[0][len(calls[0])-4:], []string{"--resume", sid, "--model", "opus"}) {
				t.Errorf("kitten calls %q", calls)
			}
		})
		t.Run("esc", func(t *testing.T) {
			h := twoEnded(t)
			tm, stdout := startRestart(t, h, fzfDir)
			tm.waitScreen("exited")
			tm.send(keyEsc)
			if code := tm.wait(); code != 1 {
				t.Fatalf("exit %d, stdout %q; screen:\n%s", code, stdout.String(), tm.screen())
			}
			var env restartEnvelope
			if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if env.OK || env.Error == nil || env.Error.Kind != "cancelled" || len(env.Error.Details) != 0 {
				t.Errorf("stdout %q", stdout.String())
			}
			if calls := h.KittenCalls(); len(calls) != 0 {
				t.Errorf("kitten calls %q", calls)
			}
		})
	})
}

// Without a terminal, or fzf, restart fails before anything is read from
// the sessions beyond its own checks.
func TestRestartUnavailable(t *testing.T) {
	t.Parallel()
	h := twoEnded(t)
	h.Setenv("PATH", t.TempDir()) // no fzf
	res := h.Sesshin("restart")
	var env restartEnvelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("exit %d, stdout %q: %v", res.Exit, res.Stdout, err)
	}
	if res.Exit != 1 || env.OK || env.Error.Kind != "unavailable" || env.Error.Details["reason"] != "fzf-missing" {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if len(h.KittenCalls()) != 0 {
		t.Error("kitten was run")
	}
}
