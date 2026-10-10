package iterm2

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/placement"
)

// Launch opens a window beside the caller's, as operations.md's The iTerm2
// launch says: it writes the launch file, runs one script that creates the
// session with the command `'<sesshin>' launch-exec <nonce>`, and returns the
// new session's placement. The result is a *placement.LaunchError: a refusal
// (iTerm2 not running, the permission refused, the caller's session not found,
// the script failing) removes the launch file; the limit passing, or output
// that is not one UUID, may have opened a window and leaves it for prune.
//
// Only spawn and resume call it: sesshin-hook never does.
func (b Backend) Launch(spec placement.LaunchSpec) (*jsonio.Object, error) {
	caller, ok := Parse(spec.Caller)
	if !ok {
		return nil, &placement.LaunchError{Err: errors.New("the caller is not an iTerm2 session")}
	}
	exe := b.Executable
	if exe == nil {
		exe = os.Executable
	}
	self, err := exe()
	if err != nil {
		return nil, &placement.LaunchError{Err: errors.New("finding this binary: " + err.Error())}
	}
	if !quotable(self) {
		return nil, &placement.LaunchError{Err: errors.New("the path of this binary, " + strconv.Quote(self) + ", holds a single quote, a backslash, or a control character, which iTerm2's splitting of a command can't be given")}
	}
	env := make(map[string]string, len(spec.Env))
	for _, v := range spec.Env {
		env[v.Name] = v.Value
	}
	nonce, err := b.Launches.Write(LaunchFile{Cwd: spec.Cwd, Env: env, Argv: spec.Argv})
	if err != nil {
		return nil, &placement.LaunchError{Err: errors.New("writing the launch file: " + err.Error())}
	}
	refused := func(err error) (*jsonio.Object, error) {
		_ = b.Launches.Remove(nonce)
		return nil, &placement.LaunchError{Err: err}
	}

	args := []string{caller, spec.Type, "'" + self + "' launch-exec " + nonce, spec.Title}
	for _, v := range spec.Vars {
		args = append(args, v.Name, v.Value)
	}
	out, err := b.run(launchScript, args, launchLimit)
	switch {
	case placement.IsTimeout(err):
		return nil, &placement.LaunchError{Unknown: true, Err: err}
	case err != nil:
		return refused(err)
	}
	switch a := answer(out); {
	case a == notRunning:
		return refused(errors.New("iTerm2 is not running"))
	case a == createdUnknown:
		return nil, &placement.LaunchError{Unknown: true, Err: errors.New("iTerm2 created the session, but its id could not be read")}
	case a == notFound:
		return refused(errors.New("iTerm2 has no session " + caller + ", the caller's"))
	case isUUID(a):
		return PlacementOf(a), nil
	default:
		return nil, &placement.LaunchError{Unknown: true, Err: errors.New("osascript printed " + strconv.Quote(a) + ", not a session's unique id")}
	}
}

// quotable reports whether s can sit in single quotes for iTerm2's splitting
// of a command, which honors a backslash and ends a quote at the next single
// quote: it must hold neither, nor a control character.
func quotable(s string) bool {
	if strings.ContainsAny(s, `'\`) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
