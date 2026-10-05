package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fallback is the statusline's line when it has nothing to render, with no
// trailing newline (hooks-spec.md, Rendering).
const fallback = "🧠 0%"

// H1 and H3 on every path: whatever the verb and the payload, the hook exits
// 0 and writes nothing, but the statusline's line (cli-spec.md, sesshin-hook).
// The unknown verb's line is the only thing left on disk, in hooks.log.
func TestHookContract(t *testing.T) {
	t.Parallel()
	const id = "0b0d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3e"
	payloads := []struct{ name, body string }{
		{"empty", ""},
		{"valid", `{"session_id":"` + id + `","cwd":"/tmp","hook_event_name":"Stop"}`},
		{"malformed", `{"session_id":"` + id + `","cwd":`},
		{"not an object", `[1,2]`},
		{"non-UUID session_id", `{"session_id":"../../etc/passwd","cwd":"/tmp"}`},
	}
	type command struct{ name, verb string }
	commands := []command{
		{"no verb", ""},
		{"unknown", "no-such-verb"},
		{"--help", "--help"},
		{"extra arguments", "stop extra --flag"},
	}
	for _, v := range verbs {
		commands = append(commands, command{v, v})
	}
	for _, c := range commands {
		for _, p := range payloads {
			t.Run(c.name+"/"+p.name, func(t *testing.T) {
				h := New(t)
				res := h.Hook(c.verb, p.body)
				want := ""
				if c.verb == "statusline" {
					want = fallback
					if p.name == "valid" {
						want = "🧠 0% | 📁 tmp" // rendered from what the payload carries
					}
				}
				if res.Exit != 0 || res.Stdout != want || res.Stderr != "" {
					t.Errorf("exit %d, stdout %q, stderr %q; want 0, %q, and no stderr", res.Exit, res.Stdout, res.Stderr, want)
				}
				if res.Duration <= 0 {
					t.Errorf("duration %v", res.Duration)
				}
				// Nothing is written under HOME but hooks.log in the state
				// directory, and only when there was a line to log.
				var created []string
				err := filepath.WalkDir(h.Home, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if !d.IsDir() {
						rel, _ := filepath.Rel(h.Home, path)
						created = append(created, rel)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				var logged bool
				if b, err := os.ReadFile(filepath.Join(h.Loc.StateDir, "hooks.log")); err == nil {
					logged = true
					if c.name == "unknown" || c.name == "--help" {
						if !strings.Contains(string(b), " "+c.verb+" ") {
							t.Errorf("hooks.log %q lacks the verb %q", b, c.verb)
						}
					}
				}
				stateLog, _ := filepath.Rel(h.Home, filepath.Join(h.Loc.StateDir, "hooks.log"))
				wantFiles := []string(nil)
				if logged {
					wantFiles = []string{stateLog}
				}
				// A verb that records adopts a session whose id it read: it
				// leaves its lifecycle.json and sesshin.json, and state.json.
				if first, _, _ := strings.Cut(c.verb, " "); (p.name == "valid" || p.name == "malformed") &&
					slices.Contains([]string{"session-start", "user-prompt", "post-tool-use", "stop", "compact"}, first) {
					sess := filepath.Join(".local", "state", "sesshin", "sessions", id)
					wantFiles = append(wantFiles, filepath.Join(sess, "sesshin.json"), filepath.Join(sess, "lifecycle.json"),
						filepath.Join(filepath.Dir(stateLog), "state.json"))
					slices.Sort(wantFiles)
				}
				if !slices.Equal(created, wantFiles) {
					t.Errorf("files created %v, want %v", created, wantFiles)
				}
				// Logged: an unknown or missing verb, a session_id that is no
				// UUID, or a payload cut short (which keeps the id it read, so
				// the verb runs), and never an empty payload.
				wantLogged := p.body != "" && (c.name == "no verb" || c.name == "unknown" || c.name == "--help" ||
					p.name == "not an object" || p.name == "non-UUID session_id" || p.name == "malformed")
				if logged != wantLogged {
					t.Errorf("logged %v, want %v", logged, wantLogged)
				}
			})
		}
	}
}

// H3: fd 2 is /dev/null, so a stray write can't reach Claude. A hook's
// stderr is a pipe in the harness, and nothing test-only is built into the
// shipped binary to ask it, so a hook held open on stdin (for half a second)
// is looked at from outside: the shell reads where /proc says its fd 2 leads.
func TestHookStderrIsDevNull(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	h := New(t)
	// exec makes the pipeline's subshell the hook itself, so $! is its pid.
	script := `sleep 0.5 | exec ` + shQuote(h.HookPath) + ` stop & pid=$!
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  l=$(readlink /proc/$pid/fd/2)
  case $l in pipe:*) sleep 0.05;; *) break;; esac
done
echo "$l"
wait`
	res := h.Run(script, "")
	if res.Exit != 0 || strings.TrimSpace(res.Stdout) != "/dev/null" || res.Stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want fd 2 at /dev/null", res.Exit, res.Stdout, res.Stderr)
	}
}
