package ops

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/kitty"
	"github.com/phansen314/sesshin/internal/placement/placementtest"
)

// bare is a backend with none of the optional abilities: kitty's placements,
// recognized, replaced, and validated as kitty's, and nothing it can do. kit
// is the fixture's backend, whose recording fakes the types below hand their
// one ability to, so a test sees every call that reached the terminal.
type bare struct{ kit placementtest.Kitty }

func (bare) Tag() string                                         { return kitty.Tag }
func (bare) Variables() []string                                 { return kitty.Backend{}.Variables() }
func (bare) Recognize(getenv func(string) string) *jsonio.Object { return kitty.Recognize(getenv) }
func (bare) Replace(next, old *jsonio.Object, resumed bool) *jsonio.Object {
	return kitty.Replace(next, old, resumed)
}
func (bare) Valid(p *jsonio.Object) bool             { return kitty.Backend{}.Valid(p) }
func (bare) Address(p *jsonio.Object) *jsonio.Object { return kitty.Backend{}.Address(p) }
func (bare) Stored(p *jsonio.Object) (string, []placement.Var, bool) {
	return kitty.Stored(p)
}

// launching adds launching, with or without user variables, and nothing
// else: no window checks.
type launching struct {
	bare
	userVars bool
}

func (l launching) UserVars() bool { return l.userVars }
func (l launching) Launch(spec placement.LaunchSpec) (*jsonio.Object, error) {
	return l.kit.Launch(spec)
}

// locating adds finding a window by pid only; pasting adds pasting only;
// focusing adds focusing only.
type locating struct{ bare }

func (l locating) Locate(stored *jsonio.Object, pid int64, getenv func(string) string) (*jsonio.Object, error) {
	return l.kit.Locate(stored, pid, getenv)
}

type pasting struct{ bare }

func (p pasting) Send(w *jsonio.Object, text string, submit bool) error {
	return p.kit.Send(w, text, submit)
}

type focusing struct{ bare }

func (f focusing) Focus(w *jsonio.Object) error { return f.kit.Focus(w) }

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

// A backend that can't launch refuses spawn with nothing reserved or
// launched (operations.md, spawn Errors).
func TestSpawnUnsupportedLaunch(t *testing.T) {
	f := newSpawnFixture(t)
	f.only = bare{f.kit}
	wantUnsupported(t, f.spawn(`"job":"api"`), "launch a window")
	noReservations(t, f)
	if len(f.launches) != 0 {
		t.Errorf("launches %+v", f.launches)
	}
}

// resume under a job is refused the same way, with nothing reserved; the
// same resume with kitty reserves, so the check can see one.
func TestResumeUnsupportedLaunch(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "api", "")
	f.only = bare{f.kit}
	wantUnsupported(t, f.resume("1"), "launch a window")
	noReservations(t, f)
	if len(f.launches) != 0 {
		t.Errorf("launches %+v", f.launches)
	}

	f.only = nil
	if out, _ := f.resumed("1", `"start_timeout_secs":0`); out.Job == nil || *out.Job != "api" || len(f.launches) != 1 {
		t.Errorf("%+v, launches %d", out, len(f.launches))
	}
	if _, ok := f.reservationOf("api", token(1)); !ok {
		t.Error("kitty's resume reserved nothing")
	}
}

// vars on a backend without user variables are refused before anything is
// reserved; without vars the same backend launches.
func TestSpawnUnsupportedVars(t *testing.T) {
	f := newSpawnFixture(t)
	f.only = launching{bare{f.kit}, false}
	wantUnsupported(t, f.spawn(`"job":"api"`, `"vars":{"p":"1"}`), "user variables")
	noReservations(t, f)
	if len(f.launches) != 0 {
		t.Errorf("launches %+v", f.launches)
	}
	if env := f.spawn(`"job":"api"`, `"start_timeout_secs":0`); !env.OK || len(f.launches) != 1 {
		t.Errorf("%+v, launches %d", env.Error, len(f.launches))
	}
	f.only = launching{bare{f.kit}, true}
	if env := f.spawn(`"vars":{"p":"1"}`, `"start_timeout_secs":0`); !env.OK || len(f.launches) != 2 {
		t.Errorf("%+v, launches %d", env.Error, len(f.launches))
	}
	if want := []placement.Var{{Name: "p", Value: "1"}}; !slices.Equal(f.launches[1].Vars, want) {
		t.Errorf("vars %+v", f.launches[1].Vars)
	}
}

// resume on a backend without user variables drops the stored ones, keeping
// the title: they describe the old window, and no one asked for them.
func TestResumeDropsStoredVars(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "", syncedPlacement)
	f.only = launching{bare{f.kit}, false}
	f.resumed("1", `"start_timeout_secs":0`)
	if got := f.launches[0]; got.Title != "api review" || len(got.Vars) != 0 {
		t.Errorf("title %q, vars %+v", got.Title, got.Vars)
	}
}

// The Errors tables' order around unsupported: not-found and unavailable
// before it; busy, job-taken, and other-format after it.
func TestUnsupportedOrder(t *testing.T) {
	t.Run("spawn: unavailable first", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.only = bare{f.kit}
		f.vars["KITTY_LISTEN_ON"] = ""
		env := f.spawn(`"vars":{"p":"1"}`)
		wantKind(t, env, KindTerminal)
		if env.Error.Details["reason"] != "unavailable" {
			t.Errorf("details %+v", env.Error.Details)
		}
	})
	t.Run("spawn: not-found first", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.only = bare{f.kit}
		f.cwd = filepath.Join(f.cwd, "missing")
		wantKind(t, f.spawn(`"job":"api"`), KindNotFound)
	})
	t.Run("spawn: before job-taken", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.running(uuidA, time.Minute, 11)
		f.sesshinWith(uuidA, 1, "api", "spawn")
		f.only = bare{f.kit}
		wantUnsupported(t, f.spawn(`"job":"api"`), "launch a window")
		f.only = launching{bare{f.kit}, false}
		wantUnsupported(t, f.spawn(`"job":"api"`, `"vars":{"p":"1"}`), "user variables")
	})
	t.Run("spawn: before busy", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.only = bare{f.kit}
		f.session(uuidA, time.Hour) // sessions/ exists, to be locked
		root, err := fsys.OS{}.OpenRoot(f.loc.SessionsDir())
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		lock, err := root.Lock(0)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Unlock()
		wantUnsupported(t, f.spawn(`"job":"api"`), "launch a window")
	})
	t.Run("resume: before other-format", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.endedSession(uuidA, 1, "", "")
		f.write(uuidA, "sesshin.json", []byte(`{"schema":3,"id":1}`))
		f.only = bare{f.kit}
		wantUnsupported(t, f.resume(uuidA, `"job":"api"`), "launch a window")
		noReservations(t, f)
	})
}

// The pickers' preflight raises unsupported as resume does, and nothing for
// kitty.
func TestCheckTerminalUnsupported(t *testing.T) {
	f := newSpawnFixture(t)
	if e := CheckTerminal(f.spawnEnv()); e != nil {
		t.Fatalf("kitty: %+v", e)
	}
	f.only = bare{f.kit}
	e := CheckTerminal(f.spawnEnv())
	if e == nil || e.Kind != KindTerminal || e.Details["reason"] != "unsupported" || e.Details["terminal"] != "kitty" {
		t.Errorf("%+v", e)
	}
}

// send needs a backend that finds a window by pid and one that pastes; either
// missing refuses with nothing asked and nothing typed, whatever the
// session's pid.
func TestSendUnsupported(t *testing.T) {
	for name, tc := range map[string]struct {
		b       func(f *sendFixture) placement.Backend
		ability string
	}{
		"neither":  {func(f *sendFixture) placement.Backend { return bare{f.kit} }, "find a window by pid"},
		"no paste": {func(f *sendFixture) placement.Backend { return locating{bare{f.kit}} }, "paste text"},
		"no find":  {func(f *sendFixture) placement.Backend { return pasting{bare{f.kit}} }, "find a window by pid"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSendFixture(t)
			f.only = tc.b(f)
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

// focus needs a backend that focuses; one that can only find the window is
// refused with nothing asked.
func TestFocusUnsupported(t *testing.T) {
	f := newFocusFixture(t)
	f.only = locating{bare{f.kit}}
	f.live(uuidA, 1, "", sendPlacement)
	wantUnsupported(t, f.focus("1"), "focus a window")
	if len(f.focuses) != 0 || len(f.finds) != 0 {
		t.Errorf("focuses %v finds %v", f.focuses, f.finds)
	}
}

// Finding the window is optional for focus: without it the stored window is
// focused, unverified, and the output is its address.
func TestFocusWithoutLocator(t *testing.T) {
	f := newFocusFixture(t)
	f.only = focusing{bare{f.kit}}
	f.live(uuidA, 1, "", `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":"t"}`)
	out := f.focused("1")
	if out.Verified || enc(t, out.Placement) != sendPlacement {
		t.Errorf("verified %v, placement %s", out.Verified, enc(t, out.Placement))
	}
	if want := []focusCall{{"unix:/old", 4}}; !slices.Equal(f.focuses, want) || len(f.finds) != 0 {
		t.Errorf("focuses %v finds %v", f.focuses, f.finds)
	}
}

// A backend that can't say whether a window exists leaves reservations to
// their age: a launched one whose window kitty would call gone still holds
// its job, and prune keeps it (design-spec.md, Terminal backends).
func TestReservationsWithoutWindowChecker(t *testing.T) {
	gone := func(*jsonio.Object) ([]int64, bool) { return []int64{1, 2}, true }

	t.Run("spawn", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.windows = gone
		f.reserveToken("api", tokenB, time.Hour, kittyAt("unix:/s", 9))
		f.only = launching{bare{f.kit}, true}
		env := f.spawn(`"job":"api"`, `"start_timeout_secs":0`)
		wantKind(t, env, KindConflict)
		if env.Error.Details["rule"] != "job-taken" {
			t.Errorf("details %+v", env.Error.Details)
		}
		if len(f.launches) != 0 {
			t.Errorf("launches %+v", f.launches)
		}
		f.only = nil
		if env := f.spawn(`"job":"api"`, `"start_timeout_secs":0`); !env.OK {
			t.Errorf("kitty: %+v", env.Error)
		}
	})

	t.Run("prune", func(t *testing.T) {
		f := newPruneFixture(t)
		f.session(pidA, 0)
		f.windows = gone
		f.reserve("api", time.Hour, kittyAt("unix:/s", 9))
		f.only = bare{f.kit}
		if out, _ := f.output(PruneInput{}); len(out.ReservationsRemoved) != 0 || !f.reserved("api") {
			t.Errorf("removed %v", removed(out))
		}
		f.only = nil
		if out, _ := f.output(PruneInput{}); !slices.Equal(removed(out), []string{"api:window-gone"}) {
			t.Errorf("kitty removed %v", removed(out))
		}
	})
}

// A placement of a tag no backend has stays no-placement, whatever the
// abilities, and the message says which of the three it is: none, a terminal
// with no backend, or one its backend rejects.
func TestNoPlacementMessages(t *testing.T) {
	for _, tc := range []struct{ name, placement, want string }{
		{"none", "", ": it has no placement"},
		{"unknown terminal", `{"terminal":"elsewhere","socket":"unix:/old","window_id":4}`, `: its placement names the terminal "elsewhere", which this sesshin has no backend for`},
		{"invalid kitty", `{"terminal":"kitty","window_id":3}`, ": its kitty placement is not valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFocusFixture(t)
			f.only = bare{f.kit}
			f.live(uuidA, 1, "", tc.placement)
			for op, env := range map[string]Envelope{"send": f.send("1"), "focus": f.focus("1")} {
				wantRule(t, env, "no-placement", uuidA)
				if !strings.HasSuffix(env.Error.Message, tc.want) {
					t.Errorf("%s: message %q, want suffix %q", op, env.Error.Message, tc.want)
				}
			}
		})
	}
}

// other is a second terminal's backend: it recognizes nothing, and its
// placements store a title and a variable of their own.
type other struct{}

func (other) Tag() string                                  { return "other" }
func (other) Recognize(func(string) string) *jsonio.Object { return nil }
func (other) Variables() []string                          { return nil }
func (other) Replace(next, _ *jsonio.Object, _ bool) *jsonio.Object {
	return next
}
func (other) Valid(p *jsonio.Object) bool             { return placement.TagOf(p) == "other" }
func (other) Address(p *jsonio.Object) *jsonio.Object { return p }
func (o other) Stored(p *jsonio.Object) (string, []placement.Var, bool) {
	if !o.Valid(p) {
		return "", nil, false
	}
	return "from other", []placement.Var{{Name: "o", Value: "1"}}, true
}

// resume takes the title and variables from the backend the stored
// placement's tag names, and launches through the caller's (design-spec.md,
// Terminal backends): a session last in another terminal reopens in kitty
// under that terminal's title.
func TestResumeStoredByPlacementsBackend(t *testing.T) {
	f := newSpawnFixture(t)
	f.also = []placement.Backend{other{}}
	f.endedSession(uuidA, 1, "", `{"terminal":"other","id":"abc"}`)
	f.resumed("1", `"start_timeout_secs":0`)
	got := f.launches[0]
	if got.Title != "from other" || !slices.Equal(got.Vars, []placement.Var{{Name: "o", Value: "1"}}) || enc(t, got.Caller) != kittyAt("unix:/kitty", 3) {
		t.Errorf("title %q, vars %+v, caller %s", got.Title, got.Vars, enc(t, got.Caller))
	}
}

// nowhere has every ability send and focus use, but its Locate breaks the
// Locator contract with no window and no error.
type nowhere struct{ bare }

func (nowhere) Locate(*jsonio.Object, int64, func(string) string) (*jsonio.Object, error) {
	return nil, nil
}
func (n nowhere) Send(w *jsonio.Object, text string, submit bool) error {
	return n.kit.Send(w, text, submit)
}
func (n nowhere) Focus(w *jsonio.Object) error { return n.kit.Focus(w) }

// A Locate that finds nothing without saying why is not a window found: send
// is unreachable and types nothing, and focus falls back to the stored
// window, unverified.
func TestLocateFindsNothing(t *testing.T) {
	sf := newSendFixture(t)
	sf.only = nowhere{bare{sf.kit}}
	sf.live(uuidA, 1, "", sendPlacement)
	wantReason(t, sf.send("1"), "unreachable")
	if len(sf.sends) != 0 {
		t.Errorf("sends %v", sf.sends)
	}

	ff := newFocusFixture(t)
	ff.only = nowhere{bare{ff.kit}}
	ff.live(uuidA, 1, "", `{"terminal":"kitty","socket":"unix:/old","window_id":4}`)
	out := ff.focused("1")
	if out.Verified {
		t.Error("verified with no window found")
	}
	if want := []focusCall{{"unix:/old", 4}}; !slices.Equal(ff.focuses, want) {
		t.Errorf("focuses %v", ff.focuses)
	}
}

// The unavailable message names what each backend needs of the caller's
// environment.
func TestUnavailableHints(t *testing.T) {
	if got, want := hints([]placement.Backend{kitty.Backend{}, bare{}}), " (for kitty, remote control on, with KITTY_LISTEN_ON and KITTY_WINDOW_ID set)"; got != want {
		t.Errorf("hints %q, want %q", got, want)
	}
	if got := hints([]placement.Backend{bare{}}); got != "" {
		t.Errorf("hints %q", got)
	}
}

// voidLaunch launches nothing and says so with no error.
type voidLaunch struct{ launching }

func (voidLaunch) Launch(placement.LaunchSpec) (*jsonio.Object, error) { return nil, nil }

// A Launch with neither a window nor an error is launch-unknown, never a
// success with no placement.
func TestLaunchReturnsNothing(t *testing.T) {
	f := newSpawnFixture(t)
	f.only = voidLaunch{launching{bare{f.kit}, true}}
	wantReason(t, f.spawn(`"job":"api"`, `"start_timeout_secs":0`), "launch-unknown")
}

// miscounting answers Exist with a fixed list, whatever it was asked.
type miscounting struct {
	bare
	answers []placement.Existence
}

func (m miscounting) Exist([]*jsonio.Object) []placement.Existence { return m.answers }

// Exist answering too few is unknown for the rest, and too many is no panic.
func TestExistMiscounts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers []placement.Existence
		removed []string
	}{
		{"few", nil, nil},
		{"many", []placement.Existence{placement.Gone, placement.Gone, placement.Gone}, []string{"api:window-gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPruneFixture(t)
			f.session(pidA, 0)
			f.reserve("api", time.Hour, kittyAt("unix:/s", 9))
			f.only = miscounting{bare{f.kit}, tc.answers}
			out, _ := f.output(PruneInput{})
			if !slices.Equal(removed(out), tc.removed) {
				t.Errorf("removed %v, want %v", removed(out), tc.removed)
			}
		})
	}
}
