package kitty

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// launchLimit bounds the one kitten @ launch (operations.md, Launching
// claude). A variable only so a test can shorten it.
var launchLimit = 10 * time.Second

// Var is a name and a value: a user variable, or a variable to set.
type Var struct{ Name, Value string }

// LaunchSpec is one launch (operations.md, Launching claude).
type LaunchSpec struct {
	// Socket is the caller's KITTY_LISTEN_ON, passed to kitten verbatim.
	Socket string
	// Type is spawn's type: tab, split, or os-window.
	Type string
	Cwd  string
	// Title is the tab title; "" leaves the backend's own. A split keeps its
	// tab's, so it is not passed for one.
	Title string
	// Vars are the window's user variables, in order.
	Vars []Var
	// Env are the variables set in the window. Nothing is removed: over
	// remote control, kitty sets a variable named alone to
	// "_delete_this_env_var_" rather than removing it (kitty 0.49.1), which
	// would make CLAUDECODE present and the session nested.
	Env []Var
	// Argv is the whole program to run: the shell, then its arguments.
	Argv []string
}

// LaunchError is a failed launch. Unknown says whether a window may have
// opened: the limit passed, or the answer named no window. Otherwise kitten
// refused (it is missing, a socket refuses, it exited nonzero) and nothing
// was opened.
type LaunchError struct {
	Unknown bool
	Err     error
}

func (e *LaunchError) Error() string { return e.Err.Error() }
func (e *LaunchError) Unwrap() error { return e.Err }

// IsUnknown reports whether err is a launch whose outcome is unknown.
func IsUnknown(err error) bool {
	var e *LaunchError
	return errors.As(err, &e) && e.Unknown
}

// Launch runs `kitten @ --to <socket> launch …` once and returns the new
// window's ID. kitten gets this process's environment and a 10-second limit,
// with a WaitDelay of 100 ms as ls has; sesshin never asks for --copy-env, so
// the window's own environment is kitty's. The result is a *LaunchError.
//
// Only spawn calls it: sesshin-hook never does.
func Launch(spec LaunchSpec) (int64, error) {
	out, timedOut, err := runKitten(launchLimit, "", launchArgs(spec)...)
	if timedOut {
		return 0, &LaunchError{Unknown: true, Err: errors.New("kitten @ launch: " + launchLimit.String() + " limit passed")}
	}
	if err != nil {
		return 0, &LaunchError{Err: err}
	}
	s, _ := strings.CutSuffix(string(out), "\n")
	id, ok := windowID(s)
	if !ok {
		return 0, &LaunchError{Unknown: true, Err: errors.New("kitten @ launch printed " + strconv.Quote(s) + ", not a window ID")}
	}
	return id, nil
}

// launchArgs is kitten's argument vector. Every option takes its value in
// the --name=value form, so a title or a variable that begins with "-" is
// never read as an option; the program follows, as launch takes it, and
// begins with the shell's absolute path.
func launchArgs(spec LaunchSpec) []string {
	typ := spec.Type
	if typ == "split" {
		typ = "window" // kitty's term
	}
	args := []string{"@", "--to", spec.Socket, "launch", "--type=" + typ, "--self", "--keep-focus", "--cwd=" + spec.Cwd}
	if spec.Title != "" && spec.Type != "split" {
		args = append(args, "--tab-title="+spec.Title)
	}
	for _, v := range spec.Vars {
		args = append(args, "--var="+v.Name+"="+v.Value)
	}
	for _, v := range spec.Env {
		args = append(args, "--env="+v.Name+"="+v.Value)
	}
	return append(args, spec.Argv...)
}

// runKitten runs kitten with args under limit, with a WaitDelay of 100 ms
// and this process's environment, feeding it stdin when there is any. It
// returns kitten's output; on failure, timedOut says the limit passed, and
// otherwise err is kitten's, with the first line of its stderr added.
func runKitten(limit time.Duration, stdin string, args ...string) (out []byte, timedOut bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kitten", args...)
	cmd.WaitDelay = waitDelay
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, true, err
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = errors.New(err.Error() + ": " + firstLine(msg))
		}
		return nil, false, err
	}
	return stdout.Bytes(), false, nil
}

// firstLine is s up to its first newline: kitten's error, for the message.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
