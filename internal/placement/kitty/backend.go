package kitty

import (
	"errors"
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/placement"
)

// Backend is kitty as a placement.Backend (design-spec.md, Terminal backends):
// it has every optional ability. It holds no state; the zero value is it.
type Backend struct{}

var (
	_ placement.Backend       = Backend{}
	_ placement.Syncer        = Backend{}
	_ placement.Launcher      = Backend{}
	_ placement.WindowChecker = Backend{}
	_ placement.Locator       = Backend{}
	_ placement.Sender        = Backend{}
	_ placement.Focuser       = Backend{}
)

// Tag is "kitty".
func (Backend) Tag() string { return Tag }

// Recognize is Recognize.
func (Backend) Recognize(getenv func(string) string) *jsonio.Object { return Recognize(getenv) }

// Replace is Replace.
func (Backend) Replace(next, old *jsonio.Object, resumed bool) *jsonio.Object {
	return Replace(next, old, resumed)
}

// Valid is whether Parse accepts p.
func (Backend) Valid(p *jsonio.Object) bool {
	_, ok := Parse(p)
	return ok
}

// Address is PlacementOf the socket and window of p: kitty's three keys.
func (Backend) Address(p *jsonio.Object) *jsonio.Object {
	w, ok := Parse(p)
	if !ok {
		return nil
	}
	return PlacementOf(w.Socket, w.WindowID)
}

// Stored is Stored.
func (Backend) Stored(p *jsonio.Object) (string, []placement.Var, bool) { return Stored(p) }

// Sync runs Sync for the window p names, and returns what applies it to a
// stored placement of that window (design-spec.md, Placement): any other
// placement, an invalid one included, is left alone, since the session moved
// and the next session-start replaces it.
func (Backend) Sync(p *jsonio.Object) (placement.Update, error) {
	w, ok := Parse(p)
	if !ok {
		return nil, errors.New("not a kitty placement")
	}
	synced, err := Sync(w.Socket, w.WindowID)
	if err != nil {
		return nil, err
	}
	return func(old *jsonio.Object) (*jsonio.Object, bool) {
		if got, ok := Parse(old); !ok || got != w {
			return old, false
		}
		return Apply(old, synced)
	}, nil
}

// UserVars is true: launch takes --var.
func (Backend) UserVars() bool { return true }

// Hint is placement.Hinter: what the environment needs for kitty to place the
// caller.
func (Backend) Hint() string { return "KITTY_LISTEN_ON and KITTY_WINDOW_ID: remote control on" }

// Launch runs Launch, and returns the placement of the window it opened, on
// the caller's socket.
func (Backend) Launch(spec placement.LaunchSpec) (*jsonio.Object, error) {
	id, err := Launch(spec)
	if err != nil {
		return nil, err
	}
	caller, _ := Parse(spec.Caller)
	return PlacementOf(caller.Socket, id), nil
}

// Exist is ExistVia over Windows.
func (Backend) Exist(ps []*jsonio.Object) []placement.Existence {
	return ExistVia(ps, func(w Parsed) ([]int64, error) { return Windows(w.Socket) })
}

// ExistVia answers for each placement of ps, with one question per distinct
// socket, asked of windows with the first placement of the socket (design-spec.md,
// Placement): a window is gone only when its socket answered without it. A
// socket that fails, and a placement that isn't kitty's, are unknown.
func ExistVia(ps []*jsonio.Object, windows func(Parsed) ([]int64, error)) []placement.Existence {
	out := make([]placement.Existence, len(ps))
	answers := map[string]map[int64]bool{}
	for _, p := range ps {
		w, ok := Parse(p)
		if !ok {
			continue
		}
		if _, asked := answers[w.Socket]; asked {
			continue
		}
		answers[w.Socket] = nil
		if ids, err := windows(w); err == nil {
			set := map[int64]bool{}
			for _, id := range ids {
				set[id] = true
			}
			answers[w.Socket] = set
		}
	}
	for i, p := range ps {
		w, ok := Parse(p)
		if !ok || answers[w.Socket] == nil {
			continue
		}
		if answers[w.Socket][w.WindowID] {
			out[i] = placement.Present
		} else {
			out[i] = placement.Gone
		}
	}
	return out
}

// Locate is LocateVia over WindowForPID, for the window p names.
func (Backend) Locate(stored *jsonio.Object, pid int64, getenv func(string) string) (*jsonio.Object, error) {
	return LocateVia(WindowForPID, getenv, stored, pid)
}

// Send is SendText.
func (Backend) Send(p *jsonio.Object, text string, submit bool) error {
	w, ok := Parse(p)
	if !ok {
		return &placement.SendError{Err: errors.New("not a kitty placement")}
	}
	return SendText(w.Socket, w.WindowID, text, submit)
}

// Focus is FocusWindow.
func (Backend) Focus(p *jsonio.Object) error {
	w, ok := Parse(p)
	if !ok {
		return errors.New("not a kitty placement")
	}
	return FocusWindow(w.Socket, w.WindowID)
}

// LocateVia is "Finding a session's window" (operations.md) with find as the
// per-socket lookup: the window on the stored placement's socket whose
// foreground processes include pid, else on the caller's KITTY_LISTEN_ON when
// set and different. A socket that fails just moves on; when none answers,
// the error says why each did not. The placement returned is the window's, on
// the socket that answered.
func LocateVia(find func(socket string, pid int64) (int64, error), getenv func(string) string, stored *jsonio.Object, pid int64) (*jsonio.Object, error) {
	w, ok := Parse(stored)
	if !ok {
		return nil, errors.New("not a kitty placement")
	}
	sockets := []string{w.Socket}
	if own := getenv("KITTY_LISTEN_ON"); own != "" && own != w.Socket {
		sockets = append(sockets, own)
	}
	var tried []string
	for _, s := range sockets {
		id, err := find(s, pid)
		if err != nil {
			tried = append(tried, s+": "+err.Error())
			continue
		}
		return PlacementOf(s, id), nil
	}
	return nil, errors.New(strings.Join(tried, "; "))
}
