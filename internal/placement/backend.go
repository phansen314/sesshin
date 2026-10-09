package placement

import (
	"errors"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Var is a name and a value: a user variable, or a variable to set.
type Var struct{ Name, Value string }

// LaunchSpec is one launch (operations.md, Launching claude).
type LaunchSpec struct {
	// Caller is the caller's placement, as Detect recognized it: where the
	// new window opens.
	Caller *jsonio.Object
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
// opened: the limit passed, or the answer named no window. Otherwise the
// backend refused and nothing was opened.
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

// SendError is a failed send. Submit says the text was pasted and only
// Enter failed; otherwise the paste failed, and some of the text may be in
// the input box.
type SendError struct {
	Submit bool
	Err    error
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }

// IsSubmit reports whether err is a send whose paste succeeded.
func IsSubmit(err error) bool {
	var e *SendError
	return errors.As(err, &e) && e.Submit
}

// IsTimeout reports whether err says its deadline passed (a TimedOut method
// that returns true): the only sync failure hooks-spec.md says to log.
func IsTimeout(err error) bool {
	var e interface{ TimedOut() bool }
	return errors.As(err, &e) && e.TimedOut()
}

// Backend is what every terminal backend provides (design-spec.md, Terminal
// backends). Its optional abilities are the interfaces below, found by type
// assertion; a backend has each whole or not at all.
type Backend interface {
	// Tag is the value of a placement's terminal key.
	Tag() string
	// Recognize returns the placement the environment names, or nil. It
	// starts no process. Multiplexed and a nested claude are ruled out
	// before it is asked.
	Recognize(getenv func(string) string) *jsonio.Object
	// Replace returns next with the keys only a sync writes, taken from old
	// when old is a valid placement of the same window or the session is
	// resumed. A nil next is nil. Neither argument is changed.
	Replace(next, old *jsonio.Object, resumed bool) *jsonio.Object
	// Valid reports whether p is a valid placement of this backend: false
	// for another backend's, or one that is not valid, nil included.
	Valid(p *jsonio.Object) bool
	// Address is the minimal placement that addresses the window of a valid
	// p, without the keys only a sync writes, as Recognize builds one.
	Address(p *jsonio.Object) *jsonio.Object
	// Stored returns the tab title ("" for none) and the user variables of a
	// valid placement, as resume reopens a window with them; ok is false for
	// any placement Valid rejects.
	Stored(p *jsonio.Object) (title string, vars []Var, ok bool)
}

// Update applies what a Sync learned to the stored placement, and reports
// whether that changed anything. It leaves a placement that is not the synced
// window's alone.
type Update func(old *jsonio.Object) (*jsonio.Object, bool)

// Syncer asks the terminal for the keys only its sync writes, for
// terminal-sync. Its error says TimedOut() for the deadline passing.
type Syncer interface {
	// Sync asks about the window of p, the placement Recognize returned.
	Sync(p *jsonio.Object) (Update, error)
}

// Launcher opens a window beside the caller's, for spawn and resume.
type Launcher interface {
	// UserVars reports whether the backend can set user variables on the
	// window it launches.
	UserVars() bool
	// Launch opens the window and returns its placement. Its error is a
	// *LaunchError.
	Launch(spec LaunchSpec) (*jsonio.Object, error)
}

// Existence is a backend's answer to whether a window exists.
type Existence int

const (
	// Unknown is no answer, however the question failed: the caller must not
	// take it for a window being gone.
	Unknown Existence = iota
	// Present: the terminal lists the window.
	Present
	// Gone: the terminal answered, and does not list the window.
	Gone
)

// WindowChecker says whether windows exist, for reservations. The backend
// batches its questions as its terminal allows.
type WindowChecker interface {
	// Exist answers for each of ps, valid placements of this backend, in
	// order.
	Exist(ps []*jsonio.Object) []Existence
}

// Locator finds the window running a pid, as "Finding a session's window"
// says (operations.md). stored is the session's placement; the caller's
// environment may name another place to look. The placement returned
// addresses the window found, which is verified; the error says, for each
// place asked, why it did not answer.
type Locator interface {
	Locate(stored *jsonio.Object, pid int64, getenv func(string) string) (*jsonio.Object, error)
}

// Sender pastes text into a window and, if submit, presses Enter. Its error
// is a *SendError.
type Sender interface {
	Send(p *jsonio.Object, text string, submit bool) error
}

// Focuser brings a window to the front.
type Focuser interface {
	Focus(p *jsonio.Object) error
}

// Detect finds the backend whose terminal the environment names, asking those
// in order, and the placement it recognized; none for no placement. tmux and
// screen are ruled out first. The nested rule is record's, which knows
// whether the session is nested.
func Detect(backends []Backend, getenv func(string) string) (Backend, *jsonio.Object) {
	if Multiplexed(getenv) {
		return nil, nil
	}
	for _, b := range backends {
		if p := b.Recognize(getenv); p != nil {
			return b, p
		}
	}
	return nil, nil
}

// TagOf is a placement's terminal tag, or "" for none.
func TagOf(p *jsonio.Object) string {
	if p == nil {
		return ""
	}
	v, _ := p.Get("terminal")
	tag, _ := v.(string)
	return tag
}

// For finds the backend a tag names, or nil for a tag no backend has.
func For(backends []Backend, tag string) Backend {
	for _, b := range backends {
		if b.Tag() == tag {
			return b
		}
	}
	return nil
}

// Of finds the backend a stored placement's tag names, or nil.
func Of(backends []Backend, p *jsonio.Object) Backend {
	if tag := TagOf(p); tag != "" {
		return For(backends, tag)
	}
	return nil
}
