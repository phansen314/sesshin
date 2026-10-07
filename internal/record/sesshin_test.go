package record

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
)

const (
	idB = "11111111-1111-4111-8111-111111111111"
	idC = "22222222-2222-4222-8222-222222222222"
	idD = "33333333-3333-4333-8333-333333333333"
)

// Design-spec, Sesshin IDs: a first run starts at 1, IDs are issued in order, a
// resume keeps its ID, and a pruned session's ID is not reused.
func TestIssue(t *testing.T) {
	f := newFix(t)
	if f.lastID() != -1 {
		t.Fatal("state.json exists before the first session")
	}
	f.rec(Event{Kind: SessionStart, Source: "startup"})
	if f.sesshinID(sid) != 1 || f.lastID() != 1 {
		t.Errorf("first session: id %d, last_id %d", f.sesshinID(sid), f.lastID())
	}
	if got := f.migration(); got != model.LatestMigration {
		t.Errorf("first run: migration %d, want %d", got, model.LatestMigration)
	}
	// Every later write carries the migration through.
	f.write(f.path("state.json"), `{"schema": 2, "last_id": 1, "migration": 7}`)
	for i, id := range []string{idB, idC} {
		if err := recordWith(f.as(id), Event{Kind: SessionStart, Source: "startup"}); err != nil {
			t.Fatal(err)
		}
		if got := f.sesshinID(id); got != int64(i+2) {
			t.Errorf("session %d: id %d", i+2, got)
		}
	}
	if got := f.migration(); got != 7 {
		t.Errorf("migration %d, want 7 carried through", got)
	}
	// A resume, and every other event, keep the ID and issue none.
	f.rec(Event{Kind: SessionStart, Source: "resume"})
	f.rec(Event{Kind: PostToolUse})
	if f.sesshinID(sid) != 1 || f.lastID() != 3 {
		t.Errorf("after a resume: id %d, last_id %d", f.sesshinID(sid), f.lastID())
	}
	// A /clear is a new session, with a new ID; a pruned session's isn't reused.
	if err := os.RemoveAll(f.sessionPath(idC)); err != nil {
		t.Fatal(err)
	}
	if err := recordWith(f.as(idD), Event{Kind: SessionStart, Source: "clear"}); err != nil {
		t.Fatal(err)
	}
	if got := f.sesshinID(idD); got != 4 || f.lastID() != 4 {
		t.Errorf("after a prune: id %d, last_id %d; want 4", got, f.lastID())
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}
}

// Design-spec, Sesshin IDs: a state.json that is missing while other sessions
// exist is rebuilt from the highest id in any sesshin.json, and logged. Hidden
// leftovers and sessions with no sesshin.json don't count for or against.
func TestRebuild(t *testing.T) {
	for _, tc := range []struct {
		name    string
		damage  func(f *fix)
		logged  string
		wantID  int64
		wantLog string
	}{
		{"missing", func(f *fix) { os.Remove(f.path("state.json")) }, "", 4, "last_id rebuilt from 3"},
		{"unusable", func(f *fix) { f.write(f.path("state.json"), "{") }, "state.json unusable", 4, "last_id rebuilt from 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFix(t)
			for _, id := range []string{sid, idB, idC} {
				if err := recordWith(f.as(id), Event{Kind: PostToolUse}); err != nil {
					t.Fatal(err)
				}
			}
			f.write(f.path("sessions", ".sesshin-tmp-leftover", "sesshin.json"), "{}")
			f.write(f.path("sessions", "5a5a5a5a-5a5a-4a5a-8a5a-5a5a5a5a5a5a", "other.txt"), "no sesshin.json")
			tc.damage(f)
			if err := recordWith(f.as(idD), Event{Kind: PostToolUse}); err != nil {
				t.Fatal(err)
			}
			if got := f.migration(); got != 0 {
				t.Errorf("migration %d, want 0 on a rebuild", got)
			}
			if got := f.sesshinID(idD); got != tc.wantID || f.lastID() != tc.wantID {
				t.Errorf("id %d, last_id %d; want %d", got, f.lastID(), tc.wantID)
			}
			log := f.logged()
			if !strings.Contains(log, tc.wantLog) || !strings.Contains(log, tc.logged) {
				t.Errorf("log %q, want %q and %q", log, tc.wantLog, tc.logged)
			}
		})
	}
}

// With state.json lost, a sessions/ that can't be listed issues no ID: a
// first run can't be told from a loss, and issuing 1 could duplicate an ID in
// use. sesshin.json is written with a null id for the next hook to complete.
func TestRebuildUnlisted(t *testing.T) {
	f := newFix(t)
	for _, id := range []string{sid, idB} {
		if err := recordWith(f.as(id), Event{Kind: PostToolUse}); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(f.path("state.json"))
	env := f.as(idD)
	env.FS = fsys.Fault{FS: env.FS, Hook: fsys.ErrnoAt(fsys.OpReadDir, ".", 1, syscall.EIO)}
	if err := recordWith(env, Event{Kind: PostToolUse}); err == nil {
		t.Error("no error")
	}
	if got := f.sesshinID(idD); got != 0 {
		t.Errorf("id %d, want none", got)
	}
	if got := f.lastID(); got != -1 {
		t.Errorf("last_id %d, want state.json left missing", got)
	}
	if log := f.logged(); !strings.Contains(log, "list sessions") || !strings.Contains(log, "written without an id") {
		t.Errorf("log %q", log)
	}
}

// Design-spec, Sesshin IDs: a state.json that can't be read may hold a last_id
// this hook can't see, so it issues no ID: sesshin.json is written with a null
// id, as when the state lock's wait runs out, and state.json is left alone.
// Only a missing or unusable one is rebuilt; the next hook that can read it
// issues from it.
func TestUnreadableState(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: PostToolUse}) // ID 1, and the sessions/ that makes the rebuild possible
	f.write(f.path("state.json"), `{"schema": 2, "last_id": 10, "migration": 1}`)
	env := f.as(idB)
	env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, "state.json", 1, syscall.EIO)}
	err := recordWith(env, Event{Kind: PostToolUse})
	if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.EIO {
		t.Errorf("err %v", err)
	}
	if got := f.sesshinID(idB); got != 0 {
		t.Errorf("id %d, want none", got)
	}
	if got := f.lastID(); got != 10 {
		t.Errorf("last_id %d, want state.json left as it was", got)
	}
	log := f.logged()
	if !strings.Contains(log, "read state.json: input/output error") || !strings.Contains(log, "sesshin.json written without an id") || strings.Contains(log, "rebuilt") {
		t.Errorf("log %q", log)
	}
	if err := recordWith(f.as(idB), Event{Kind: PostToolUse}); err != nil {
		t.Fatal(err)
	}
	if got := f.sesshinID(idB); got != 11 || f.lastID() != 11 {
		t.Errorf("id %d, last_id %d; want 11, from the last_id that was there", got, f.lastID())
	}
}

// A pending id logs "written without an id" only when sesshin.json was written;
// otherwise it says it wasn't. "last_id rebuilt" is logged once state.json is
// written, not before.
func TestPendingLogsWhatHappened(t *testing.T) {
	f := newFix(t)
	for _, id := range []string{sid, idB} {
		if err := recordWith(f.as(id), Event{Kind: PostToolUse}); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(f.path("state.json")) // so idC rebuilds
	f.logs = nil

	// state.json can't be written: no ID, and no "rebuilt".
	env := f.as(idC)
	env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpCreateTemp && op.Root == f.path() {
			return syscall.ENOSPC
		}
		return nil
	}}
	if err := recordWith(env, Event{Kind: PostToolUse}); err == nil {
		t.Error("no error")
	}
	if log := f.logged(); strings.Contains(log, "rebuilt") || !strings.Contains(log, "sesshin.json written without an id") {
		t.Errorf("log %q", log)
	}

	// Nor can sesshin.json, after lifecycle.json was: it says it wasn't written.
	f.logs = nil
	temps := 0
	env = f.as(idD)
	env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpCreateTemp {
			if temps++; temps > 1 {
				return syscall.ENOSPC
			}
		}
		return nil
	}}
	if err := recordWith(env, Event{Kind: PostToolUse}); err == nil {
		t.Error("no error")
	}
	if log := f.logged(); strings.Contains(log, "without an id") || !strings.Contains(log, "write sesshin.json: no space left on device") || !strings.Contains(log, "sesshin.json not written") {
		t.Errorf("log %q", log)
	}
}

// A sesshin.json that exists but can't be read (here, a directory in its place)
// issues no ID: a replacement would fail too, after state.json had taken one,
// so every hook would burn an ID. lifecycle.json is still recorded.
func TestUnreadableSesshinIssuesNoID(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: PostToolUse}) // ID 1
	path := f.sessionPath(sid, "sesshin.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := recordWith(f.as(sid), Event{Kind: PostToolUse}); err == nil {
			t.Error("no error")
		}
	}
	if got := f.lastID(); got != 1 {
		t.Errorf("last_id %d, want 1: an ID was issued for a sesshin.json that can't be written", got)
	}
	if got := f.life(sid).EventSeq; got != 4 {
		t.Errorf("event_seq %d, want 4: lifecycle.json still records", got)
	}
	if log := f.logged(); !strings.Contains(log, "read sesshin.json: is a directory") || strings.Contains(log, "state.json") {
		t.Errorf("log %q", log)
	}
}

// A state.json lost with no other session is a first run: ID 1, no rebuild
// logged. An unusable one is still logged.
func TestFirstRunAfterLoss(t *testing.T) {
	f := newFix(t)
	f.write(f.path("state.json"), "[]")
	f.rec(Event{Kind: PostToolUse})
	if f.sesshinID(sid) != 1 || f.lastID() != 1 {
		t.Errorf("id %d, last_id %d", f.sesshinID(sid), f.lastID())
	}
	if got := f.logged(); !strings.Contains(got, "state.json unusable") || strings.Contains(got, "rebuilt") {
		t.Errorf("log %q", got)
	}
}

// Hooks-spec, Recording an event step 5: an unusable sesshin.json is created afresh with a new ID, never the old one, and
// logged.
func TestUnusableSesshin(t *testing.T) {
	for name, content := range map[string]string{
		"not json": "nope",
		"bad id":   `{"schema": 2, "id": 0, "job": null, "source": "hook", "placement": null, "extra": {}}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFix(t)
			f.rec(Event{Kind: PostToolUse})
			f.write(f.sessionPath(sid, "sesshin.json"), content)
			f.rec(Event{Kind: PostToolUse})
			if f.sesshinID(sid) != 2 || f.lastID() != 2 {
				t.Errorf("id %d, last_id %d; want a fresh 2", f.sesshinID(sid), f.lastID())
			}
			if got := f.logged(); !strings.Contains(got, "sesshin.json unusable") {
				t.Errorf("log %q", got)
			}
		})
	}
}

// Hooks-spec, session-start: a session-start that completes a sesshin.json whose
// id is null replaces the placement, as for one that has an ID, and also when
// the ID can't be issued, so a resume in another window isn't left pointing at
// the last one's.
func TestSessionStartCompletingReplacesPlacement(t *testing.T) {
	const pending = `{"schema": 2, "id": null, "job": null, "source": "hook", "placement": {"terminal": "kitty", "socket": "unix:/tmp/kitty-1", "window_id": 1}, "extra": {}}`
	window := func(f *fix) string {
		pl := f.sesshin(sid).Placement
		if pl == nil {
			return ""
		}
		w, _ := pl.Get("window_id")
		return string(w.(json.Number))
	}
	setup := func(t *testing.T) (*fix, *int) {
		f := newFix(t)
		f.write(f.sessionPath(sid, "sesshin.json"), pending)
		place, calls, _ := placed(kitty(2))
		f.env.Placement = place
		return f, calls
	}

	t.Run("the pending file", func(t *testing.T) {
		f, calls := setup(t)
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		if f.sesshinID(sid) != 1 || window(f) != "2" || *calls != 1 {
			t.Errorf("id %d, window %q, %d calls; want 1, the new window's, 1", f.sesshinID(sid), window(f), *calls)
		}
	})
	t.Run("another event keeps it", func(t *testing.T) {
		f, calls := setup(t)
		f.rec(Event{Kind: PostToolUse})
		if f.sesshinID(sid) != 1 || window(f) != "1" || *calls != 0 {
			t.Errorf("id %d, window %q, %d calls; want 1, the old window's, 0", f.sesshinID(sid), window(f), *calls)
		}
	})
	t.Run("under tmux", func(t *testing.T) {
		f, calls := setup(t)
		f.setenv("TMUX", "/tmp/tmux-1000/default,1,0")
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		if f.sesshinID(sid) != 1 || f.sesshin(sid).Placement != nil || *calls != 0 {
			t.Errorf("id %d, placement %v, %d calls; want 1, null, 0", f.sesshinID(sid), f.sesshin(sid).Placement, *calls)
		}
	})
	t.Run("state lock held", func(t *testing.T) {
		f, _ := setup(t)
		f.rec(Event{Kind: PostToolUse}) // creates sessions/ and state.json; sesshin.json gets an ID
		f.write(f.sessionPath(sid, "sesshin.json"), pending)
		os.Remove(f.path("state.json"))
		release := hold(t, f.path("sessions"))
		f.env.LockWait = 20 * time.Millisecond
		if err := Record(f.env, Event{Kind: SessionStart, Source: "resume"}); err == nil {
			t.Error("no error while the state lock is held")
		}
		if h := f.sesshin(sid); h.ID != nil || window(f) != "2" {
			t.Errorf("id %v, window %q; want a null id and the new window's", h.ID, window(f))
		}
		release()
		f.env.Placement = nil
		f.rec(Event{Kind: PostToolUse})
		if f.sesshinID(sid) != 1 || window(f) != "2" {
			t.Errorf("completed: id %d, window %q", f.sesshinID(sid), window(f))
		}
	})
}

// Creating sesshin.json: the placement is the backend's, null when there is
// none; it is not asked for a session started by another session, or under
// tmux or screen. Completing keeps it. Only session-start replaces it, with
// the old placement handed over for the keys only the backend's sync writes.
func TestPlacement(t *testing.T) {
	window := func(h *fix, id string) string {
		pl := h.sesshin(id).Placement
		if pl == nil {
			return ""
		}
		w, _ := pl.Get("window_id")
		return string(w.(json.Number))
	}

	t.Run("none", func(t *testing.T) {
		f := newFix(t)
		f.rec(Event{Kind: SessionStart, Source: "startup"})
		if f.sesshin(sid).Placement != nil {
			t.Error("placement without a backend")
		}
	})

	t.Run("created, kept, replaced", func(t *testing.T) {
		f := newFix(t)
		place, calls, old := placed(kitty(1))
		f.env.Placement = place
		f.rec(Event{Kind: SessionStart, Source: "startup"})
		if window(f, sid) != "1" || *calls != 1 || *old != nil {
			t.Errorf("window %q, %d calls, old %v", window(f, sid), *calls, *old)
		}
		for _, k := range []Kind{PostToolUse, Stop, UserPromptSubmit, SessionEnd} {
			place2, calls2, _ := placed(kitty(9))
			f.env.Placement = place2
			f.rec(Event{Kind: k})
			if window(f, sid) != "1" || *calls2 != 0 {
				t.Errorf("%v: window %q, %d calls; only session-start replaces placement", baseType(k), window(f, sid), *calls2)
			}
		}
		place3, calls3, old3 := placed(kitty(3))
		f.env.Placement = place3
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		if window(f, sid) != "3" || *calls3 != 1 || *old3 == nil {
			t.Errorf("resume: window %q, %d calls, old %v", window(f, sid), *calls3, *old3)
		}
		if f.sesshinID(sid) != 1 || f.lastID() != 1 {
			t.Errorf("a resume issued an ID: %d, %d", f.sesshinID(sid), f.lastID())
		}
	})

	t.Run("replaced by null", func(t *testing.T) {
		f := newFix(t)
		place, _, _ := placed(kitty(1))
		f.env.Placement = place
		f.rec(Event{Kind: SessionStart, Source: "startup"})
		f.setenv("TMUX", "/tmp/tmux-1000/default,1,0")
		place2, calls, _ := placed(kitty(2))
		f.env.Placement = place2
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		if f.sesshin(sid).Placement != nil || *calls != 0 {
			t.Errorf("under tmux: placement %v, %d calls", f.sesshin(sid).Placement, *calls)
		}
	})

	for _, tc := range []struct {
		name string
		set  func(*fix)
	}{
		{"nested", func(f *fix) { f.env.Lookup = nestedClaude }},
		{"tmux", func(f *fix) { f.setenv("TMUX", "x") }},
		{"screen", func(f *fix) { f.setenv("STY", "x") }},
	} {
		t.Run("never placed: "+tc.name, func(t *testing.T) {
			f := newFix(t)
			tc.set(f)
			place, calls, _ := placed(kitty(1))
			f.env.Placement = place
			f.rec(Event{Kind: SessionStart, Source: "startup"})
			if f.sesshin(sid).Placement != nil || *calls != 0 {
				t.Errorf("placement %v, %d calls", f.sesshin(sid).Placement, *calls)
			}
		})
	}

	// nested is read from lifecycle.json, so an async hook creating sesshin.json
	// later, which finds no process, still doesn't place a nested session.
	t.Run("nested from lifecycle.json", func(t *testing.T) {
		f := newFix(t)
		f.env.Lookup = nestedClaude
		f.rec(Event{Kind: PostToolUse})
		os.Remove(f.sessionPath(sid, "sesshin.json"))
		place, calls, _ := placed(kitty(1))
		f.env.Placement = place
		f.rec(Event{Kind: PostToolUse})
		if f.sesshin(sid).Placement != nil || *calls != 0 {
			t.Errorf("placement %v, %d calls", f.sesshin(sid).Placement, *calls)
		}
	})
}

// Hooks-spec, Creating sesshin.json step 3: a new file has job null and source
// hook, whether or not its ID could be issued, and whether it replaces a
// missing or an unusable file.
func TestNewSesshinJobAndSource(t *testing.T) {
	check := func(t *testing.T, f *fix) {
		t.Helper()
		if h := f.sesshin(sid); h.Job != nil || h.Source != "hook" {
			t.Errorf("job %v, source %q; want null, hook", h.Job, h.Source)
		}
	}
	t.Run("issued", func(t *testing.T) {
		f := newFix(t)
		f.rec(Event{Kind: SessionStart, Source: "startup"})
		check(t, f)
	})
	t.Run("replacing an unusable file", func(t *testing.T) {
		f := newFix(t)
		f.rec(Event{Kind: PostToolUse})
		// A file from before job and source existed is unusable now.
		f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 2, "id": 1, "placement": null}`)
		f.rec(Event{Kind: PostToolUse})
		check(t, f)
	})
	t.Run("the ID can't be issued", func(t *testing.T) {
		f := newFix(t)
		f.rec(Event{Kind: PostToolUse})
		f.write(f.sessionPath(sid, "sesshin.json"), "nope")
		release := hold(t, f.path("sessions"))
		defer release()
		f.env.LockWait = 20 * time.Millisecond
		if err := Record(f.env, Event{Kind: PostToolUse}); err == nil {
			t.Error("no error while the state lock is held")
		}
		if h := f.sesshin(sid); h.ID != nil {
			t.Fatalf("id %v, want null", h.ID)
		}
		check(t, f)
	})
}

// Hooks-spec, Creating sesshin.json step 3: a hook completing a file keeps its
// job and source, and so does one replacing the placement of a file that has
// an ID, and one that can't issue an ID (a file whose ID is null keeps them).
func TestSesshinJobAndSourceKept(t *testing.T) {
	const stored = `{"schema": 2, "id": %s, "job": "api-review", "source": "spawn", "placement": {"terminal": "kitty", "socket": "unix:/tmp/kitty-1", "window_id": 1}, "extra": {}}`
	keeps := func(t *testing.T, f *fix) {
		t.Helper()
		h := f.sesshin(sid)
		if h.Job == nil || *h.Job != "api-review" || h.Source != "spawn" {
			t.Errorf("job %v, source %q; want api-review, spawn", h.Job, h.Source)
		}
	}
	t.Run("completing a pending id", func(t *testing.T) {
		f := newFix(t)
		f.write(f.sessionPath(sid, "sesshin.json"), strings.Replace(stored, "%s", "null", 1))
		f.rec(Event{Kind: PostToolUse})
		if f.sesshinID(sid) != 1 {
			t.Errorf("id %d, want 1", f.sesshinID(sid))
		}
		keeps(t, f)
	})
	t.Run("session-start completing a pending id", func(t *testing.T) {
		f := newFix(t)
		f.write(f.sessionPath(sid, "sesshin.json"), strings.Replace(stored, "%s", "null", 1))
		place, _, _ := placed(kitty(2))
		f.env.Placement = place
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		if f.sesshinID(sid) != 1 {
			t.Errorf("id %d, want 1", f.sesshinID(sid))
		}
		keeps(t, f)
	})
	t.Run("session-start replacing the placement", func(t *testing.T) {
		f := newFix(t)
		f.write(f.sessionPath(sid, "sesshin.json"), strings.Replace(stored, "%s", "5", 1))
		place, calls, _ := placed(kitty(2))
		f.env.Placement = place
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		if w, _ := f.sesshin(sid).Placement.Get("window_id"); *calls != 1 || w != json.Number("2") {
			t.Errorf("window %v after %d calls; want 2 after 1", w, *calls)
		}
		if f.sesshinID(sid) != 5 {
			t.Errorf("id %d, want 5", f.sesshinID(sid))
		}
		keeps(t, f)
	})
	t.Run("the ID can't be issued", func(t *testing.T) {
		f := newFix(t)
		f.rec(Event{Kind: PostToolUse}) // creates sessions/
		f.write(f.sessionPath(sid, "sesshin.json"), strings.Replace(stored, "%s", "null", 1))
		release := hold(t, f.path("sessions"))
		defer release()
		f.env.LockWait = 20 * time.Millisecond
		place, _, _ := placed(kitty(2))
		f.env.Placement = place
		if err := Record(f.env, Event{Kind: SessionStart, Source: "resume"}); err == nil {
			t.Error("no error while the state lock is held")
		}
		if h := f.sesshin(sid); h.ID != nil {
			t.Fatalf("id %v, want null", h.ID)
		}
		keeps(t, f)
	})
}
