package kitty

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/placement"
)

// launchLimit bounds the one kitten @ launch (operations.md, Launching
// claude). A variable only so a test can shorten it.
var launchLimit = 10 * time.Second

// Launch runs `kitten @ --to <socket> launch …` once, on the socket of the
// spec's caller placement, and returns the new window's ID. A caller that is
// not a kitty placement is a refusal. kitten gets this process's environment and a 10-second limit,
// with a WaitDelay of 100 ms as ls has; sesshin never asks for --copy-env, so
// the window's own environment is kitty's. The result is a *placement.LaunchError.
//
// Only spawn calls it: sesshin-hook never does.
func Launch(spec placement.LaunchSpec) (int64, error) {
	caller, ok := Parse(spec.Caller)
	if !ok {
		return 0, &placement.LaunchError{Err: errors.New("the caller is not a kitty window")}
	}
	out, timedOut, err := runKitten(launchLimit, "", launchArgs(caller.Socket, spec)...)
	if timedOut {
		return 0, &placement.LaunchError{Unknown: true, Err: errors.New("kitten @ launch: " + launchLimit.String() + " limit passed")}
	}
	if err != nil {
		return 0, &placement.LaunchError{Err: err}
	}
	s, _ := strings.CutSuffix(string(out), "\n")
	id, ok := windowID(s)
	if !ok {
		return 0, &placement.LaunchError{Unknown: true, Err: errors.New("kitten @ launch printed " + strconv.Quote(s) + ", not a window ID")}
	}
	return id, nil
}

// launchArgs is kitten's argument vector, for the socket. Every option takes its value in
// the --name=value form, so a title or a variable that begins with "-" is
// never read as an option; the program follows, as launch takes it, and
// begins with the shell's absolute path.
func launchArgs(socket string, spec placement.LaunchSpec) []string {
	typ := spec.Type
	if typ == "split" {
		typ = "window" // kitty's term
	}
	args := []string{"@", "--to", socket, "launch", "--type=" + typ, "--self", "--keep-focus", "--cwd=" + spec.Cwd}
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
