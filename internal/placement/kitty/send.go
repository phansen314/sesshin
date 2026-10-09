package kitty

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/phansen314/sesshin/internal/placement"
)

// sendLimit bounds each kitten call of send (operations.md, send). A variable
// only so a test can shorten it.
var sendLimit = 5 * time.Second

// The bracketed-paste markers sesshin puts around the text itself, and the
// argument kitten turns into Enter (operations.md, send, Effects).
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
	enterArg   = `\r` // the two characters backslash and r, which kitten reads as CR
)

// WindowForPID runs `kitten @ --to <socket> ls` once, under a 5-second limit,
// and returns the ID of the window whose foreground processes include pid:
// never the stored window, which may now hold a shell. A failing socket,
// output that is not kitty's, and a listing with no such window are each an
// *Error.
//
// Only send calls it: sesshin-hook never does.
func WindowForPID(socket string, pid int64) (int64, error) {
	out, err := lsWithin(socket, sendLimit)
	if err != nil {
		return 0, err
	}
	return ParseWindowForPID(out, pid)
}

// ParseWindowForPID reads the output of `kitten @ ls` and returns the ID of
// the first window, in the order listed, whose foreground_processes include
// pid. A window's own `pid` (the process kitty started in it) is not looked
// at: it is the shell, not claude. Output not shaped as kitty's answer is an
// *Error, and so is no match.
func ParseWindowForPID(data []byte, pid int64) (int64, error) {
	want := strconv.FormatInt(pid, 10)
	var found int64
	err := walkWindows(data, false, func(_, w any) (bool, error) {
		for _, fp := range list(w, "foreground_processes") {
			if n, ok := member(fp, "pid").(json.Number); !ok || string(n) != want {
				continue
			}
			id, ok := member(w, "id").(json.Number)
			if !ok {
				return false, errors.New("a window has no id")
			}
			win, ok := windowID(string(id))
			if !ok {
				return false, errors.New("a window's id is not a positive integer")
			}
			found = win
			return true, nil
		}
		return false, nil
	})
	switch {
	case err != nil:
		return 0, err
	case found == 0:
		return 0, &Error{Err: errors.New("no window has the pid among its foreground processes")}
	}
	return found, nil
}

// SendText pastes text into the window as one bracketed paste and, if
// submit, presses Enter with a second call (operations.md, send, Effects 4
// and 5). sesshin writes the markers itself, with kitty's own turned off,
// because kitty's wrap each 2048-byte chunk of a longer text as a paste of
// its own. Each call has a 5-second limit, kitten gets this process's
// environment, and the result is a *SendError.
//
// Only send calls it: sesshin-hook never does.
func SendText(socket string, window int64, text string, submit bool) error {
	match := "id:" + strconv.FormatInt(window, 10)
	stdin := pasteStart + text + pasteEnd
	if err := sendCall(stdin, "@", "--to", socket, "send-text", "--match", match, "--bracketed-paste=disable", "--stdin"); err != nil {
		return &placement.SendError{Err: err}
	}
	if !submit {
		return nil
	}
	if err := sendCall("", "@", "--to", socket, "send-text", "--match", match, enterArg); err != nil {
		return &placement.SendError{Submit: true, Err: err}
	}
	return nil
}

// sendCall runs kitten with args under the send limit, feeding it stdin when
// there is any.
func sendCall(stdin string, args ...string) error {
	_, timedOut, err := runKitten(sendLimit, stdin, args...)
	if timedOut {
		return errors.New("kitten @ send-text: " + sendLimit.String() + " limit passed")
	}
	return err
}

// focusLimit bounds the kitten call of focus (operations.md, focus). A
// variable only so a test can shorten it.
var focusLimit = 5 * time.Second

// FocusWindow runs `kitten @ --to <socket> focus-window --match id:<window>`
// under a 5-second limit, which makes kitty activate the window's tab and OS
// window. A nonzero exit, the limit passing, and a missing kitten are each an
// error.
//
// Only focus calls it: sesshin-hook never does.
func FocusWindow(socket string, window int64) error {
	_, timedOut, err := runKitten(focusLimit, "", "@", "--to", socket, "focus-window", "--match", "id:"+strconv.FormatInt(window, 10))
	if timedOut {
		return errors.New("kitten @ focus-window: " + focusLimit.String() + " limit passed")
	}
	return err
}
