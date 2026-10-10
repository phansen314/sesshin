package iterm2

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Runner runs one AppleScript, with its arguments as the `on run` handler's
// argv, and returns what it printed. Its error says TimedOut() for the limit
// passing; any other is osascript's, with the first line of its stderr in the
// message. Tests supply their own.
type Runner interface {
	Run(script string, args []string, limit time.Duration) (stdout []byte, err error)
}

// RunnerFunc is a Runner that is a function.
type RunnerFunc func(script string, args []string, limit time.Duration) ([]byte, error)

// Run calls f.
func (f RunnerFunc) Run(script string, args []string, limit time.Duration) ([]byte, error) {
	return f(script, args, limit)
}

// osascriptPath is the system's osascript, by its absolute path, as nothing
// on PATH should stand in for it.
const osascriptPath = "/usr/bin/osascript"

// waitDelay bounds the wait for osascript's output pipe once it has been
// killed, so a grandchild holding the pipe can't stretch the limit.
const waitDelay = 100 * time.Millisecond

// Error is a failed osascript run. Timeout is the limit passing.
type Error struct {
	Timeout bool
	Err     error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// TimedOut reports whether the failure is the limit passing
// (placement.IsTimeout).
func (e *Error) TimedOut() bool { return e.Timeout }

// osascript is the real Runner: `/usr/bin/osascript -e <script> <args…>`,
// with this process's environment.
type osascript struct{}

func (osascript) Run(script string, args []string, limit time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, osascriptPath, append([]string{"-e", script}, args...)...)
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, &Error{Timeout: true, Err: errors.New("osascript: " + limit.String() + " limit passed")}
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = errors.New(err.Error() + ": " + firstLine(msg))
		}
		return nil, &Error{Err: err}
	}
	return stdout.Bytes(), nil
}

// firstLine is s up to its first newline: osascript's error, for the message.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// refused is osascript's error number for macOS denying this app the control
// of another (errAEEventNotPermitted).
const refused = "(-1743)"

// denied is the message for a refused permission (design-spec.md, The iTerm2
// backend): it names the setting.
const denied = "macOS refused permission to control iTerm2; allow it in System Settings → Privacy & Security → Automation"

// notRunning is what a script that found iTerm2 not running prints, having
// asked it nothing; it is not a UUID or a tty, so no answer can be taken for
// it. notFound is the same for a session the script did not find.
const (
	notRunning = "not-running"
	notFound   = "not-found"
)

// runError is err from a Runner as a message for the caller: osascript's
// error -1743 names the permission to grant, and a timeout keeps its type.
func runError(err error) error {
	if strings.Contains(err.Error(), refused) {
		return &Error{Err: errors.New(denied + ": " + err.Error())}
	}
	return err
}
