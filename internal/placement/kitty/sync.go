package kitty

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

const (
	// deadline bounds the one child process (hooks-spec.md, The contract, H4).
	deadline = time.Second
	// waitDelay bounds the wait for the child's output pipe once it has been
	// killed, so a grandchild holding the pipe can't stretch the deadline.
	waitDelay = 100 * time.Millisecond
)

// Synced is what only kitty can say while the session is alive.
type Synced struct {
	// TabTitle is the title of the tab holding the window.
	TabTitle string
	// UserVars are the window's user variables, in kitty's order; empty when
	// it has none.
	UserVars *jsonio.Object
}

// Error is a failed kitten @ ls, whichever of Sync, Windows, and
// WindowForPID asked. Timeout is the deadline passing; for Sync, the one
// failure that is logged, any other (kitten not on PATH, a socket that
// refuses, a nonzero exit, output that is not kitten @ ls's JSON, a window
// not listed) being silent.
type Error struct {
	Timeout bool
	Err     error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// IsTimeout reports whether err is the deadline passing: the only sync
// failure hooks-spec.md says to log.
func IsTimeout(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Timeout
}

// Sync runs `kitten @ --to <socket> ls` once and returns what it says of the
// window. The child gets a 1-second deadline and a WaitDelay of 100 ms, and
// this process's environment: kitten is looked up on this process's PATH,
// not on a copy. The result is an *Error.
func Sync(socket string, window int64) (Synced, error) {
	out, err := ls(socket)
	if err != nil {
		return Synced{}, err
	}
	return ParseLS(out, window)
}

// ls runs `kitten @ --to <socket> ls` once and returns its output, under the
// deadline and environment Sync's doc comment states. The result is an
// *Error.
func ls(socket string) ([]byte, error) { return lsWithin(socket, deadline) }

// lsWithin is ls under the limit given: send's 5 seconds, or Sync's 1.
func lsWithin(socket string, limit time.Duration) ([]byte, error) {
	out, timedOut, err := runKitten(limit, "", "@", "--to", socket, "ls")
	if timedOut {
		err = errors.New("kitten @ ls: " + limit.String() + " deadline passed")
	}
	if err != nil {
		return nil, &Error{Timeout: timedOut, Err: err}
	}
	return out, nil
}

// Windows runs `kitten @ --to <socket> ls` once and returns the ID of every
// window it lists: the answer to whether a window exists (design-spec.md,
// Placement). Any failure is no answer, an *Error, and the caller must not
// take it for a window being gone. It runs under Sync's deadline.
func Windows(socket string) ([]int64, error) {
	out, err := ls(socket)
	if err != nil {
		return nil, err
	}
	return ParseWindows(out)
}

// ParseWindows reads the output of `kitten @ ls` and returns the ID of every
// window it lists, in the order listed. Anything not shaped as kitty's
// answer is malformed, since reading it as no windows would free a job whose
// window still exists: an OS window that isn't an object with a list of tabs,
// a tab that isn't an object with a list of windows, or a window without a
// positive integer id. The result is an *Error.
func ParseWindows(data []byte) ([]int64, error) {
	ids := []int64{}
	err := walkWindows(data, true, func(_, w any) (bool, error) {
		n, ok := member(w, "id").(json.Number)
		if !ok {
			return false, errors.New("a window has no id")
		}
		id, ok := windowID(string(n))
		if !ok {
			return false, errors.New("a window's id is not a positive integer")
		}
		ids = append(ids, id)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// ParseLS reads the output of `kitten @ ls`: a list of OS windows, each with
// tabs, each with windows. It returns the title of the tab holding the window
// and the window's user_vars, scrubbed (design-spec.md, Placement). The result
// is an *Error.
func ParseLS(data []byte, window int64) (Synced, error) {
	want := strconv.FormatInt(window, 10)
	var synced Synced
	found := false
	err := walkWindows(data, false, func(tab, w any) (bool, error) {
		if id, ok := member(w, "id").(json.Number); !ok || string(id) != want {
			return false, nil
		}
		title, ok := member(tab, "title").(string)
		if !ok {
			return false, errors.New("tab title is not a string")
		}
		vars := &jsonio.Object{}
		if raw := member(w, "user_vars"); raw != nil {
			if vars, ok = stringMembers(raw); !ok {
				return false, errors.New("user_vars is not an object of strings")
			}
		}
		synced, found = Synced{TabTitle: model.Scrub(title), UserVars: scrubbed(vars)}, true
		return true, nil
	})
	switch {
	case err != nil:
		return Synced{}, err
	case !found:
		return Synced{}, &Error{Err: errors.New("window " + want + " not listed")}
	}
	return synced, nil
}

// walkWindows reads the output of `kitten @ ls` and calls visit with each
// window and the tab holding it, in the order listed, until visit says it is
// done or fails; its failure is the *Error returned. Anything but a list of
// OS windows is an *Error. An OS window or tab without its list is skipped,
// or, when strict, an *Error.
func walkWindows(data []byte, strict bool, visit func(tab, w any) (done bool, err error)) error {
	v, _, err := jsonio.ParseValue(data)
	if err != nil {
		return &Error{Err: err}
	}
	osWindows, ok := v.([]any)
	if !ok {
		return &Error{Err: errors.New("not a list of OS windows")}
	}
	for _, ow := range osWindows {
		tabs, ok := member(ow, "tabs").([]any)
		if !ok && strict {
			return &Error{Err: errors.New("an OS window has no list of tabs")}
		}
		for _, tab := range tabs {
			windows, ok := member(tab, "windows").([]any)
			if !ok && strict {
				return &Error{Err: errors.New("a tab has no list of windows")}
			}
			for _, w := range windows {
				done, err := visit(tab, w)
				if err != nil {
					return &Error{Err: err}
				}
				if done {
					return nil
				}
			}
		}
	}
	return nil
}

// Apply returns p with s's keys, and whether that changed anything. p must
// be a valid kitty placement (Parse); otherwise it is returned as it is,
// unchanged. p is not modified.
func Apply(p *jsonio.Object, s Synced) (*jsonio.Object, bool) {
	if _, ok := Parse(p); !ok {
		return p, false
	}
	out := &jsonio.Object{Members: append([]jsonio.Member(nil), p.Members...)}
	out.Set(keyTabTitle, s.TabTitle)
	out.Set(keyUserVars, s.UserVars)
	before, _ := jsonio.MarshalLine(p)
	after, _ := jsonio.MarshalLine(out)
	if bytes.Equal(before, after) {
		return p, false
	}
	return out, true
}

func member(v any, key string) any {
	o, ok := v.(*jsonio.Object)
	if !ok || o == nil {
		return nil
	}
	m, _ := o.Get(key)
	return m
}

func list(v any, key string) []any {
	l, _ := member(v, key).([]any)
	return l
}

// scrubbed is vars, which holds only strings, with each value scrubbed.
func scrubbed(vars *jsonio.Object) *jsonio.Object {
	out := &jsonio.Object{Members: make([]jsonio.Member, len(vars.Members))}
	for i, m := range vars.Members {
		out.Members[i] = jsonio.Member{Key: m.Key, Value: model.Scrub(m.Value.(string))}
	}
	return out
}
