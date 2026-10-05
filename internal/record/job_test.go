package record

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

const (
	tokA = "3fa85f6457174562b3fc2c963f66afa6"
	tokB = "0123456789abcdef0123456789abcdef"
)

// reserve writes reservations/<job>.json, created age before the fixture's
// clock, with no placement unless placed.
func (f *fix) reserve(job, token string, age time.Duration, placed bool) {
	f.t.Helper()
	placement := "null"
	if placed {
		placement = `{"terminal": "kitty", "socket": "unix:/tmp/kitty-1", "window_id": 7}`
	}
	doc := fmt.Sprintf(`{"schema": 1, "job": %q, "token": %q, "created_at": %q, "placement": %s}`,
		job, token, model.FormatTimestamp(t0.Add(-age)), placement)
	if _, r := model.ReadReservation([]byte(doc), job); !r.Usable {
		f.t.Fatalf("fixture reservation unusable: %s", r.Reason())
	}
	f.write(f.path("reservations", job+".json"), doc)
}

func (f *fix) reserved(job string) bool {
	_, err := os.Stat(f.path("reservations", job+".json"))
	return err == nil
}

// table is the fake process table of Adopt rule 3: pids 100-199 run, started
// as linux:boot:<pid>; 300 can't be checked; every other is gone.
func table(pid int64) (string, error) {
	switch {
	case pid >= 100 && pid < 200:
		return fmt.Sprintf("linux:boot:%d", pid), nil
	case pid == 300:
		return "", errors.New("unreadable process table")
	}
	return "", proc.ErrNoProcess
}

// other records another session of the state directory: started in the
// process pid, its sesshin.json naming job ("" for none), and with end, ended.
// It sees no SESSHIN_JOB, and its log lines are dropped.
func (f *fix) other(id string, pid int64, job string, end bool) {
	f.t.Helper()
	e := f.as(id)
	e.Getenv = func(string) string { return "" }
	e.Log = func(string) {}
	no := false
	e.Lookup = func(fsys.FS, string) proc.Claude {
		return proc.Claude{PID: pid, StartedAt: fmt.Sprintf("linux:boot:%d", pid), Nested: &no}
	}
	if err := recordWith(e, Event{Kind: SessionStart, Source: "startup"}); err != nil {
		f.t.Fatal(err)
	}
	h := f.sesshin(id)
	if job != "" {
		h.Job = &job
	}
	out, err := jsonio.MarshalFile(h)
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(f.sessionPath(id, "sesshin.json"), string(out))
	if end {
		if err := recordWith(e, Event{Kind: SessionEnd, Reason: "logout"}); err != nil {
			f.t.Fatal(err)
		}
	}
}

// jobFix is a fixture whose Adopt rule 3 reads the fake table, and whose
// session runs with SESSHIN_JOB job and SESSHIN_TOKEN token.
func jobFix(t *testing.T, job, token string) *fix {
	f := newFix(t)
	f.env.StartedAt = table
	if job != "" {
		f.setenv("SESSHIN_JOB", job)
	}
	if token != "" {
		f.setenv("SESSHIN_TOKEN", token)
	}
	return f
}

// wantJob checks session id's sesshin.json: its job ("" for null) and source.
func (f *fix) wantJob(id, job, source string) {
	f.t.Helper()
	h := f.sesshin(id)
	if val(h.Job) != job || (h.Job == nil) != (job == "") || h.Source != source {
		f.t.Errorf("job %v, source %q; want %q, %q\nlog:\n%s", h.Job, h.Source, job, source, f.logged())
	}
}

func start() Event { return Event{Kind: SessionStart, Source: "startup"} }

// Design-spec, Reservations, Adopt, rule 1: a session started by another
// session gets no job, whatever it inherited, and takes no reservation. A
// nested that is unknown (null) is not nested.
func TestAdoptNested(t *testing.T) {
	f := jobFix(t, "api", tokA)
	f.reserve("api", tokA, time.Minute, true)
	f.env.Lookup = nestedClaude
	f.rec(start())
	f.wantJob(sid, "", "hook")
	if !f.reserved("api") {
		t.Error("the reservation was taken by a nested session")
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}

	g := jobFix(t, "api", tokA)
	g.reserve("api", tokA, time.Minute, true)
	g.env.Lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{} } // nested null
	g.rec(start())
	g.wantJob(sid, "api", "spawn")
}

// Rule 2: a fresh reservation whose token is SESSHIN_TOKEN is this session's:
// the job, source spawn, and the reservation removed.
func TestAdoptFreshReservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		age    time.Duration
		placed bool
	}{
		{"launched", time.Hour, true},
		{"just made", time.Second, false},
		{"not yet stranded", 110 * time.Second, false},
		{"launched, almost a day old", 23 * time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := jobFix(t, "api", tokA)
			f.reserve("api", tokA, tc.age, tc.placed)
			// A live session holding the job doesn't matter: the reservation
			// is this session's, and the holder is another claim's problem.
			f.rec(start())
			f.wantJob(sid, "api", "spawn")
			if f.reserved("api") {
				t.Error("the reservation is still there")
			}
			if got := f.logged(); got != "" {
				t.Errorf("log %q", got)
			}
		})
	}
}

// Rule 2: a stale matching reservation is removed all the same, and rule 3
// decides: the job is taken if no one holds it, with source hook.
func TestAdoptStaleReservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		age    time.Duration
		placed bool
	}{
		{"stranded", 3 * time.Minute, false},
		{"expired", 25 * time.Hour, true},
	} {
		t.Run(tc.name+", the job free", func(t *testing.T) {
			f := jobFix(t, "api", tokA)
			f.reserve("api", tokA, tc.age, tc.placed)
			f.rec(start())
			f.wantJob(sid, "api", "hook")
			if f.reserved("api") {
				t.Error("the stale reservation is still there")
			}
		})
		t.Run(tc.name+", the job taken", func(t *testing.T) {
			f := jobFix(t, "api", tokA)
			f.reserve("api", tokA, tc.age, tc.placed)
			f.other(idB, 101, "api", false)
			f.logs = nil
			f.rec(start())
			f.wantJob(sid, "", "hook")
			if f.reserved("api") {
				t.Error("the stale reservation is still there")
			}
			if got := f.logged(); got != "job api held by #1" {
				t.Errorf("log %q", got)
			}
		})
	}
}

// Rule 3: SESSHIN_JOB is the job unless a live or unknown session reports it, or
// a fresh reservation holds it. An ended session's job is free.
func TestAdoptHeld(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fix)
		job   string
		log   string
	}{
		{"free", func(f *fix) {}, "api", ""},
		{"a live session", func(f *fix) { f.other(idB, 101, "api", false) }, "", "job api held by #1"},
		{"an unknown session", func(f *fix) { f.other(idB, 300, "api", false) }, "", "job api held by #1"},
		{"a session ended by SessionEnd", func(f *fix) { f.other(idB, 101, "api", true) }, "api", ""},
		{"a session whose process is gone", func(f *fix) { f.other(idB, 9, "api", false) }, "api", ""},
		{"a live session of another job", func(f *fix) { f.other(idB, 101, "web", false) }, "api", ""},
		{"a live session with no job", func(f *fix) { f.other(idB, 101, "", false) }, "api", ""},
		{"a live session of the same job in another case", func(f *fix) { f.other(idB, 101, "API", false) }, "", "job api held by #1"},
		{"an ended one and a live one", func(f *fix) {
			f.other(idB, 101, "api", true)
			f.other(idC, 102, "api", false)
		}, "", "job api held by #"},
		{"a fresh reservation of another token", func(f *fix) { f.reserve("api", tokB, time.Minute, true) }, "", "job api held by a reservation"},
		{"a stale reservation of another token", func(f *fix) { f.reserve("api", tokB, 3*time.Minute, false) }, "api", ""},
		{"a reservation of another job", func(f *fix) { f.reserve("web", tokB, time.Minute, true) }, "api", ""},
		{"an unusable reservation", func(f *fix) { f.write(f.path("reservations", "api.json"), "{") }, "api", ""},
		{"a session with no usable lifecycle.json", func(f *fix) {
			f.write(f.sessionPath(idB, "sesshin.json"), `{"schema": 1, "id": 9, "job": "api", "source": "hook", "placement": null}`)
			f.write(f.sessionPath(idB, "lifecycle.json"), "{")
		}, "api", "last_id rebuilt from 9"},
		{"an earlier session of this process, its SessionEnd lost", func(f *fix) {
			f.other(idB, 101, "api", false)
			f.env.Now = t0.Add(time.Second)
			f.env.Lookup = func(fsys.FS, string) proc.Claude {
				no := false
				return proc.Claude{PID: 101, StartedAt: "linux:boot:101", Nested: &no}
			}
		}, "api", ""},
		{"a session with an unusable sesshin.json", func(f *fix) {
			f.other(idB, 101, "api", false)
			f.write(f.sessionPath(idB, "sesshin.json"), "{")
		}, "api", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := jobFix(t, "api", "") // no token: rule 3 alone
			tc.setup(f)
			f.logs = nil
			f.rec(start())
			f.wantJob(sid, tc.job, "hook")
			if got := f.logged(); !strings.HasPrefix(got, tc.log) || tc.log == "" && got != "" {
				t.Errorf("log %q, want %q", got, tc.log)
			}
		})
	}
}

// Rule 3 reads every other session and nothing of this one: its own earlier
// file, left over, holds nothing.
func TestAdoptLeavesItselfOut(t *testing.T) {
	f := jobFix(t, "api", "")
	f.rec(start())
	f.wantJob(sid, "api", "hook")
	// A second start of the same session (no ID pending): kept, not decided
	// again, and not held against itself either.
	f.rec(Event{Kind: SessionStart, Source: "resume"})
	f.wantJob(sid, "api", "hook")
}

// Rule 4: with no SESSHIN_JOB, no job.
func TestAdoptNoJob(t *testing.T) {
	f := jobFix(t, "", tokA)
	f.reserve("api", tokA, time.Minute, true)
	f.rec(start())
	f.wantJob(sid, "", "hook")
	if !f.reserved("api") {
		t.Error("a reservation was taken with no SESSHIN_JOB")
	}
}

// A malformed SESSHIN_JOB or SESSHIN_TOKEN is ignored, as if unset; only
// session-start logs it.
func TestAdoptMalformed(t *testing.T) {
	for _, tc := range []struct {
		name, job, token string
		jobWant          string
		logs             []string
	}{
		{"job", "API Review", tokA, "", []string{"SESSHIN_JOB"}},
		{"all digits", "12", "", "", []string{"SESSHIN_JOB"}},
		{"token", "api", "XYZ", "api", []string{"SESSHIN_TOKEN"}},
		{"token too short", "api", "3fa8", "api", []string{"SESSHIN_TOKEN"}},
		{"both", "-api", "3FA85F6457174562B3FC2C963F66AFA6", "", []string{"SESSHIN_JOB", "SESSHIN_TOKEN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := jobFix(t, tc.job, tc.token)
			// A reservation the bad token must not take.
			f.reserve("api", tokA, time.Minute, true)
			f.rec(start())
			job := tc.jobWant
			if job == "api" {
				// The reservation is another token's, and fresh: held.
				job = ""
			}
			f.wantJob(sid, job, "hook")
			if !f.reserved("api") {
				t.Error("the reservation was removed")
			}
			log := f.logged()
			for _, name := range tc.logs {
				if !strings.Contains(log, name) {
					t.Errorf("log %q, want it to name %s", log, name)
				}
			}
			if n := strings.Count(log, "SESSHIN_"); n != len(tc.logs) {
				t.Errorf("log %q: %d lines about the variables, want %d", log, n, len(tc.logs))
			}
			// Any other hook that adopts says nothing of them.
			f.logs = nil
			if err := recordWith(f.as(idB), Event{Kind: PostToolUse}); err != nil {
				t.Fatal(err)
			}
			if got := f.logged(); strings.Contains(got, "SESSHIN_") {
				t.Errorf("a PostToolUse logged %q", got)
			}
		})
	}
}

// A pending id, completed by a later hook, has its job decided then (hooks-spec,
// Creating sesshin.json, When the ID can't be issued): the session that couldn't
// be issued an ID has no job, and no reservation is taken.
func TestAdoptPendingID(t *testing.T) {
	f := jobFix(t, "api", tokA)
	f.reserve("api", tokA, time.Minute, true)
	e := f.env
	e.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, "state.json", 1, syscall.EIO)}
	if err := recordWith(e, start()); err == nil {
		t.Fatal("no error")
	}
	h := f.sesshin(sid)
	if h.ID != nil || h.Job != nil || h.Source != "hook" {
		t.Errorf("pending sesshin.json: %+v", h)
	}
	if !f.reserved("api") {
		t.Error("the reservation was taken without an ID")
	}
	// The next lifecycle hook completes it, and decides the job.
	if err := recordWith(f.env, Event{Kind: PostToolUse}); err != nil {
		t.Fatal(err)
	}
	if f.sesshinID(sid) != 1 {
		t.Errorf("id %d", f.sesshinID(sid))
	}
	f.wantJob(sid, "api", "spawn")
	if f.reserved("api") {
		t.Error("the reservation is still there")
	}
}

// A file that already names a job keeps it, and its source, when its id is
// completed: the job is decided once.
func TestAdoptPendingKeepsItsJob(t *testing.T) {
	f := jobFix(t, "api", "")
	f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 1, "id": null, "job": "kept", "source": "spawn", "placement": null}`)
	f.rec(Event{Kind: PostToolUse})
	f.wantJob(sid, "kept", "spawn")
	if f.sesshinID(sid) != 1 {
		t.Errorf("id %d", f.sesshinID(sid))
	}
}

// Step 4: a reservation whose removal fails is logged, and the job is the
// session's all the same; it goes stale.
func TestAdoptRemovalFails(t *testing.T) {
	f := jobFix(t, "api", tokA)
	f.reserve("api", tokA, time.Minute, true)
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpRemove, "api.json", 1, syscall.EIO)}
	f.rec(start())
	f.wantJob(sid, "api", "spawn")
	if !f.reserved("api") {
		t.Error("the reservation is gone")
	}
	if got := f.logged(); !strings.Contains(got, "remove reservation: ") {
		t.Errorf("log %q", got)
	}
}

// Step 4: the reservation is removed only when sesshin.json was written.
func TestAdoptKeepsReservationWhenWriteFails(t *testing.T) {
	f := jobFix(t, "api", tokA)
	f.reserve("api", tokA, time.Minute, true)
	temps := 0
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		// lifecycle.json's temp file, then state.json's; sesshin.json's fails.
		if op.Name == fsys.OpCreateTemp {
			if temps++; temps == 3 {
				return syscall.ENOSPC
			}
		}
		return nil
	}}
	if err := recordWith(f.env, start()); err == nil {
		t.Fatal("no error")
	}
	if !f.reserved("api") {
		t.Error("the reservation was removed though sesshin.json wasn't written")
	}
}

// A session's job survives a hook that finds it already decided: only the
// SessionStart of a resume takes a reservation.
func TestAdoptOnlyOnce(t *testing.T) {
	f := jobFix(t, "", "")
	f.rec(start())
	f.wantJob(sid, "", "hook")
	f.setenv("SESSHIN_JOB", "api")
	f.rec(Event{Kind: PostToolUse})
	f.wantJob(sid, "", "hook")
}

// resumeFix is a session already recorded, with no job, that is then resumed
// with SESSHIN_JOB and SESSHIN_TOKEN set.
func resumeFix(t *testing.T) *fix {
	f := newFix(t)
	f.env.StartedAt = table
	f.rec(start())
	f.setenv("SESSHIN_JOB", "api")
	f.setenv("SESSHIN_TOKEN", tokA)
	return f
}

// stateLocks counts the locks taken on sessions/, the state lock.
func (f *fix) stateLocks(locks *int) {
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpLock && op.Root == f.path("sessions") {
			*locks++
		}
		return nil
	}}
}

// Hooks-spec, session-start, Adopting a resumed session's reservation: a
// reservation whose token is SESSHIN_TOKEN is removed, and when fresh sets the
// job; source is never changed.
func TestAdoptOnResume(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		f := resumeFix(t)
		f.reserve("api", tokA, time.Minute, true)
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		f.wantJob(sid, "api", "hook")
		if f.reserved("api") {
			t.Error("the reservation is still there")
		}
	})
	t.Run("fresh, replacing the stored job", func(t *testing.T) {
		f := resumeFix(t)
		f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 1, "id": 1, "job": "old", "source": "spawn", "placement": null}`)
		f.reserve("api", tokA, time.Minute, true)
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		f.wantJob(sid, "api", "spawn")
		if f.reserved("api") {
			t.Error("the reservation is still there")
		}
	})
	t.Run("stale", func(t *testing.T) {
		f := resumeFix(t)
		f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 1, "id": 1, "job": "old", "source": "hook", "placement": null}`)
		f.reserve("api", tokA, 3*time.Minute, false)
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		f.wantJob(sid, "old", "hook")
		if f.reserved("api") {
			t.Error("the stale reservation is still there")
		}
	})
	t.Run("no reservation, and no state lock", func(t *testing.T) {
		f := resumeFix(t)
		locks := 0
		f.stateLocks(&locks)
		f.rec(Event{Kind: SessionStart, Source: "clear"})
		f.wantJob(sid, "", "hook")
		if locks != 0 {
			t.Errorf("%d state locks", locks)
		}
	})
	t.Run("another token", func(t *testing.T) {
		f := resumeFix(t)
		f.reserve("api", tokB, time.Minute, true)
		locks := 0
		f.stateLocks(&locks)
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		f.wantJob(sid, "", "hook")
		if !f.reserved("api") || locks != 0 {
			t.Errorf("reserved %v, %d state locks", f.reserved("api"), locks)
		}
	})
	t.Run("no SESSHIN_TOKEN", func(t *testing.T) {
		f := resumeFix(t)
		f.setenv("SESSHIN_TOKEN", "")
		f.reserve("api", tokA, time.Minute, true)
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		f.wantJob(sid, "", "hook")
		if !f.reserved("api") {
			t.Error("the reservation was removed")
		}
	})
	t.Run("only session-start", func(t *testing.T) {
		f := resumeFix(t)
		f.reserve("api", tokA, time.Minute, true)
		f.rec(Event{Kind: PostToolUse})
		f.wantJob(sid, "", "hook")
		if !f.reserved("api") {
			t.Error("a PostToolUse took the reservation")
		}
	})
	t.Run("a pending id completed by the resume", func(t *testing.T) {
		f := resumeFix(t)
		f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 1, "id": null, "job": null, "source": "hook", "placement": null}`)
		f.reserve("api", tokA, time.Minute, true)
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		f.wantJob(sid, "api", "spawn")
		if f.reserved("api") || f.sesshinID(sid) != 2 {
			t.Errorf("reserved %v, id %d", f.reserved("api"), f.sesshinID(sid))
		}
	})
}

// A removal that fails is logged, and the reservation goes stale.
func TestAdoptOnResumeFailures(t *testing.T) {
	t.Run("removal", func(t *testing.T) {
		f := resumeFix(t)
		f.reserve("api", tokA, time.Minute, true)
		f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpRemove, "api.json", 1, syscall.EIO)}
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		if !f.reserved("api") {
			t.Error("the reservation is gone")
		}
		if got := f.logged(); !strings.Contains(got, "remove reservation: ") {
			t.Errorf("log %q", got)
		}
	})
	t.Run("state lock", func(t *testing.T) {
		f := resumeFix(t)
		f.reserve("api", tokA, time.Minute, true)
		f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
			if op.Name == fsys.OpLock && op.Root == f.path("sessions") {
				return syscall.EAGAIN
			}
			return nil
		}}
		f.rec(Event{Kind: SessionStart, Source: "resume"})
		f.wantJob(sid, "", "hook")
		if !f.reserved("api") {
			t.Error("the reservation is gone")
		}
		if got := f.logged(); !strings.Contains(got, "state lock: ") {
			t.Errorf("log %q", got)
		}
	})
}
