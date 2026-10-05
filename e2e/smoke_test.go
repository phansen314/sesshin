package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/proc"
)

// lookup is what the test binary prints in "lookup" mode, run by the fake
// claude's sh -c where sesshin-hook would be: what a hook sees of its place.
type lookup struct {
	PID, PPID, GrandPPID int64
	Env                  []string
	Found                proc.Claude
}

// runLookup is the lookup mode: proc.Find as a hook calls it, by CLAUDE_PID.
func runLookup() int {
	self := int64(os.Getpid())
	ppid := ppidOf(self)
	l := lookup{PID: self, PPID: ppid, GrandPPID: ppidOf(ppid), Env: os.Environ()}
	l.Found = proc.Find(fsys.OS{}, os.Getenv("CLAUDE_PID"))
	if err := json.NewEncoder(os.Stdout).Encode(l); err != nil {
		return 1
	}
	return 0
}

// ppidOf is pid's parent from /proc/<pid>/stat, read after the last ")"
// since the name can hold anything; 0 when it can't be read.
func ppidOf(pid int64) int64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	i := strings.LastIndexByte(string(b), ')')
	var state string
	var ppid int64
	if i < 0 {
		return 0
	}
	if _, err := fmt.Sscanf(string(b[i+1:]), " %s %d", &state, &ppid); err != nil {
		return 0
	}
	return ppid
}

// runLookupUnder runs lookup mode as the hook, under the fake claude.
func runLookupUnder(t *testing.T, h *Harness) (Result, lookup) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc; macOS joins with #47")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	res := h.Run(modeEnv+"=lookup "+shQuote(self), "")
	if res.Exit != 0 || res.Stderr != "" {
		t.Fatalf("lookup exited %d: %s", res.Exit, res.Stderr)
	}
	var l lookup
	if err := json.Unmarshal([]byte(res.Stdout), &l); err != nil {
		t.Fatalf("lookup output: %v: %s", err, res.Stdout)
	}
	return res, l
}

// The verbs of hooks-spec.md, Registration.
var verbs = []string{
	"session-start", "user-prompt", "terminal-sync", "post-tool-use", "stop",
	"notification", "compact", "cwd-changed", "session-end", "statusline",
}

// The fake claude has the shape a real hook sees: CLAUDE_PID names the hook's
// parent or its parent's parent, and the lookup finds the fake claude.
func TestFakeClaudeShape(t *testing.T) {
	t.Parallel()
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%v", nested), func(t *testing.T) {
			h := New(t)
			h.Nested = nested
			res, l := runLookupUnder(t, h)
			var claudePID int64
			fmt.Sscan(envValue(l.Env, "CLAUDE_PID"), &claudePID)
			if claudePID != int64(res.ClaudePID) || (claudePID != l.PPID && claudePID != l.GrandPPID) {
				t.Errorf("CLAUDE_PID %d; claude %d, hook's parent %d, its parent %d", claudePID, res.ClaudePID, l.PPID, l.GrandPPID)
			}
			if l.Found.PID != int64(res.ClaudePID) || l.Found.Nested == nil || *l.Found.Nested != nested {
				t.Errorf("found %+v; want pid %d, nested %v", l.Found, res.ClaudePID, nested)
			}
			if !strings.HasPrefix(l.Found.StartedAt, "linux:") {
				t.Errorf("started at %q", l.Found.StartedAt)
			}
			if got := envValue(l.Env, "CLAUDECODE"); got != "1" {
				t.Errorf("hook's CLAUDECODE = %q, want 1", got)
			}
			if got := envValue(l.Env, "CLAUDE_CODE_ENTRYPOINT"); got != "cli" {
				t.Errorf("hook's CLAUDE_CODE_ENTRYPOINT = %q, want cli", got)
			}
		})
	}
}

// envValue is name's value in env, "" when unset.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// The environment is isolated, even when the test runs with every removed
// variable set, as it would under a real Claude Code, kitty, or tmux.
func TestIsolation(t *testing.T) {
	dirty := []string{
		"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "CLAUDE_CONFIG_DIR", "CLAUDE_PID",
		"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "KITTY_WINDOW_ID", "KITTY_LISTEN_ON", "TMUX", "STY",
	}
	for _, name := range dirty {
		t.Setenv(name, "/real/"+name)
	}
	h := New(t)

	for _, name := range dirty {
		if v := envValue(h.Environ(), name); v != "" {
			t.Errorf("the harness leaves %s = %q", name, v)
		}
	}
	if got := envValue(h.Environ(), "HOME"); got != h.Home || h.Home == os.Getenv("HOME") {
		t.Errorf("HOME = %q, harness home %q, real %q", got, h.Home, os.Getenv("HOME"))
	}
	for name, path := range map[string]string{"config": h.Loc.ConfigDir, "state": h.Loc.StateDir, "settings": h.Loc.ClaudeSettings} {
		if !strings.HasPrefix(path, h.Home+string(filepath.Separator)) {
			t.Errorf("%s location %q is not under HOME %q", name, path, h.Home)
		}
	}

	// What a hook sees: only the three variables the fake claude sets, with
	// its values.
	_, l := runLookupUnder(t, h)
	for _, kv := range l.Env {
		name, v, _ := strings.Cut(kv, "=")
		switch name {
		case "CLAUDE_PID", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT":
			if strings.HasPrefix(v, "/real/") {
				t.Errorf("the hook sees %s", kv)
			}
		case "HOME":
			if v != h.Home {
				t.Errorf("the hook's HOME is %q", v)
			}
		default:
			if strings.HasPrefix(v, "/real/") || isRemoved(kv) {
				t.Errorf("the hook sees %s", kv)
			}
		}
	}
	path := filepath.SplitList(envValue(l.Env, "PATH"))
	if len(path) == 0 || path[0] != bin {
		t.Errorf("PATH starts with %q, want %q", path, bin)
	}
	if _, err := os.Stat(filepath.Join(bin, "kitten")); err != nil {
		t.Error(err)
	}
}

// Setenv and Unsetenv move the locations with the environment.
func TestLocations(t *testing.T) {
	t.Parallel()
	h := New(t)
	if h.Loc.ClaudeSettings != filepath.Join(h.Home, ".claude", "settings.json") {
		t.Errorf("settings %q", h.Loc.ClaudeSettings)
	}
	other := t.TempDir()
	h.Setenv("CLAUDE_CONFIG_DIR", other)
	h.Setenv("XDG_STATE_HOME", other)
	if h.Loc.ClaudeSettings != filepath.Join(other, "settings.json") ||
		(runtime.GOOS == "linux" && h.Loc.StateDir != filepath.Join(other, "sesshin")) {
		t.Errorf("locations %+v", h.Loc)
	}
	h.Unsetenv("CLAUDE_CONFIG_DIR")
	if h.Loc.ClaudeSettings != filepath.Join(h.Home, ".claude", "settings.json") {
		t.Errorf("settings %q", h.Loc.ClaudeSettings)
	}
}

func TestSesshinVersion(t *testing.T) {
	t.Parallel()
	res := New(t).Sesshin("version")
	if res.Exit != 0 || res.Stderr != "" {
		t.Fatalf("exit %d, stderr %q", res.Exit, res.Stderr)
	}
	var env struct {
		OK     bool
		Result struct{ Version string }
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil || !env.OK || env.Result.Version == "" {
		t.Errorf("stdout %q: %+v, %v", res.Stdout, env, err)
	}
}

// The fake kitten is first on PATH, and does what the test picked.
func TestFakeKitten(t *testing.T) {
	t.Parallel()
	h := New(t)
	if res := h.Run("command -v kitten", ""); strings.TrimSpace(res.Stdout) != filepath.Join(bin, "kitten") {
		t.Errorf("kitten is %q", res.Stdout)
	}
	if res := h.Run("kitten @ ls", ""); res.Exit != 1 || res.Stdout != "" || res.Stderr != "" {
		t.Errorf("unconfigured: exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	h.Kitten(`[{"id":1}]`)
	if res := h.Run("kitten @ ls", ""); res.Exit != 0 || res.Stdout != `[{"id":1}]` {
		t.Errorf("fixed: exit %d, stdout %q", res.Exit, res.Stdout)
	}
	h.KittenHangs(300 * time.Millisecond)
	if res := h.Run("kitten @ ls", ""); res.Exit != 0 || res.Stdout != "" || res.Duration < 300*time.Millisecond {
		t.Errorf("hanging: exit %d, stdout %q, took %v", res.Exit, res.Stdout, res.Duration)
	}
	// Every run is recorded, with its arguments as given.
	h.Run(`kitten @ --to 'unix:/a b' launch --env=X='$(y)' -- ''`, "")
	want := [][]string{{"@", "ls"}, {"@", "ls"}, {"@", "ls"}, {"@", "--to", "unix:/a b", "launch", "--env=X=$(y)", "--", ""}}
	if got := h.KittenCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls %q, want %q", got, want)
	}
	if got := New(t).KittenCalls(); got != nil {
		t.Errorf("a new harness has calls %q", got)
	}
}

// The fake claude reports the child's status, output, and payload handling.
func TestRunReports(t *testing.T) {
	t.Parallel()
	h := New(t)
	res := h.Run(`cat; echo err >&2; exit 7`, "payload")
	if res.Exit != 7 || res.Signal != 0 || res.Stdout != "payload" || res.Stderr != "err\n" || res.ClaudePID == 0 {
		t.Errorf("%+v", res)
	}
	// A crash ends by a signal (The hook binary: SIGABRT on a fatal error).
	if res := h.Run(`kill -ABRT $$`, ""); res.Signal != syscall.SIGABRT || res.Exit != -1 {
		t.Errorf("killed: %+v", res)
	}
}

// A hook path that needs quoting still runs: single quotes in the path
// included (hooks-spec.md, Registration).
func TestHookPathQuoting(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/a/b-c_d.e": "/a/b-c_d.e",
		"/a b/c":     "'/a b/c'",
		"/a'b":       `'/a'\''b'`,
		"":           "''",
	} {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %q, want %q", in, got, want)
		}
	}
	h := New(t)
	h.HookPath = filepath.Join(t.TempDir(), "it's a dir", "sesshin-hook")
	if err := os.MkdirAll(filepath.Dir(h.HookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A hard link opens no file for writing: a copy's write descriptor can
	// leak into a parallel test's fork, and exec then fails with ETXTBSY
	// (golang/go#22315). A copy is the fallback across file systems.
	if err := os.Link(filepath.Join(bin, "sesshin-hook"), h.HookPath); err != nil {
		if err := copyFile(filepath.Join(bin, "sesshin-hook"), h.HookPath); err != nil {
			t.Fatal(err)
		}
	}
	if res := h.Hook("stop", ""); res.Exit != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
}
