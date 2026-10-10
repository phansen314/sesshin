package hook

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/iterm2"
)

// fake is a backend of its own tag, recognized by FAKE_WINDOW, with no
// optional ability: its placement is {"terminal":"fake","window":<id>}.
type fake struct{}

func (fake) Tag() string         { return "fake" }
func (fake) Variables() []string { return []string{"FAKE_WINDOW"} }
func (fake) Recognize(getenv func(string) string) *jsonio.Object {
	w := getenv("FAKE_WINDOW")
	if w == "" {
		return nil
	}
	return &jsonio.Object{Members: []jsonio.Member{{Key: "terminal", Value: "fake"}, {Key: "window", Value: w}}}
}
func (fake) Replace(next, _ *jsonio.Object, _ bool) *jsonio.Object { return next }
func (fake) Valid(p *jsonio.Object) bool                           { return placement.TagOf(p) == "fake" }
func (fake) Address(p *jsonio.Object) *jsonio.Object               { return p }
func (fake) Stored(*jsonio.Object) (string, []placement.Var, bool) { return "", nil, false }

// syncing is fake with sync: it counts its calls and writes a title into a
// stored placement of the same window.
type syncing struct {
	fake
	calls *int
}

func (s syncing) Sync(p *jsonio.Object) (placement.Update, error) {
	*s.calls++
	want, _ := p.Get("window")
	return func(old *jsonio.Object) (*jsonio.Object, bool) {
		if got, _ := old.Get("window"); placement.TagOf(old) != "fake" || got != want {
			return old, false
		}
		out := &jsonio.Object{Members: append([]jsonio.Member(nil), old.Members...)}
		out.Set("title", "synced")
		return out, true
	}, nil
}

// syncWith runs terminal-sync in the environment given, plus HOME, over the
// backends given.
func syncWith(t *testing.T, home string, env map[string]string, list []placement.Backend) {
	t.Helper()
	Run([]string{"terminal-sync"}, Process{
		Stdin:  strings.NewReader(`{"session_id":"` + session + `","hook_event_name":"UserPromptSubmit"}`),
		Stdout: &bytes.Buffer{},
		FS:     fsys.OS{},
		Getenv: func(k string) string {
			if k == "HOME" {
				return home
			}
			return env[k]
		},
		GOOS:     "linux",
		Now:      func() time.Time { return start },
		Backends: list,
	})
}

// placedSession makes the session with sesshin.json's placement set to the
// JSON given, and returns sesshin.json's path. Placement is written by hand:
// what session-start records depends on whether the test process is nested.
func placedSession(t *testing.T, home, pl string) string {
	t.Helper()
	state := startWith(t, home, nil, `"source":"startup"`)
	path := filepath.Join(state, "sessions", session, "sesshin.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"placement": null`)) {
		t.Fatalf("sesshin.json has a placement: %s", b)
	}
	if err := os.WriteFile(path, bytes.Replace(b, []byte(`"placement": null`), []byte(`"placement":`+pl), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// terminal-sync asks the backend the environment names, and does nothing,
// silently, when that backend has no sync (design-spec.md, Terminal
// backends).
func TestTerminalSyncBackends(t *testing.T) {
	const pl = `{"terminal":"fake","window":"w1"}`
	env := map[string]string{"FAKE_WINDOW": "w1"}

	t.Run("without sync", func(t *testing.T) {
		home := t.TempDir()
		path := placedSession(t, home, pl)
		before, _ := os.ReadFile(path)
		syncWith(t, home, env, []placement.Backend{fake{}})
		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Errorf("sesshin.json changed:\n%s\n%s", before, after)
		}
		if log := readLog(t, filepath.Join(home, ".local", "state", "sesshin")); log != "" {
			t.Errorf("logged %q", log)
		}
	})

	t.Run("with sync", func(t *testing.T) {
		home := t.TempDir()
		path := placedSession(t, home, pl)
		calls := 0
		syncWith(t, home, env, []placement.Backend{syncing{calls: &calls}})
		b, _ := os.ReadFile(path)
		if calls != 1 || !bytes.Contains(b, []byte(`"title": "synced"`)) {
			t.Errorf("calls %d, sesshin.json %s", calls, b)
		}
	})

	t.Run("first in order wins", func(t *testing.T) {
		home := t.TempDir()
		path := placedSession(t, home, pl)
		calls := 0
		syncWith(t, home, env, []placement.Backend{fake{}, syncing{calls: &calls}})
		b, _ := os.ReadFile(path)
		if calls != 0 || bytes.Contains(b, []byte("synced")) {
			t.Errorf("calls %d, sesshin.json %s", calls, b)
		}
	})

	t.Run("under tmux", func(t *testing.T) {
		home := t.TempDir()
		path := placedSession(t, home, pl)
		calls := 0
		syncWith(t, home, map[string]string{"FAKE_WINDOW": "w1", "TMUX": "/tmp/tmux"}, []placement.Backend{syncing{calls: &calls}})
		b, _ := os.ReadFile(path)
		if calls != 0 || bytes.Contains(b, []byte("synced")) {
			t.Errorf("calls %d, sesshin.json %s", calls, b)
		}
	})
}

// No verb starts osascript: detection reads the environment, and a hook never
// asks iTerm2 anything (design-spec.md, The iTerm2 backend). Every verb runs in
// an iTerm2 environment over a backend whose runner fails the test when
// called, and the placement it records is the session's UUID in capitals.
func TestIterm2NeverRunsScripts(t *testing.T) {
	const uuid = "2E30574E-F9EE-4D62-BE94-56A54E66E0D5"
	runner := iterm2.RunnerFunc(func(script string, args []string, _ time.Duration) ([]byte, error) {
		t.Errorf("a hook ran osascript with %q", args)
		return nil, errors.New("not to be called")
	})
	list := []placement.Backend{iterm2.Backend{GOOS: "darwin", Runner: runner}}
	env := map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t0p0:" + strings.ToLower(uuid)}
	home := t.TempDir()
	events := map[string]string{
		"session-start": `"hook_event_name":"SessionStart","source":"startup"`,
		"user-prompt":   `"hook_event_name":"UserPromptSubmit","prompt_id":"p1"`,
		"terminal-sync": `"hook_event_name":"UserPromptSubmit","prompt_id":"p1"`,
		"post-tool-use": `"hook_event_name":"PostToolUse","prompt_id":"p1"`,
		"stop":          `"hook_event_name":"Stop","prompt_id":"p1"`,
		"notification":  `"hook_event_name":"Notification","notification_type":"permission_prompt"`,
		"compact":       `"hook_event_name":"PostCompact","trigger":"auto"`,
		"cwd-changed":   `"hook_event_name":"CwdChanged","cwd":"/tmp"`,
		"session-end":   `"hook_event_name":"SessionEnd","reason":"other"`,
		"statusline":    `"model":{"display_name":"x"},"workspace":{"current_dir":"/tmp"}`,
	}
	for _, verb := range verbs10 {
		Run([]string{verb}, Process{
			Stdin:  strings.NewReader(`{"session_id":"` + session + `",` + events[verb] + `}`),
			Stdout: &bytes.Buffer{},
			FS:     fsys.OS{},
			Getenv: func(k string) string {
				if k == "HOME" {
					return home
				}
				return env[k]
			},
			GOOS:     "linux",
			Now:      func() time.Time { return start },
			Backends: list,
		})
		if t.Failed() {
			t.Fatalf("after %s", verb)
		}
	}
	b, err := os.ReadFile(filepath.Join(home, ".local", "state", "sesshin", "sessions", session, "sesshin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"session_id": "`+uuid+`"`)) || !bytes.Contains(b, []byte(`"terminal": "iterm2"`)) {
		t.Errorf("sesshin.json: %s", b)
	}
}
