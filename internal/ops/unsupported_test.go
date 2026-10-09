package ops

import (
	"os"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// bare is a backend with none of the optional abilities: kitty's placements,
// recognized, replaced, and validated as kitty's, and nothing it can do.
type bare struct{}

func (bare) Tag() string                                         { return kitty.Tag }
func (bare) Recognize(getenv func(string) string) *jsonio.Object { return kitty.Recognize(getenv) }
func (bare) Replace(next, old *jsonio.Object, resumed bool) *jsonio.Object {
	return kitty.Replace(next, old, resumed)
}
func (bare) Valid(p *jsonio.Object) bool             { return kitty.Backend{}.Valid(p) }
func (bare) Address(p *jsonio.Object) *jsonio.Object { return kitty.Backend{}.Address(p) }
func (bare) Stored(p *jsonio.Object) (string, []placement.Var, bool) {
	return kitty.Stored(p)
}

// launching adds launching, with or without user variables, and records it.
type launching struct {
	bare
	userVars bool
	launched *int
}

func (l launching) UserVars() bool { return l.userVars }
func (l launching) Launch(placement.LaunchSpec) (*jsonio.Object, error) {
	*l.launched++
	return kitty.PlacementOf("unix:/kitty", 9), nil
}

// locating adds finding a window by pid only; pasting adds pasting only.
type locating struct{ bare }

func (locating) Locate(*jsonio.Object, int64, func(string) string) (*jsonio.Object, error) {
	return kitty.PlacementOf("unix:/old", 21), nil
}

type pasting struct{ bare }

func (pasting) Send(*jsonio.Object, string, bool) error { return nil }

// wantUnsupported checks env is terminal unsupported from kitty, naming the
// ability in its detail.
func wantUnsupported(t *testing.T, env Envelope, ability string) {
	t.Helper()
	wantReason(t, env, "unsupported")
	if d, _ := env.Error.Details["detail"].(string); !strings.Contains(d, ability) {
		t.Errorf("detail %q does not name %q", d, ability)
	}
}

// noReservations checks nothing was reserved: reservations/ was never made.
func noReservations(t *testing.T, f *spawnFixture) {
	t.Helper()
	if _, err := os.Stat(f.loc.ReservationsDir()); !os.IsNotExist(err) {
		t.Errorf("reservations/: %v", err)
	}
}

// A backend that can't launch refuses spawn and resume with nothing reserved
// or launched (operations.md, spawn Errors).
func TestSpawnUnsupportedLaunch(t *testing.T) {
	f := newSpawnFixture(t)
	f.only = bare{}
	wantUnsupported(t, f.spawn(`"job":"api"`), "launch a window")
	noReservations(t, f)

	f.endedSession(uuidA, 1, "", "")
	wantUnsupported(t, f.resume("1"), "launch a window")
}

// vars on a backend without user variables are refused before anything is
// reserved; without vars the same backend launches.
func TestSpawnUnsupportedVars(t *testing.T) {
	f := newSpawnFixture(t)
	launched := 0
	f.only = launching{userVars: false, launched: &launched}
	wantUnsupported(t, f.spawn(`"job":"api"`, `"vars":{"p":"1"}`), "user variables")
	noReservations(t, f)
	if launched != 0 {
		t.Errorf("launches %d", launched)
	}
	if env := f.spawn(`"job":"api"`, `"start_timeout_secs":0`); !env.OK || launched != 1 {
		t.Errorf("%+v, launches %d", env.Error, launched)
	}
	f.only = launching{userVars: true, launched: &launched}
	if env := f.spawn(`"vars":{"p":"1"}`, `"start_timeout_secs":0`); !env.OK || launched != 2 {
		t.Errorf("%+v, launches %d", env.Error, launched)
	}
}

// The Errors table's order: unavailable, then unsupported, then the rest.
func TestSpawnUnsupportedOrder(t *testing.T) {
	f := newSpawnFixture(t)
	f.only = bare{}
	f.vars["KITTY_LISTEN_ON"] = ""
	env := f.spawn(`"vars":{"p":"1"}`)
	wantKind(t, env, KindTerminal)
	if env.Error.Details["reason"] != "unavailable" {
		t.Errorf("details %+v", env.Error.Details)
	}
}

// send needs a backend that finds a window by pid and one that pastes; either
// missing refuses with nothing typed, whatever the session's pid.
func TestSendUnsupported(t *testing.T) {
	for name, tc := range map[string]struct {
		b       placement.Backend
		ability string
	}{
		"neither":  {bare{}, "find a window by pid"},
		"no paste": {locating{}, "paste text"},
		"no find":  {pasting{}, "find a window by pid"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSendFixture(t)
			f.only = tc.b
			f.live(uuidA, 1, "", sendPlacement)
			wantUnsupported(t, f.send("1"), tc.ability)
			// Before the pid is looked at.
			f.live(uuidB, 2, "", sendPlacement, func(l *model.LifecycleFile) { l.PID, l.PIDStartedAt = nil, nil })
			wantUnsupported(t, f.send("2"), tc.ability)
			if len(f.sends) != 0 || len(f.finds) != 0 {
				t.Errorf("sends %v finds %v", f.sends, f.finds)
			}
		})
	}
}

// focus needs a backend that focuses; finding the window is optional, and
// without it the stored window is the one.
func TestFocusUnsupported(t *testing.T) {
	f := newFocusFixture(t)
	f.only = bare{}
	f.live(uuidA, 1, "", sendPlacement)
	wantUnsupported(t, f.focus("1"), "focus a window")
	if len(f.focuses) != 0 {
		t.Errorf("focuses %v", f.focuses)
	}
}

// A placement of a tag no backend has stays no-placement, whatever the
// abilities.
func TestUnknownTagNoPlacement(t *testing.T) {
	f := newSendFixture(t)
	f.only = bare{}
	f.live(uuidA, 1, "", `{"terminal":"elsewhere","socket":"unix:/old","window_id":4}`)
	wantRule(t, f.send("1"), "no-placement", uuidA)
}
