package live

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

var now = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

const (
	idA = "00000000-0000-4000-8000-00000000000a"
	idB = "00000000-0000-4000-8000-00000000000b"
	idC = "00000000-0000-4000-8000-00000000000c"
)

// started is the start time of a fake process: the one every fake table
// holds for pid 100.
const started = "linux:boot:100"

// sess builds a session with a usable lifecycle.json that has no pid.
func sess(id, lastStart, lastEvent string) Session {
	return Session{ID: id, Lifecycle: &model.LifecycleFile{
		SessionID:   id,
		LastStartAt: model.Timestamp(lastStart),
		LastEventAt: model.Timestamp(lastEvent),
	}}
}

func withPair(s Session, pid int64, at string) Session {
	s.Lifecycle.PID, s.Lifecycle.PIDStartedAt = &pid, &at
	return s
}

func withStatusline(s Session, receivedAt string, pid int64, at string) Session {
	st := &model.StatuslineFile{ReceivedAt: model.Timestamp(receivedAt)}
	if pid != 0 {
		st.PID, st.PIDStartedAt = &pid, &at
	}
	s.Statusline = st
	return s
}

func ended(s Session, at string, reason *string) Session {
	e := model.Timestamp(at)
	s.Lifecycle.EndedAt, s.Lifecycle.EndReason = &e, reason
	return s
}

func str(s string) *string { return &s }

// table is a process table: pid 100 started at started, 200 at another
// time, and any other pid is not there. Pids 300-302 can't be checked.
func table(pid int64) (string, error) {
	switch pid {
	case 100:
		return started, nil
	case 200:
		return "linux:boot:200", nil
	case 300:
		return "", &os.PathError{Op: "open", Path: "/proc/300/stat", Err: syscall.EACCES}
	case 301:
		return "", errors.New("malformed process table entry")
	case 302:
		// A missing boot ID is the check failing, though its errno is ENOENT.
		return "", &os.PathError{Op: "open", Path: "/proc/sys/kernel/random/boot_id", Err: syscall.ENOENT}
	}
	return "", proc.ErrNoProcess
}

func TestDerive(t *testing.T) {
	const (
		stale  = "2026-10-01T00:00:00Z" // past the TTL
		recent = "2026-10-03T11:00:00Z" // inside it
	)
	for _, tc := range []struct {
		name     string
		s        Session
		state    State
		reason   string
		lastSeen string
	}{
		{"ended_at set, with its reason",
			ended(withPair(sess(idA, recent, recent), 100, started), recent, str("logout")), Ended, "logout", recent},
		{"ended_at set, no reason",
			ended(sess(idA, recent, recent), recent, nil), Ended, "", recent},
		{"ended_at set wins over a live process",
			ended(withPair(sess(idA, recent, recent), 100, started), recent, str("other")), Ended, "other", recent},
		{"ended_at set, pid from the statusline only",
			ended(withStatusline(sess(idA, recent, recent), recent, 100, started), recent, str("clear")), Ended, "clear", recent},
		{"pair from lifecycle, process matches",
			withPair(sess(idA, recent, recent), 100, started), Live, "", recent},
		{"pair from the statusline, process matches",
			withStatusline(sess(idA, recent, recent), recent, 100, started), Live, "", recent},
		{"pair from the statusline, no such process",
			withStatusline(sess(idA, recent, recent), recent, 7, "linux:boot:7"), Ended, "", recent},
		{"lifecycle's pair wins over the statusline's",
			withStatusline(withPair(sess(idA, recent, recent), 7, "linux:boot:7"), recent, 100, started), Ended, "", recent},
		{"statusline without a pair",
			withStatusline(sess(idA, recent, recent), recent, 0, ""), Live, "", recent},
		{"no pair, inside the TTL",
			sess(idA, recent, recent), Live, "", recent},
		{"no pair, exactly at the TTL",
			sess(idA, recent, "2026-10-02T12:00:00Z"), Live, "", "2026-10-02T12:00:00Z"},
		{"no pair, past the TTL",
			sess(idA, stale, stale), Ended, "", stale},
		{"no pair, stale lifecycle but a recent statusline",
			withStatusline(sess(idA, stale, stale), recent, 0, ""), Live, "", recent},
		{"no such process",
			withPair(sess(idA, recent, recent), 9, started), Ended, "", recent},
		{"the check fails (boot ID missing)",
			withPair(sess(idA, recent, recent), 302, started), Unknown, "", recent},
		{"a different start time",
			withPair(sess(idA, recent, recent), 200, started), Ended, "", recent},
		{"the check fails (EACCES)",
			withPair(sess(idA, recent, recent), 300, started), Unknown, "", recent},
		{"the check fails (no errno)",
			withPair(sess(idA, recent, recent), 301, started), Unknown, "", recent},
		{"unusable lifecycle",
			Session{ID: idA}, Unknown, "", ""},
		{"unusable lifecycle, usable statusline",
			Session{ID: idA, Statusline: &model.StatuslineFile{ReceivedAt: recent, PID: new64(100), PIDStartedAt: str(started)}}, Unknown, "", recent},
		{"last seen is the lifecycle's when later",
			withStatusline(sess(idA, stale, recent), stale, 0, ""), Live, "", recent},
		{"last seen is the statusline's when later",
			withStatusline(sess(idA, stale, "2026-10-03T01:00:00Z"), recent, 0, ""), Live, "", recent},
	} {
		got := Derive([]Session{tc.s}, now, table)[0]
		if got.State != tc.state || got.EndReason != tc.reason || string(got.LastSeen) != tc.lastSeen {
			t.Errorf("%s: %v %q seen %q; want %v %q seen %q", tc.name,
				got.State, got.EndReason, got.LastSeen, tc.state, tc.reason, tc.lastSeen)
		}
	}
}

func new64(n int64) *int64 { return &n }

// Rule 3: of the sessions sharing a process, the top-ranked is live.
func TestDeriveRank(t *testing.T) {
	const (
		t1 = "2026-10-03T10:00:00Z"
		t2 = "2026-10-03T11:00:00Z"
	)
	pairOf := func(id, start, event string) Session { return withPair(sess(id, start, event), 100, started) }
	for _, tc := range []struct {
		name string
		in   []Session
		live string // the one live ID
	}{
		{"last_start_at first", []Session{pairOf(idA, t1, t2), pairOf(idB, t2, t1)}, idB},
		{"last_start_at first, order of input", []Session{pairOf(idB, t2, t1), pairOf(idA, t1, t2)}, idB},
		{"then last_event_at", []Session{pairOf(idA, t1, t1), pairOf(idB, t1, t2)}, idB},
		{"then the lower UUID", []Session{pairOf(idB, t1, t1), pairOf(idA, t1, t1)}, idA},
		{"three", []Session{pairOf(idC, t1, t1), pairOf(idB, t2, t1), pairOf(idA, t1, t2)}, idB},
		{"pair from the statusline", []Session{
			withStatusline(sess(idA, t1, t1), t1, 100, started), withStatusline(sess(idB, t2, t2), t2, 100, started)}, idB},
		{"a pair from each file", []Session{
			withStatusline(sess(idA, t1, t1), t1, 100, started), pairOf(idB, t2, t2)}, idB},
	} {
		got := Derive(tc.in, now, table)
		for i, r := range got {
			want, reason := Ended, Superseded
			if tc.in[i].ID == tc.live {
				want, reason = Live, ""
			}
			if r.State != want || r.EndReason != reason || r.ID != tc.in[i].ID {
				t.Errorf("%s: %s is %v %q; want %v %q", tc.name, r.ID, r.State, r.EndReason, want, reason)
			}
		}
	}
}

// Sessions in other processes, with unusable lifecycles, or ended some
// other way don't change who is live.
func TestDeriveRankScope(t *testing.T) {
	const t1, t2 = "2026-10-03T10:00:00Z", "2026-10-03T11:00:00Z"
	pairOf := func(id string, pid int64, at, start string) Session {
		return withPair(sess(id, start, start), pid, at)
	}
	in := []Session{
		pairOf(idA, 100, started, t1),
		pairOf(idB, 200, "linux:boot:200", t2),   // another process
		pairOf(idC, 100, "linux:boot:other", t2), // same pid, another start time
		{ID: "x", Statusline: &model.StatuslineFile{ReceivedAt: t2, PID: new64(100), PIDStartedAt: str(started)}}, // unusable lifecycle
	}
	got := Derive(in, now, table)
	want := []State{Live, Live, Ended, Unknown}
	for i, r := range got {
		if r.State != want[i] || r.EndReason != "" {
			t.Errorf("%s: %v %q; want %v", r.ID, r.State, r.EndReason, want[i])
		}
	}
}

// A process whose check failed leaves its sessions unknown, superseded or
// not: no session is called ended on a guess.
func TestDeriveRankUnknown(t *testing.T) {
	const t1, t2 = "2026-10-03T10:00:00Z", "2026-10-03T11:00:00Z"
	in := []Session{
		withPair(sess(idA, t1, t1), 300, started),
		withPair(sess(idB, t2, t2), 300, started),
	}
	for _, r := range Derive(in, now, table) {
		if r.State != Unknown {
			t.Errorf("%s: %v, want unknown", r.ID, r.State)
		}
	}
}

// The start time is looked up once per pid.
func TestDeriveLooksUpOncePerPID(t *testing.T) {
	calls := map[int64]int{}
	count := func(pid int64) (string, error) {
		calls[pid]++
		return table(pid)
	}
	const t1 = "2026-10-03T10:00:00Z"
	in := []Session{
		withPair(sess(idA, t1, t1), 100, started),
		withPair(sess(idB, t1, t1), 100, started),
		withStatusline(sess(idC, t1, t1), t1, 100, "linux:boot:other"),
		withPair(sess("d", t1, t1), 300, started),
		withPair(sess("e", t1, t1), 300, started),
		sess("f", t1, t1),
	}
	Derive(in, now, count)
	if calls[100] != 1 || calls[300] != 1 || len(calls) != 2 {
		t.Errorf("lookups %v, want one each for 100 and 300", calls)
	}
}

func TestDeriveKeepsOrder(t *testing.T) {
	in := []Session{sess(idC, "2026-10-03T10:00:00Z", "2026-10-03T10:00:00Z"), {ID: idA}, sess(idB, "2026-10-03T10:00:00Z", "2026-10-03T10:00:00Z")}
	for i, r := range Derive(in, now, table) {
		if r.ID != in[i].ID {
			t.Errorf("result %d is %s, want %s", i, r.ID, in[i].ID)
		}
	}
	if got := Derive(nil, now, table); len(got) != 0 {
		t.Errorf("no sessions gave %v", got)
	}
}

func TestStateString(t *testing.T) {
	for s, want := range map[State]string{Live: "live", Ended: "ended", Unknown: "unknown"} {
		if s.String() != want {
			t.Errorf("%d is %q, want %q", s, s.String(), want)
		}
	}
}

// The real process table: this process is live by its own start time, and
// a process that can't exist is ended.
func TestDeriveRealProc(t *testing.T) {
	startedAt := func(pid int64) (string, error) { return proc.StartedAt(fsys.OS{}, pid) }
	pid := int64(os.Getpid())
	at, err := startedAt(pid)
	if err != nil {
		t.Skipf("no process table: %v", err)
	}
	const t1 = "2026-10-03T10:00:00Z"
	in := []Session{
		withPair(sess(idA, t1, t1), pid, at),
		withPair(sess(idB, t1, t1), pid, at+"x"),
		withPair(sess(idC, t1, t1), 1<<30, at),
	}
	got := Derive(in, now, startedAt)
	want := []State{Live, Ended, Ended}
	for i, r := range got {
		if r.State != want[i] {
			t.Errorf("%s: %v, want %v", r.ID, r.State, want[i])
		}
	}
}

// The unknown-pid limit is a constant 24 hours, not a setting.
func TestUnknownPIDTTL(t *testing.T) {
	if UnknownPIDTTL != 24*time.Hour {
		t.Errorf("UnknownPIDTTL = %v", UnknownPIDTTL)
	}
}

// Design-spec, Reservations, Revived by hand: among live sessions storing one
// job, the earliest last_start_at holds it, then the lower UUID; the others
// report nil. A session whose liveness is unknown counts as live; an ended
// one reports what it stored.
func TestJobs(t *testing.T) {
	const (
		early = "2026-10-03T09:00:00Z"
		mid   = "2026-10-03T10:00:00Z"
		late  = "2026-10-03T11:00:00Z"
	)
	// Each session runs in a process of its own, so rule 3 ends none of them.
	alive := func(pid int64) (string, error) {
		if pid >= 100 && pid < 200 {
			return fmt.Sprintf("linux:boot:%d", pid), nil
		}
		return table(pid)
	}
	pid := int64(100)
	run := func(id, lastStart string, pid int64) Session {
		return withPair(sess(id, lastStart, late), pid, fmt.Sprintf("linux:boot:%d", pid))
	}
	live := func(id, lastStart string) Session { pid++; return run(id, lastStart, pid) }
	unknown := func(id, lastStart string) Session { return withPair(sess(id, lastStart, late), 300, started) }
	gone := func(id, lastStart string) Session { return withPair(sess(id, lastStart, late), 9, started) }
	api, web, apiUp := str("api"), str("web"), str("API")
	for _, tc := range []struct {
		name string
		in   []Session
		jobs []*string
		want []*string
	}{
		{"the earliest start holds it",
			[]Session{live(idA, mid), live(idB, early), live(idC, late)}, []*string{api, api, api}, []*string{nil, api, nil}},
		{"the lower UUID holds a tie",
			[]Session{live(idB, mid), live(idA, mid)}, []*string{api, api}, []*string{nil, api}},
		{"jobs differing only in case are one job",
			[]Session{live(idA, mid), live(idB, early)}, []*string{api, apiUp}, []*string{nil, apiUp}},
		{"another job is its own",
			[]Session{live(idA, early), live(idB, mid), live(idC, late)}, []*string{api, web, nil}, []*string{api, web, nil}},
		{"an unknown session holds its job",
			[]Session{unknown(idA, early), live(idB, mid)}, []*string{api, api}, []*string{api, nil}},
		{"a live holder keeps it from an unknown one",
			[]Session{unknown(idA, mid), live(idB, early)}, []*string{api, api}, []*string{nil, api}},
		{"an ended session reports what it stored, and holds nothing",
			[]Session{gone(idA, early), live(idB, mid), ended(live(idC, early), late, nil)}, []*string{api, api, api}, []*string{api, api, api}},
		{"no stored job",
			[]Session{live(idA, early)}, []*string{nil}, []*string{nil}},
		{"nothing",
			nil, nil, []*string{}},
	} {
		got := Jobs(tc.in, Derive(tc.in, now, alive), tc.jobs)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %d jobs, want %d", tc.name, len(got), len(tc.want))
			continue
		}
		for i := range got {
			if (got[i] == nil) != (tc.want[i] == nil) || got[i] != nil && *got[i] != *tc.want[i] {
				t.Errorf("%s: session %d reports %v, want %v", tc.name, i, deref(got[i]), deref(tc.want[i]))
			}
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}
