package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
)

// ChildLimit is how long each self-test child may run: the timeout sesshin-hook
// is registered with (hooks-spec.md, Registration). Past it the child is
// killed and the self-test fails.
const ChildLimit = 10 * time.Second

// selfTestSession is the session the self-test's hooks record.
const selfTestSession = "5e1f7e57-1a2b-4c3d-8e4f-0123456789ab"

// removedFromChild are the variables the self-test's children run without:
// a temporary HOME relocates the config and state directories, and these keep
// the caller's own session and window out of the test (operations.md,
// install step 2).
var removedFromChild = []string{
	"XDG_CONFIG_HOME", "XDG_STATE_HOME", "CLAUDE_CONFIG_DIR", "CLAUDE_PID", "CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT", "KITTY_LISTEN_ON", "KITTY_WINDOW_ID", "TMUX", "STY",
}

// ChildResult is how one self-test child ended.
type ChildResult struct {
	// Err is why it could not be started.
	Err error
	// TimedOut: it was still running at the limit, and was killed.
	TimedOut bool
	// Exit is its exit status, -1 when a signal ended it.
	Exit           int
	Stdout, Stderr []byte
}

// SelfTester runs install's self-test. Each field is a seam a test replaces
// (implementation-spec.md, Install and uninstall); OSSelfTester sets them all.
type SelfTester struct {
	FS        fsys.FS
	GOOS      string
	Environ   func() []string
	ReadBuild func(path string) (buildinfo.Info, error)
	// TempDir makes the temporary HOME, and returns the function that
	// removes it.
	TempDir func() (dir string, remove func(), err error)
	// Child runs hook with verb, stdin, and env, for at most limit.
	Child func(hook, verb string, stdin []byte, env []string, limit time.Duration) ChildResult
}

// OSSelfTester is the self-test of the running process.
func OSSelfTester() *SelfTester {
	return &SelfTester{
		FS:        fsys.OS{},
		GOOS:      runtime.GOOS,
		Environ:   os.Environ,
		ReadBuild: buildinfo.ReadFile,
		TempDir:   osTempDir,
		Child:     osChild,
	}
}

func osTempDir() (string, func(), error) {
	dir, err := os.MkdirTemp("", "sesshin-selftest-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func osChild(hook, verb string, stdin []byte, env []string, limit time.Duration) ChildResult {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, hook, verb)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 100 * time.Millisecond // a grandchild holding the pipes can't stretch the limit
	err := cmd.Run()
	res := ChildResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		res.TimedOut = true
	case err == nil:
	case errors.As(err, &ee):
		res.Exit = ee.ExitCode()
	default:
		res.Err = err
	}
	return res
}

// selfTestFailed is a self-test-failed error: hook is the verb, or "" for sesshin-hook
// itself (null).
func selfTestFailed(path, verb, detail string) *Error {
	var hook any
	if verb != "" {
		hook = verb
	}
	msg := "self-test failed: " + detail
	if verb != "" {
		msg = "self-test failed on " + verb + ": " + detail
	}
	return &Error{
		Kind:    KindSelfTest,
		Message: msg,
		Details: map[string]any{"path": path, "hook": hook, "detail": detail},
	}
}

// Test runs the self-test of the sesshin-hook at hook against sesshin's build. It
// returns self-test-failed, io for an OS error reading sesshin-hook, or nil.
// Nothing is left behind: the temporary directory is removed either way.
func (t *SelfTester) Test(hook string, sesshin buildinfo.Info) *Error {
	fi, err := t.FS.Stat(hook)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return selfTestFailed(hook, "", "sesshin-hook is not beside sesshin")
	case err != nil:
		return IOError(hook, err)
	case !fi.Mode().IsRegular():
		return selfTestFailed(hook, "", "sesshin-hook is not a regular file")
	}
	info, err := t.ReadBuild(hook)
	if _, ok := fsys.ErrnoOf(err); ok {
		return IOError(hook, err)
	}
	switch {
	case err != nil:
		return selfTestFailed(hook, "", "sesshin-hook cannot be identified: "+err.Error())
	case !info.Identified():
		return selfTestFailed(hook, "", "sesshin-hook cannot be identified: it has no commit and its version is "+info.Version)
	case !buildinfo.Same(info, sesshin):
		return selfTestFailed(hook, "", fmt.Sprintf("sesshin-hook is from another build: %s, sesshin is %s", describeBuild(info), describeBuild(sesshin)))
	}

	home, remove, err := t.TempDir()
	if err != nil {
		return IOError(os.TempDir(), err)
	}
	defer remove()
	return t.run(hook, home)
}

func describeBuild(i buildinfo.Info) string {
	s := i.Version
	if i.Commit != "" {
		s += " (commit " + i.Commit
		if i.Modified {
			s += ", modified"
		}
		s += ")"
	}
	return s
}

// run runs the three verbs under the temporary HOME and checks what they
// wrote.
func (t *SelfTester) run(hook, home string) *Error {
	env := slices.DeleteFunc(t.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return name == "HOME" || slices.Contains(removedFromChild, name)
	})
	env = append(env, "HOME="+home)
	getenv := func(key string) string {
		for _, kv := range slices.Backward(env) {
			if v, ok := strings.CutPrefix(kv, key+"="); ok {
				return v
			}
		}
		return ""
	}
	l, err := loc.Resolve(t.GOOS, getenv)
	if err != nil {
		return selfTestFailed(hook, "", "the temporary directory is not usable as HOME: "+err.Error())
	}

	var statusline []byte
	for _, v := range []struct{ verb, payload string }{
		{"session-start", selfTestPayload("SessionStart", home, `"source":"startup","model":"claude-opus-5-5"`)},
		{"stop", selfTestPayload("Stop", home, "")},
		{"statusline", statuslinePayload(home)},
	} {
		res := t.Child(hook, v.verb, []byte(v.payload), env, ChildLimit)
		if detail := childFailure(res); detail != "" {
			return selfTestFailed(hook, v.verb, detail)
		}
		statusline = res.Stdout
	}

	dir := l.SessionDir(selfTestSession)
	if d := t.checkFile(dir, model.LifecycleName, func(b []byte) string {
		_, r := model.ReadLifecycle(b, selfTestSession)
		return unusable(r)
	}); d != "" {
		return selfTestFailed(hook, "session-start", d)
	}
	if d := t.checkFile(dir, model.SesshinName, func(b []byte) string {
		h, r := model.ReadSesshin(b)
		switch {
		case !r.Usable:
			return unusable(r)
		case h.ID == nil || *h.ID != 1:
			return "its id is not 1"
		}
		return ""
	}); d != "" {
		return selfTestFailed(hook, "session-start", d)
	}
	if d := t.checkFile(dir, model.StatuslineName, func(b []byte) string {
		_, r := model.ReadStatusline(b)
		return unusable(r)
	}); d != "" {
		return selfTestFailed(hook, "statusline", d)
	}
	switch {
	case len(statusline) == 0:
		return selfTestFailed(hook, "statusline", "it printed no line")
	case bytes.HasSuffix(statusline, []byte("\n")):
		return selfTestFailed(hook, "statusline", "its line ends in a newline")
	}
	return nil
}

// childFailure says how a child failed the test, "" when it passed: it ran,
// exited 0, and wrote nothing to stderr.
func childFailure(r ChildResult) string {
	switch {
	case r.Err != nil:
		return "could not be started: " + r.Err.Error()
	case r.TimedOut:
		return fmt.Sprintf("did not exit within %v, and was killed", ChildLimit)
	case r.Exit == -1:
		return "was ended by a signal"
	case r.Exit != 0:
		return fmt.Sprintf("exited with status %d%s", r.Exit, stderrNote(r.Stderr))
	case len(r.Stderr) > 0:
		return "wrote to stderr:" + stderrNote(r.Stderr)
	}
	return ""
}

// stderrNote is " (stderr: ...)", cut short; "" when it is empty.
func stderrNote(b []byte) string {
	s := strings.TrimSpace(strings.ToValidUTF8(string(b), "�"))
	if s == "" {
		return ""
	}
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return " " + s
}

func unusable(r model.FileResult) string {
	if r.Usable {
		return ""
	}
	return "is not valid: " + r.Reason()
}

// checkFile reads name in dir and applies check, which says what is wrong
// with its content ("" for nothing). It returns the detail of a failure,
// naming the file.
func (t *SelfTester) checkFile(dir, name string, check func([]byte) string) string {
	b, err := t.FS.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return name + " was not written"
	}
	if err != nil {
		return "reading " + name + ": " + fsys.Describe(err)
	}
	if d := check(b); d != "" {
		return name + " " + d
	}
	return ""
}

// selfTestPayload is the payload of a lifecycle event.
func selfTestPayload(event, cwd, extra string) string {
	b, _ := json.Marshal(map[string]string{"session_id": selfTestSession, "hook_event_name": event, "cwd": cwd})
	s := string(b)
	if extra != "" {
		s = s[:len(s)-1] + "," + extra + "}"
	}
	return s
}

// statuslinePayload is a minimal realistic statusline payload.
func statuslinePayload(cwd string) string {
	b, _ := json.Marshal(map[string]any{
		"session_id":      selfTestSession,
		"hook_event_name": "Status",
		"cwd":             cwd,
		"model":           map[string]string{"id": "claude-opus-5-5", "display_name": "Opus 5.5"},
		"workspace":       map[string]string{"current_dir": cwd, "project_dir": cwd},
		"cost":            map[string]any{"total_cost_usd": 0.01, "total_duration_ms": 1000, "total_api_duration_ms": 500},
		"context_window":  map[string]any{"total_input_tokens": 1000, "context_window_size": 200000, "used_percentage": 1},
	})
	return string(b)
}
