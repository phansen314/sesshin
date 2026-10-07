package record

import (
	"os"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

func extraText(t *testing.T, f *fix) string {
	t.Helper()
	b, err := jsonio.MarshalLine(f.sesshin(sid).Extra)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

// Hooks-spec, Creating sesshin.json: a file written afresh takes its extra
// from SESSHIN_EXTRA, its key order and number text kept.
func TestExtraCopiedOnFreshWrite(t *testing.T) {
	f := newFix(t)
	const want = `{"z":1,"koan-task":57,"r":1.10,"big":1e400,"n":{"a":[-0,"x"]}}`
	f.setenv("SESSHIN_EXTRA", want)
	f.rec(Event{Kind: PostToolUse}) // any hook that can adopt writes it
	if got := extraText(t, f); got != want {
		t.Errorf("extra %s, want %s", got, want)
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}
}

// An unset, malformed, non-object, or over-limit SESSHIN_EXTRA is {}, and
// only session-start logs it.
func TestExtraIgnored(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 33) + "1" + strings.Repeat("}", 33) // 33 levels
	big := `{"k":"` + strings.Repeat("x", 65536) + `"}`
	for _, tc := range []struct{ name, value, log string }{
		{"unset", "", ""},
		{"malformed", `{"a":`, "SESSHIN_EXTRA"},
		{"not an object", `[1]`, "SESSHIN_EXTRA"},
		{"repeated key", `{"a":1,"a":2}`, "SESSHIN_EXTRA repeated key; ignored"},
		{"lone surrogate", `{"a":"\ud800"}`, "SESSHIN_EXTRA"},
		{"too deep", deep, "SESSHIN_EXTRA"},
		{"too big", big, "SESSHIN_EXTRA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFix(t)
			f.setenv("SESSHIN_EXTRA", tc.value)
			f.rec(Event{Kind: PostToolUse})
			if got := extraText(t, f); got != "{}" {
				t.Errorf("hook: extra %s, want {}", got)
			}
			if got := f.logged(); got != "" {
				t.Errorf("a hook other than session-start logged %q", got)
			}

			f = newFix(t)
			f.setenv("SESSHIN_EXTRA", tc.value)
			f.rec(Event{Kind: SessionStart, Source: "startup"})
			if got := extraText(t, f); got != "{}" {
				t.Errorf("session-start: extra %s, want {}", got)
			}
			if got := f.logged(); !strings.Contains(got, tc.log) || (tc.log == "" && got != "") || strings.Count(got, "\n") > 1 {
				t.Errorf("session-start log %q, want it to contain %q", got, tc.log)
			}
		})
	}
}

// The limits are inclusive: 32 levels and 65,536 bytes are within them.
func TestExtraAtLimits(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 32) + "1" + strings.Repeat("}", 32) // 32 levels
	pad := 65536 - len(`{"k":""}`)
	exact := `{"k":"` + strings.Repeat("x", pad) + `"}`
	for _, v := range []string{deep, exact} {
		f := newFix(t)
		f.setenv("SESSHIN_EXTRA", v)
		f.rec(Event{Kind: SessionStart, Source: "startup"})
		if got := extraText(t, f); got != v {
			t.Errorf("extra of %d bytes not kept (got %d bytes)", len(v), len(got))
		}
		if f.logged() != "" {
			t.Errorf("log %q", f.logged())
		}
	}
}

// A session started by another session inherited the variable and gets {}.
func TestExtraNested(t *testing.T) {
	f := newFix(t)
	f.setenv("SESSHIN_EXTRA", `{"a":1}`)
	f.env.Lookup = nestedClaude
	f.rec(Event{Kind: SessionStart, Source: "startup"})
	if got := extraText(t, f); got != "{}" {
		t.Errorf("extra %s, want {}", got)
	}
}

// Hooks-spec, Creating sesshin.json: every rewrite of sesshin.json keeps the
// extra as it read it, byte for byte: completing an id, replacing the
// placement, adopting a resumed session's reservation, the terminal sync. A
// different SESSHIN_EXTRA changes nothing once the file exists.
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
	other := func(f *fix) { f.setenv("SESSHIN_EXTRA", `{"other":true}`) }

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
		f.reserve("api", tokA, 0, true)
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
