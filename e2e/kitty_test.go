package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A lifecycle hook that creates sesshin.json records the kitty placement of its
// environment: here an adoption, since no session-start ran.
func TestKittyPlacementRecorded(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.KittyWindow(kittySocket, "7")
	quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`)))
	want := `{"terminal":"kitty","socket":"unix:/tmp/kitty-{kitty.pid}-4099","window_id":7}`
	if got := readSesshinPlacement(t, h); got != want {
		t.Errorf("placement %s, want %s", got, want)
	}
}

func TestKittyPlacementNull(t *testing.T) {
	t.Parallel()
	for name, set := range map[string]map[string]string{
		"under tmux":         {"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "7", "TMUX": "/tmp/tmux-1000/default,1,0"},
		"under screen":       {"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "7", "STY": "123.pts-0.host"},
		"remote control off": {"KITTY_WINDOW_ID": "7"},
		"not kitty":          {},
	} {
		t.Run(name, func(t *testing.T) {
			h := New(t)
			for k, v := range set {
				h.Setenv(k, v)
			}
			quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`)))
			if got := readSesshinPlacement(t, h); got != "null" {
				t.Errorf("placement %s, want null", got)
			}
		})
	}
}

// A hook that completes an existing sesshin.json keeps its placement.
func TestKittyPlacementKeptByLaterHooks(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.KittyWindow("unix:/x", "7")
	quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`)))
	h.Setenv("KITTY_WINDOW_ID", "8")
	quiet(t, h.Hook("stop", event("Stop", `"prompt_id":"p1"`, `"background_tasks":[]`)))
	if got := readSesshinPlacement(t, h); got != `{"terminal":"kitty","socket":"unix:/x","window_id":7}` {
		t.Errorf("placement %s", got)
	}
}

const kittenLS = `[{"id":1,"tabs":[{"id":2,"title":"first","windows":[{"id":3,"user_vars":{}}]},{"id":4,"title":"api review","windows":[{"id":7,"user_vars":{"project":"api"}}]}]}]`

const kittenLSRenamed = `[{"id":1,"tabs":[{"id":4,"title":"api deploy","windows":[{"id":7,"user_vars":{"project":"api"}}]}]}]`

func terminalSync(t *testing.T, h *Harness) {
	t.Helper()
	quiet(t, h.Hook("terminal-sync", event("UserPromptSubmit", `"prompt_id":"p2"`)))
}

// terminal-sync records the tab title and user variables, writes only when
// they changed, and never touches lifecycle.json.
func TestTerminalSync(t *testing.T) {
	t.Parallel()
	h := inKitty(t)
	lifeBefore, _ := os.ReadFile(filepath.Join(sessionDir(h), "lifecycle.json"))
	h.Kitten(kittenLS)
	terminalSync(t, h)
	want := `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"api review","user_vars":{"project":"api"}}`
	if got := readSesshinPlacement(t, h); got != want {
		t.Fatalf("placement %s, want %s", got, want)
	}
	first, fi1 := sesshinFile(t, h)

	terminalSync(t, h)
	second, fi2 := sesshinFile(t, h)
	if second != first || !os.SameFile(fi1, fi2) || !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Error("sesshin.json rewritten for the same output")
	}

	h.Kitten(kittenLSRenamed)
	terminalSync(t, h)
	if got := readSesshinPlacement(t, h); !strings.Contains(got, `"tab_title":"api deploy"`) {
		t.Errorf("placement %s", got)
	}
	if life, _ := os.ReadFile(filepath.Join(sessionDir(h), "lifecycle.json")); string(life) != string(lifeBefore) {
		t.Error("lifecycle.json changed")
	}
	noLog(t, h)
}

// A kitten that hangs is given up on after about a second: exit 0, silent,
// the timeout logged, nothing written.
func TestTerminalSyncHangs(t *testing.T) {
	t.Parallel()
	h := inKitty(t)
	before, _ := sesshinFile(t, h)
	h.KittenHangs(3 * time.Second)
	res := h.Hook("terminal-sync", event("UserPromptSubmit"))
	quiet(t, res)
	t.Logf("gave up after %v", res.Duration)
	if res.Duration < 900*time.Millisecond || res.Duration > 2*time.Second {
		t.Errorf("took %v, want about 1s", res.Duration)
	}
	if after, _ := sesshinFile(t, h); after != before {
		t.Error("sesshin.json written")
	}
	if b := hooksLog(h); !strings.Contains(b, "terminal-sync") || !strings.Contains(b, "deadline") {
		t.Errorf("hooks.log %q", b)
	}
}

// Every other failure is silent: no answer, no JSON, no such window.
func TestTerminalSyncFailsSilently(t *testing.T) {
	t.Parallel()
	for name, out := range map[string]string{"no answer": "", "bad JSON": "not json", "no such window": `[]`} {
		t.Run(name, func(t *testing.T) {
			h := inKitty(t)
			before, _ := sesshinFile(t, h)
			if out != "" {
				h.Kitten(out)
			}
			terminalSync(t, h)
			if after, _ := sesshinFile(t, h); after != before {
				t.Error("sesshin.json written")
			}
			noLog(t, h)
		})
	}
}

// No session directory: nothing is created, not even the state directory.
func TestTerminalSyncNoSession(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.KittyWindow("unix:/x", "7")
	h.Kitten(kittenLS)
	terminalSync(t, h)
	if _, err := os.Stat(h.Loc.StateDir); !os.IsNotExist(err) {
		t.Errorf("state directory: %v", err)
	}
}

// Under tmux, outside kitty, or with remote control off, there is nothing to
// do; and a placement of another window is left alone.
func TestTerminalSyncNothingToDo(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(h *Harness){
		"under tmux":         func(h *Harness) { h.Setenv("TMUX", "/tmp/tmux-1000/default,1,0") },
		"under screen":       func(h *Harness) { h.Setenv("STY", "123.pts-0.host") },
		"remote control off": func(h *Harness) { h.Unsetenv("KITTY_LISTEN_ON") },
		"another window":     func(h *Harness) { h.Setenv("KITTY_WINDOW_ID", "3") },
		"another socket":     func(h *Harness) { h.Setenv("KITTY_LISTEN_ON", "unix:/y") },
	} {
		t.Run(name, func(t *testing.T) {
			h := inKitty(t)
			before, _ := sesshinFile(t, h)
			change(h)
			h.Kitten(kittenLS)
			terminalSync(t, h)
			if after, _ := sesshinFile(t, h); after != before {
				t.Errorf("sesshin.json written: %s", after)
			}
			noLog(t, h)
		})
	}
}

// A session whose placement is null stays null.
func TestTerminalSyncNullPlacement(t *testing.T) {
	t.Parallel()
	h := New(t)
	quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`)))
	h.KittyWindow("unix:/x", "7")
	h.Kitten(kittenLS)
	terminalSync(t, h)
	if got := readSesshinPlacement(t, h); got != "null" {
		t.Errorf("placement %s", got)
	}
}
