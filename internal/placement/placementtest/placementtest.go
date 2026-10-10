// Package placementtest is a kitty backend whose calls to the terminal are
// functions a test supplies, for the tests of the packages that reach a
// backend only through placement's interfaces. It is not linked by any
// binary.
package placementtest

import (
	"errors"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// Kitty is the kitty backend with every ability and none of its processes:
// it recognizes, replaces, validates, and orders sockets as kitty does, and
// asks the functions below instead of kitten. A function left nil fails the
// call.
type Kitty struct {
	kitty.Backend
	// LaunchFn opens a window and returns its ID.
	LaunchFn func(placement.LaunchSpec) (int64, error)
	// WindowsFn answers which windows exist on the placement's socket, and
	// false for no answer.
	WindowsFn func(p *jsonio.Object) ([]int64, bool)
	// FindFn is the lookup of one socket, as kitty.WindowForPID.
	FindFn func(socket string, pid int64) (int64, error)
	// SendFn pastes into a window.
	SendFn func(socket string, window int64, text string, submit bool) error
	// FocusFn focuses a window.
	FocusFn func(socket string, window int64) error
}

var errNotFaked = errors.New("not faked")

// Sync is never kitten's: a test that syncs has its own backend.
func (Kitty) Sync(*jsonio.Object) (placement.Update, error) { return nil, errNotFaked }

// Launch calls LaunchFn, and places the window on the caller's socket.
func (k Kitty) Launch(spec placement.LaunchSpec) (*jsonio.Object, error) {
	if k.LaunchFn == nil {
		return nil, errNotFaked
	}
	id, err := k.LaunchFn(spec)
	if err != nil {
		return nil, err
	}
	caller, _ := kitty.Parse(spec.Caller)
	return kitty.PlacementOf(caller.Socket, id), nil
}

// Exist is kitty's, one question per socket, over WindowsFn.
func (k Kitty) Exist(ps []*jsonio.Object) []placement.Existence {
	return kitty.ExistVia(ps, func(w kitty.Parsed) ([]int64, error) {
		if k.WindowsFn == nil {
			return nil, errNotFaked
		}
		ids, ok := k.WindowsFn(kitty.PlacementOf(w.Socket, w.WindowID))
		if !ok {
			return nil, errNotFaked
		}
		return ids, nil
	})
}

// Locate is kitty's order of sockets over FindFn.
func (k Kitty) Locate(stored *jsonio.Object, pid int64, getenv func(string) string) (*jsonio.Object, error) {
	if k.FindFn == nil {
		return nil, errNotFaked
	}
	return kitty.LocateVia(k.FindFn, getenv, stored, pid)
}

// Send calls SendFn.
func (k Kitty) Send(p *jsonio.Object, text string, submit bool) error {
	w, _ := kitty.Parse(p)
	if k.SendFn == nil {
		return errNotFaked
	}
	return k.SendFn(w.Socket, w.WindowID, text, submit)
}

// Focus calls FocusFn.
func (k Kitty) Focus(p *jsonio.Object) error {
	w, _ := kitty.Parse(p)
	if k.FocusFn == nil {
		return errNotFaked
	}
	return k.FocusFn(w.Socket, w.WindowID)
}
