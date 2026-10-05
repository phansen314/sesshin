// Package e2e runs the built sesshin and sesshin-hook binaries
// (implementation-spec.md, The e2e harness). Everything here is test code, so
// the guards on sesshin-hook's own packages don't apply to it.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
)

// The test binary is also the fake claude, the fake kitten, and the lookup a
// hook makes, chosen by the first of these set, or by its own name for kitten
// (internal/proc's test runs itself the same way).
const (
	modeEnv   = "SESSHIN_E2E_MODE" // "claude" or "lookup"
	cmdEnv    = "SESSHIN_E2E_CMD"  // the shell command the fake claude runs
	kittenEnv = "SESSHIN_E2E_KITTEN_OUTPUT"
	sleepEnv  = "SESSHIN_E2E_KITTEN_SLEEP"
	// kittenLogEnv names the file the fake kitten appends its arguments to.
	kittenLogEnv = "SESSHIN_E2E_KITTEN_LOG"
)

// A fake claude or a hook that hasn't finished by then is killed, so a bug
// can't hang the suite.
const runTimeout = 30 * time.Second

// bin is the directory TestMain builds into; it holds sesshin, sesshin-hook, and
// the copies of the test binary named claude and kitten, and is removed
// afterwards.
var bin string

// testBin is the directory under bin that holds sesshin and sesshin-hook built with
// -tags sesshintest, which a test chooses with Harness.UseSesshintest.
var testBin string

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(modeEnv) == "claude":
		os.Exit(runFakeClaude())
	case os.Getenv(modeEnv) == "lookup":
		os.Exit(runLookup())
	case filepath.Base(os.Args[0]) == "kitten":
		os.Exit(runFakeKitten())
	}
	os.Exit(run(m))
}

// run builds the binaries and runs the tests, so the deferred removal of the
// build directory happens before the exit.
func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "sesshin-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	bin = dir
	testBin = filepath.Join(dir, "sesshintest")
	for _, b := range []struct {
		dir  string
		tags []string
	}{{dir, nil}, {testBin, []string{"sesshintest"}}} {
		if err := build(b.dir, b.tags...); err != nil {
			fmt.Fprintln(os.Stderr, "e2e:", err)
			return 1
		}
	}
	self, err := os.Executable()
	if err == nil {
		for _, name := range []string{"claude", "kitten"} {
			if err = copyFile(self, filepath.Join(dir, name)); err != nil {
				break
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	return m.Run()
}

// build builds sesshin and sesshin-hook into dir with the given build tags, and
// returns an error carrying the compiler's output. run builds a second set,
// with "sesshintest", into its own directory.
func build(dir string, tags ...string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"sesshin", "sesshin-hook"} {
		args := []string{"build", "-o", filepath.Join(dir, name)}
		if len(tags) > 0 {
			args = append(args, "-tags", strings.Join(tags, ","))
		}
		cmd := exec.Command("go", append(args, "./cmd/"+name)...)
		cmd.Dir = ".." // the module root: tests run in e2e/
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s: %v\n%s", name, err, out)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// removed are the variables, and prefixes (ending in "_"), every test starts
// without: anything that could point sesshin at real files or at a real
// terminal or session.
var removed = []string{
	"CLAUDE_CONFIG_DIR", "CLAUDE_PID", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT",
	"TMUX", "STY", "KITTY_", "XDG_",
}

func isRemoved(kv string) bool {
	name, _, _ := strings.Cut(kv, "=")
	return slices.ContainsFunc(removed, func(r string) bool {
		if strings.HasSuffix(r, "_") {
			return strings.HasPrefix(name, r)
		}
		return name == r
	})
}

// Harness is one test's environment: its own HOME, no variables that could
// reach real files, and the fake kitten first on PATH.
type Harness struct {
	t *testing.T

	// Home is the test's HOME, a temporary directory.
	Home string
	// Loc is where sesshin's files and Claude's settings are, resolved against
	// the test's environment; zero while HOME is unusable.
	Loc loc.Locations
	// HookPath is the sesshin-hook the fake claude runs; a test may point it
	// elsewhere.
	HookPath string
	// SesshinPath is the sesshin that Sesshin runs.
	SesshinPath string
	// Nested starts the fake claude with CLAUDECODE=1 in its own
	// environment: a session started by another session.
	Nested bool

	env []string
}

// UseSesshintest makes the harness run the sesshintest builds of sesshin-hook and sesshin
// (implementation-spec.md, Test hooks). Set SESSHIN_TEST_PANIC with Setenv to
// choose a point.
func (h *Harness) UseSesshintest() {
	h.HookPath = filepath.Join(testBin, "sesshin-hook")
	h.SesshinPath = filepath.Join(testBin, "sesshin")
}

// New returns a harness with a fresh HOME.
func New(t *testing.T) *Harness {
	t.Helper()
	h := &Harness{
		t:           t,
		Home:        t.TempDir(),
		HookPath:    filepath.Join(bin, "sesshin-hook"),
		SesshinPath: filepath.Join(bin, "sesshin"),
	}
	h.env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return isRemoved(kv) || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "PATH=") ||
			strings.HasPrefix(kv, "SESSHIN_E2E_") ||
			strings.HasPrefix(kv, "SESSHIN_TEST_")
	})
	h.Setenv("HOME", h.Home)
	h.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.Setenv(kittenLogEnv, filepath.Join(t.TempDir(), "kitten-calls"))
	return h
}

// Setenv sets a variable for every process the harness starts, and resolves
// Loc again.
func (h *Harness) Setenv(key, value string) {
	h.Unsetenv(key)
	h.env = append(h.env, key+"="+value)
	h.resolve()
}

// Unsetenv removes a variable, and resolves Loc again.
func (h *Harness) Unsetenv(key string) {
	h.env = slices.DeleteFunc(h.env, func(kv string) bool { return strings.HasPrefix(kv, key+"=") })
	h.resolve()
}

func (h *Harness) resolve() {
	h.Loc, _ = loc.Resolve(runtime.GOOS, h.getenv)
}

func (h *Harness) getenv(key string) string {
	for _, kv := range slices.Backward(h.env) {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// Environ is the environment the harness gives every process it starts.
func (h *Harness) Environ() []string { return slices.Clone(h.env) }

// Kitten makes the fake kitten print output and exit 0. Unconfigured, it
// prints nothing and exits 1.
func (h *Harness) Kitten(output string) {
	h.Setenv(kittenEnv, output)
	h.Unsetenv(sleepEnv)
}

// KittenHangs makes the fake kitten sleep for d, then exit 0 printing
// nothing; a test picks d past the 1-second deadline of terminal-sync's run
// (hooks-spec.md, The contract, H4).
func (h *Harness) KittenHangs(d time.Duration) {
	h.Setenv(sleepEnv, d.String())
	h.Unsetenv(kittenEnv)
}

// KittenCalls is the argument vector of every run of the fake kitten so far,
// in order, without the program name: what spawn launched, and asked.
func (h *Harness) KittenCalls() [][]string {
	h.t.Helper()
	b, err := os.ReadFile(h.getenv(kittenLogEnv))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			h.t.Fatalf("kitten call %q: %v", line, err)
		}
		calls = append(calls, args)
	}
	return calls
}

// KittenStdin is the standard input of every run of the fake kitten given
// --stdin so far, in order: what send pasted.
func (h *Harness) KittenStdin() []string {
	h.t.Helper()
	b, err := os.ReadFile(h.getenv(kittenLogEnv) + ".stdin")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	var ins []string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		var in string
		if err := json.Unmarshal([]byte(line), &in); err != nil {
			h.t.Fatalf("kitten stdin %q: %v", line, err)
		}
		ins = append(ins, in)
	}
	return ins
}

// Result is what a process printed and how it ended.
type Result struct {
	// Exit is the exit status; -1 when a signal ended the process or the
	// run timed out.
	Exit int
	// Signal is the signal that ended the process sh -c started, as Claude
	// Code would see it; 0 when it exited. When sh doesn't exec the command,
	// a signal that ends the command is sh's exit status 128+n instead, so
	// a test of a crash (H1's SIGABRT) accepts either.
	Signal         syscall.Signal
	Stdout, Stderr string
	Duration       time.Duration // start to exit of the process itself
	ClaudePID      int           // the fake claude's pid; 0 for Sesshin
}

// Sesshin runs the sesshin CLI directly, with no fake claude and nothing on stdin.
func (h *Harness) Sesshin(args ...string) Result {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.SesshinPath, args...)
	cmd.Env = h.env
	cmd.WaitDelay = 100 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	return Result{Exit: exitCode(h.t, err), Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
}

// Hook runs sesshin-hook <verb> as Claude Code does, with payload on stdin. An
// empty verb runs it with none.
func (h *Harness) Hook(verb, payload string) Result {
	h.t.Helper()
	command := shQuote(h.HookPath)
	if verb != "" {
		command += " " + verb
	}
	return h.Run(command, payload)
}

// Run starts the fake claude, which runs sh -c command with payload on stdin
// and reports how it ended.
func (h *Harness) Run(command, payload string) Result {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(bin, "claude"), "-test.run=^$")
	cmd.Env = append(slices.Clone(h.env), modeEnv+"=claude", cmdEnv+"="+command)
	if h.Nested {
		cmd.Env = append(cmd.Env, "CLAUDECODE=1")
	}
	cmd.Stdin = strings.NewReader(payload)
	cmd.WaitDelay = 100 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		h.t.Fatalf("fake claude: %v: %s", err, stderr.String())
	}
	if stderr.Len() > 0 {
		h.t.Fatalf("fake claude: %s", stderr.String())
	}
	var rep report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		h.t.Fatalf("fake claude report: %v: %s", err, stdout.String())
	}
	return Result{Exit: rep.Exit, Signal: syscall.Signal(rep.Signal), Stdout: rep.Stdout, Stderr: rep.Stderr,
		Duration: time.Duration(rep.DurationNS), ClaudePID: pid}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.ExitCode()
	}
	t.Fatal(err)
	return 0
}

// shQuote single-quotes path for sh -c when it holds any character outside
// A-Za-z0-9/._-, as install does (hooks-spec.md, Registration).
func shQuote(path string) string {
	plain := path != "" && !strings.ContainsFunc(path, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-", r))
	})
	if plain {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// report is what the fake claude prints for the harness.
type report struct {
	Exit           int
	Signal         int
	Stdout, Stderr string
	DurationNS     int64
}

// runFakeClaude runs as the test binary copied to claude. It sets the
// environment Claude Code gives its children, runs the command through sh -c
// with its own stdin, and prints a report. Its own environment has CLAUDECODE
// only when the harness started it nested.
func runFakeClaude() int {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout-5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", os.Getenv(cmdEnv))
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, modeEnv+"=") || strings.HasPrefix(kv, cmdEnv+"=") ||
			strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "CLAUDE_PID=") ||
			strings.HasPrefix(kv, "CLAUDE_CODE_ENTRYPOINT=")
	}), "CLAUDE_PID="+strconv.Itoa(os.Getpid()), "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli")
	cmd.Stdin = os.Stdin
	cmd.WaitDelay = 100 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	rep := report{Stdout: stdout.String(), Stderr: stderr.String(), DurationNS: int64(time.Since(start))}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		rep.Exit = ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			rep.Signal = int(ws.Signal())
		}
	default:
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(rep); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// runFakeKitten runs as the test binary copied to kitten, as the harness
// configured it.
func runFakeKitten() int {
	if path := os.Getenv(kittenLogEnv); path != "" {
		line, _ := json.Marshal(os.Args[1:])
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "kitten:", err)
			return 2
		}
		_, err = f.Write(append(line, '\n'))
		if err2 := f.Close(); err == nil {
			err = err2
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "kitten:", err)
			return 2
		}
	}
	if path := os.Getenv(kittenLogEnv); path != "" && slices.Contains(os.Args[1:], "--stdin") {
		in, _ := io.ReadAll(os.Stdin)
		line, _ := json.Marshal(string(in))
		f, err := os.OpenFile(path+".stdin", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = f.Write(append(line, '\n'))
			if err2 := f.Close(); err == nil {
				err = err2
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "kitten:", err)
			return 2
		}
	}
	if v := os.Getenv(sleepEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			fmt.Fprintln(os.Stderr, "kitten:", err)
			return 2
		}
		time.Sleep(d)
		return 0
	}
	out, ok := os.LookupEnv(kittenEnv)
	if !ok {
		return 1
	}
	_, _ = io.WriteString(os.Stdout, out)
	return 0
}

// ptr formats a pointer field's value, "<null>" for nil.
func ptr[T any](p *T) string {
	if p == nil {
		return "<null>"
	}
	return fmt.Sprint(*p)
}

// hooksLog is hooks.log's content, "" when there is none.
func hooksLog(h *Harness) string {
	b, _ := os.ReadFile(filepath.Join(h.Loc.StateDir, "hooks.log"))
	return string(b)
}

// noLog fails the test when anything was logged.
func noLog(t *testing.T, h *Harness) {
	t.Helper()
	if b := hooksLog(h); b != "" {
		t.Errorf("hooks.log: %s", b)
	}
}

// sesshinPath is the session's sesshin.json, for the test session sid.
func sesshinPath(h *Harness) string { return filepath.Join(sessionDir(h), "sesshin.json") }

// readSesshin reads the session's sesshin.json, which must be usable.
func readSesshin(t *testing.T, h *Harness) model.SesshinFile {
	t.Helper()
	b, err := os.ReadFile(sesshinPath(h))
	if err != nil {
		t.Fatal(err)
	}
	sesshin, r := model.ReadSesshin(b)
	if !r.Usable {
		t.Fatalf("sesshin.json unusable: %s", r.Reason())
	}
	return sesshin
}

// readSesshinPlacement is the session's placement as compact JSON, "null" for
// none.
func readSesshinPlacement(t *testing.T, h *Harness) string {
	t.Helper()
	sesshin := readSesshin(t, h)
	if sesshin.Placement == nil {
		return "null"
	}
	out, err := jsonio.MarshalLine(sesshin.Placement)
	if err != nil {
		t.Fatal(err)
	}
	return string(out[:len(out)-1])
}

// sesshinFile is the session's sesshin.json as text, and its file info.
func sesshinFile(t *testing.T, h *Harness) (string, os.FileInfo) {
	t.Helper()
	b, err := os.ReadFile(sesshinPath(h))
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(sesshinPath(h))
	if err != nil {
		t.Fatal(err)
	}
	return string(b), fi
}

// writeSesshin replaces sesshin.json, as the sync verb would have left it.
func writeSesshin(t *testing.T, h *Harness, content string) {
	t.Helper()
	if err := os.WriteFile(sesshinPath(h), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// kittySocket is the socket the tests' kitty windows are on, with kitty's
// placeholders in it.
const kittySocket = "unix:/tmp/kitty-{kitty.pid}-4099"

// KittyWindow puts every process the harness starts in the kitty window
// window of socket.
func (h *Harness) KittyWindow(socket, window string) {
	h.Setenv("KITTY_LISTEN_ON", socket)
	h.Setenv("KITTY_WINDOW_ID", window)
}

// inKitty starts a session in window 7 of the socket unix:/x, through a
// prompt.
func inKitty(t *testing.T) *Harness {
	t.Helper()
	h := New(t)
	h.KittyWindow("unix:/x", "7")
	quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`)))
	return h
}
