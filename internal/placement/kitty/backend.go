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

// Valid is Parse.
func (Backend) Valid(p *jsonio.Object) (placement.Window, bool) { return Parse(p) }

// Place is PlacementOf.
func (Backend) Place(w placement.Window) *jsonio.Object { return PlacementOf(w.Socket, w.WindowID) }

// Stored is Stored.
func (Backend) Stored(p *jsonio.Object) (string, []placement.Var, bool) { return Stored(p) }

// Sync runs Sync for the window, and returns what applies it to a stored
// placement of that window (design-spec.md, Placement): any other placement,
// an invalid one included, is left alone, since the session moved and the
// next session-start replaces it.
func (Backend) Sync(w placement.Window) (placement.Update, error) {
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

// Launch runs Launch, and returns the window it opened on the spec's socket.
func (Backend) Launch(spec placement.LaunchSpec) (placement.Window, error) {
	id, err := Launch(spec)
	if err != nil {
		return placement.Window{}, err
	}
	return placement.Window{Socket: spec.Socket, WindowID: id}, nil
}

// Windows is Windows, on the window's socket.
func (Backend) Windows(w placement.Window) ([]int64, error) { return Windows(w.Socket) }

// Locate is LocateVia over WindowForPID.
func (Backend) Locate(stored placement.Window, pid int64, getenv func(string) string) (placement.Window, error) {
	return LocateVia(WindowForPID, getenv, stored, pid)
}

// Send is SendText.
func (Backend) Send(w placement.Window, text string, submit bool) error {
	return SendText(w.Socket, w.WindowID, text, submit)
}

// Focus is FocusWindow.
func (Backend) Focus(w placement.Window) error { return FocusWindow(w.Socket, w.WindowID) }

// LocateVia is "Finding a session's window" (operations.md) with find as the
// per-socket lookup: the window on the stored socket whose foreground
// processes include pid, else on the caller's KITTY_LISTEN_ON when set and
// different. A socket that fails just moves on; when none answers, the error
// says why each did not.
func LocateVia(find func(socket string, pid int64) (int64, error), getenv func(string) string, stored placement.Window, pid int64) (placement.Window, error) {
	sockets := []string{stored.Socket}
	if own := getenv("KITTY_LISTEN_ON"); own != "" && own != stored.Socket {
		sockets = append(sockets, own)
	}
	var tried []string
	for _, s := range sockets {
		w, err := find(s, pid)
		if err != nil {
			tried = append(tried, s+": "+err.Error())
			continue
		}
		return placement.Window{Socket: s, WindowID: w}, nil
	}
	return placement.Window{}, errors.New(strings.Join(tried, "; "))
}
