package record

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

func extraText(t *testing.T, f *fix) string {
	t.Helper()
	b, err := jsonio.MarshalLine(f.sesshin(sid).Extra)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

// Hooks-spec, Creating sesshin.json: the reservation a session adopts hands it
// its extra, key order and number text kept, whether the hook creates the file
// or completes its pending id.
func TestExtraAdoptedFromReservation(t *testing.T) {
	const want = `{"z":1,"ticket":"auth-3","r":1.10,"big":1e400,"n":{"a":[-0,"x"]}}`
	t.Run("a fresh write", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.reserveExtra("api", tokA, time.Minute, true, want)
		f.rec(Event{Kind: PostToolUse}) // any hook that can adopt writes it
		if got := extraText(t, f); got != want {
			t.Errorf("extra %s, want %s", got, want)
		}
		f.wantJob(sid, "api", "spawn")
		if got := f.logged(); got != "" {
			t.Errorf("log %q", got)
		}
	})
	t.Run("completing a pending id", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.reserveExtra("api", tokA, time.Minute, true, want)
		f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 2, "id": null, "job": null, "source": "hook", "placement": null, "extra": {}}`)
		f.rec(Event{Kind: PostToolUse})
		if got := extraText(t, f); got != want {
			t.Errorf("extra %s, want %s", got, want)
		}
		f.wantJob(sid, "api", "spawn")
	})
	t.Run("a job-less reservation", func(t *testing.T) {
		f := jobFix(t, "", tokA)
		f.reserveExtra("", tokA, time.Minute, true, want)
		f.rec(start())
		if got := extraText(t, f); got != want {
			t.Errorf("extra %s, want %s", got, want)
		}
	})
	t.Run("the reservation removed", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.reserveExtra("api", tokA, time.Minute, true, want)
		f.rec(start())
		if f.reserved("api", tokA) {
			t.Error("the reservation is still there")
		}
	})
}

// Without a fresh reservation of its own, a file written afresh has {}: no
// reservation, a stale one (which delivers nothing), another token's, a
// nested session, an unusable one, a reservation named before tokens.
func TestExtraEmptyWithoutAdoption(t *testing.T) {
	const extra = `{"ticket":"auth-3"}`
	for _, tc := range []struct {
		name  string
		setup func(f *fix)
		nest  bool
	}{
		{"no reservation", func(f *fix) {}, false},
		{"a stale reservation", func(f *fix) { f.reserveExtra("api", tokA, 3*time.Minute, false, extra) }, false},
		{"another token's", func(f *fix) { f.reserveExtra("api", tokB, time.Minute, true, extra) }, false},
		{"a nested session", func(f *fix) { f.reserveExtra("api", tokA, time.Minute, true, extra) }, true},
		{"an unusable reservation", func(f *fix) {
			f.write(f.path("reservations", model.ReservationName("api", tokA)), `{"schema": 2`)
		}, false},
		{"a reservation named before tokens", func(f *fix) {
			f.write(f.path("reservations", "api.json"), `{"schema": 1, "job": "api", "token": "`+tokA+`", "created_at": "`+string(model.FormatTimestamp(t0))+`", "placement": null}`)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := jobFix(t, "api", tokA)
			tc.setup(f)
			if tc.nest {
				f.env.Lookup = nestedClaude
			}
			f.rec(start())
			if got := extraText(t, f); got != "{}" {
				t.Errorf("extra %s, want {}", got)
			}
		})
	}
}

// A /clear (or /new) after adoption inherits SESSHIN_JOB and SESSHIN_TOKEN, but
// the reservation was removed by the first session: the next starts at {}.
func TestExtraNotInherited(t *testing.T) {
	f := jobFix(t, "api", tokA)
	f.reserveExtra("api", tokA, time.Minute, true, `{"ticket":"auth-3"}`)
	f.rec(start())
	if got := extraText(t, f); got != `{"ticket":"auth-3"}` {
		t.Fatalf("extra %s", got)
	}
	// The next session of the same process, in the same environment.
	e := f.as(idB)
	if err := recordWith(e, Event{Kind: SessionStart, Source: "clear"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := jsonio.MarshalLine(f.sesshin(idB).Extra); string(got) != "{}\n" {
		t.Errorf("the cleared session's extra %s", got)
	}
}

// A pending file completed without a reservation keeps its extra.
func TestExtraKeptOnCompletion(t *testing.T) {
	f := newFix(t)
	f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 2, "id": null, "job": null, "source": "hook", "placement": null, "extra": {"ticket": "auth-3"}}`)
	f.rec(Event{Kind: PostToolUse})
	if f.sesshinID(sid) != 1 {
		t.Fatal("id not issued")
	}
	if got := extraText(t, f); got != `{"ticket":"auth-3"}` {
		t.Errorf("extra %s", got)
	}
}

// Adopting a resumed session's reservation never touches extra, whatever the
// reservation holds.
func TestExtraUntouchedByResumeAdoption(t *testing.T) {
	f := resumeFix(t)
	f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 2, "id": 1, "job": null, "source": "hook", "placement": null, "extra": {"mine": 1}}`)
	f.reserveExtra("api", tokA, time.Minute, true, `{"theirs":2}`)
	f.rec(Event{Kind: SessionStart, Source: "resume"})
	f.wantJob(sid, "api", "hook")
	if got := extraText(t, f); got != `{"mine":1}` {
		t.Errorf("extra %s", got)
	}
}

// Hooks-spec, When the ID can't be issued: a file written without an id has
// extra {}, and the reservation is not taken.
func TestExtraPendingWrite(t *testing.T) {
	f := jobFix(t, "api", tokA)
	f.reserveExtra("api", tokA, time.Minute, true, `{"ticket":"auth-3"}`)
	e := f.env
	e.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, "state.json", 1, syscall.EIO)}
	if err := recordWith(e, start()); err == nil {
		t.Fatal("no error")
	}
	if got := extraText(t, f); got != "{}" {
		t.Errorf("extra %s", got)
	}
	if !f.reserved("api", tokA) {
		t.Error("the reservation was taken without an ID")
	}
}

// No hook reads extra from the environment.
func TestExtraNotFromEnvironment(t *testing.T) {
	f := newFix(t)
	f.setenv("SESSHIN_EXTRA", `{"a":1}`)
	f.rec(start())
	if got := extraText(t, f); got != "{}" {
		t.Errorf("extra %s, want {}", got)
	}
}

// Hooks-spec, Creating sesshin.json: every rewrite of sesshin.json keeps the
// extra as it read it, byte for byte: completing an id, replacing the
// placement, adopting a resumed session's reservation, the terminal sync.
func TestExtraKept(t *testing.T) {
	const extra = `{
    "b": 1.10,
    "a": [
      -0,
      1e400
    ],
    "c": {}
  }`
	file := func(id, job, src string) string {
		return `{"schema": 2, "id": ` + id + `, "job": ` + job + `, "source": "` + src + `", "placement": {"terminal": "kitty", "socket": "unix:/tmp/kitty-1", "window_id": 1}, "extra": ` + extra + `}`
	}
	stored := func(f *fix) string {
		t.Helper()
		b, err := os.ReadFile(f.sessionPath(sid, "sesshin.json"))
		if err != nil {
			t.Fatal(err)
		}
		_, after, _ := strings.Cut(string(b), `"extra": `)
		return strings.TrimSuffix(after, "\n}\n")
	}
	check := func(t *testing.T, f *fix) {
		t.Helper()
		if got := stored(f); got != extra {
			t.Errorf("extra rewritten as\n%s\nwant\n%s", got, extra)
		}
	}
	other := func(f *fix) {}

	t.Run("completing an id", func(t *testing.T) {
		f := newFix(t)
		other(f)
		f.write(f.sessionPath(sid, "sesshin.json"), file("null", "null", "hook"))
		f.rec(Event{Kind: PostToolUse})
		if f.sesshinID(sid) != 1 {
			t.Fatal("id not issued")
		}
		check(t, f)
	})
	t.Run("session-start completing an id", func(t *testing.T) {
		f := newFix(t)
		other(f)
		f.write(f.sessionPath(sid, "sesshin.json"), file("null", "null", "hook"))
		place, _, _ := placed(kitty(2))
		f.env.Placement = place
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		check(t, f)
	})
	t.Run("replacing the placement", func(t *testing.T) {
		f := newFix(t)
		other(f)
		f.write(f.sessionPath(sid, "sesshin.json"), file("5", "null", "hook"))
		place, calls, _ := placed(kitty(2))
		f.env.Placement = place
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		if *calls != 1 {
			t.Fatalf("%d placement calls", *calls)
		}
		check(t, f)
		if f.logged() != "" {
			t.Errorf("log %q", f.logged())
		}
	})
	t.Run("the id can't be issued", func(t *testing.T) {
		f := newFix(t)
		f.rec(Event{Kind: PostToolUse})
		f.write(f.sessionPath(sid, "sesshin.json"), file("null", "null", "hook"))
		release := hold(t, f.path("sessions"))
		defer release()
		f.env.LockWait = 20 * 1000 * 1000
		place, _, _ := placed(kitty(2))
		f.env.Placement = place
		if err := Record(f.env, Event{Kind: SessionStart, Source: "resume"}); err == nil {
			t.Error("no error while the state lock is held")
		}
		check(t, f)
	})
	t.Run("adopting a reservation on resume", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		other(f)
		f.reserveExtra("api", tokA, 0, true, `{"other":true}`)
		f.write(f.sessionPath(sid, "sesshin.json"), file("3", "null", "hook"))
		f.rec(start())
		f.wantJob(sid, "api", "hook")
		check(t, f)
	})
	t.Run("terminal sync", func(t *testing.T) {
		f := newFix(t)
		f.write(f.sessionPath(sid, "sesshin.json"), file("3", "null", "hook"))
		calls := 0
		if err := SetPlacementSync(f.env, set(synced(), &calls)); err != nil || calls != 1 {
			t.Fatalf("%v, %d calls", err, calls)
		}
		check(t, f)
	})
}
