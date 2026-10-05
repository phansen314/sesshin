package live

import (
	"cmp"
	"errors"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

// State is a session's liveness (design-spec.md, Liveness).
type State int

const (
	// Live: its process is running, or its pid is unknown and it is
	// recent.
	Live State = iota
	// Ended: it has ended, or its process is gone.
	Ended
	// Unknown: liveness can't be judged. Never pruned.
	Unknown
)

func (s State) String() string {
	switch s {
	case Live:
		return "live"
	case Ended:
		return "ended"
	}
	return "unknown"
}

// Superseded is the end reason of a session that rule 3 ended: another
// session ranks above it in the same process. It is never stored.
const Superseded = "superseded"

// Session is one session directory as read. A file that isn't usable is
// nil, as a missing one is (design-spec.md, Format versions).
type Session struct {
	// ID is the directory's name: the session's UUID.
	ID         string
	Lifecycle  *model.LifecycleFile
	Statusline *model.StatuslineFile
}

// Result is the liveness of one session.
type Result struct {
	ID    string
	State State
	// EndReason is why an Ended session ended: its stored end_reason,
	// Superseded, or "" when it has none (its process is gone, its unknown
	// pid timed out, or its stored end_reason is null).
	EndReason string
	// LastSeen is the later of lifecycle.json's last_event_at and
	// statusline.json's received_at, from the files that are usable; "" when
	// neither is.
	LastSeen model.Timestamp
}

// StartedAt returns process pid's current pid_started_at, as
// proc.StartedAt does: proc.ErrNoProcess when there is no such process, and
// any other error when the check failed.
type StartedAt func(pid int64) (string, error)

// pair is a process identity: the pid and its start time.
type pair struct {
	pid     int64
	started string
}

// UnknownPIDTTL is how long after its last seen a session with no pid stays
// live (design-spec.md, Liveness). Fixed, not configured: the hooks judge
// liveness too, and read no config.toml.
const UnknownPIDTTL = 24 * time.Hour

// Derive judges sessions, returning one Result per session in the same
// order. now decides a session with no pid, against UnknownPIDTTL; startedAt
// is called once per distinct pid.
func Derive(sessions []Session, now time.Time, startedAt StartedAt) []Result {
	type lookup struct {
		started string
		err     error
	}
	cache := map[int64]lookup{}
	current := func(pid int64) (string, error) {
		l, ok := cache[pid]
		if !ok {
			l.started, l.err = startedAt(pid)
			cache[pid] = l
		}
		return l.started, l.err
	}

	results := make([]Result, len(sessions))
	pairs := make([]*pair, len(sessions))
	groups := map[pair][]int{} // sessions with a usable lifecycle.json, by pair
	for i, s := range sessions {
		results[i] = Result{ID: s.ID, LastSeen: lastSeen(s)}
		if s.Lifecycle == nil {
			results[i].State = Unknown
			continue
		}
		if pairs[i] = pidPair(s); pairs[i] != nil {
			groups[*pairs[i]] = append(groups[*pairs[i]], i)
		}
	}

	for i, s := range sessions {
		r := &results[i]
		l := s.Lifecycle
		switch {
		case l == nil:
		case l.EndedAt != nil:
			r.State = Ended
			if l.EndReason != nil {
				r.EndReason = *l.EndReason
			}
		case pairs[i] == nil:
			r.State = Live
			if now.Sub(r.LastSeen.Time()) > UnknownPIDTTL {
				r.State = Ended
			}
		default:
			started, err := current(pairs[i].pid)
			switch {
			case err != nil && !errors.Is(err, proc.ErrNoProcess):
				r.State = Unknown
			case err != nil || started != pairs[i].started:
				r.State = Ended
			default:
				r.State = Live
			}
		}
	}

	// Rule 3: only the top-ranked session of a process stays live.
	for _, g := range groups {
		top := g[0]
		for _, i := range g[1:] {
			if ranksAbove(sessions[i], sessions[top]) {
				top = i
			}
		}
		for _, i := range g {
			if i != top && results[i].State == Live {
				results[i].State = Ended
				results[i].EndReason = Superseded
			}
		}
	}
	return results
}

// pidPair is the session's pair: lifecycle.json's, else a usable
// statusline.json's; nil when neither has one. The session's lifecycle.json
// must be usable.
func pidPair(s Session) *pair {
	if l := s.Lifecycle; l.PID != nil {
		return &pair{*l.PID, *l.PIDStartedAt}
	}
	if st := s.Statusline; st != nil && st.PID != nil {
		return &pair{*st.PID, *st.PIDStartedAt}
	}
	return nil
}

// lastSeen is the later of the usable files' last_event_at and received_at.
// Timestamps have a fixed width, so they order as strings.
func lastSeen(s Session) model.Timestamp {
	var t model.Timestamp
	if s.Lifecycle != nil {
		t = s.Lifecycle.LastEventAt
	}
	if s.Statusline != nil {
		t = max(t, s.Statusline.ReceivedAt)
	}
	return t
}

// ranksAbove reports whether a ranks above b in one process: the later
// last_start_at, then the later last_event_at, then the lower UUID
// (design-spec.md, Liveness, rule 3).
func ranksAbove(a, b Session) bool {
	if c := cmp.Compare(a.Lifecycle.LastStartAt, b.Lifecycle.LastStartAt); c != 0 {
		return c > 0
	}
	if c := cmp.Compare(a.Lifecycle.LastEventAt, b.Lifecycle.LastEventAt); c != 0 {
		return c > 0
	}
	return strings.Compare(a.ID, b.ID) < 0
}

// Jobs is each session's reported job (design-spec.md, Reservations): jobs
// are the stored ones, nil for none, in the order of sessions, and results
// what Derive returned for them. Among the sessions storing one job (by
// model.JobKey: jobs that differ only in case are one) whose liveness is
// live or unknown, the one whose current life started first
// (earliest last_start_at, then the lower UUID) reports it, and the others
// report nil; an ended session reports what it stored. sessions must be every
// session read, so a narrowing that hides the holder still leaves the others
// nil. A session with no usable lifecycle.json holds nothing and reports
// what it stored.
func Jobs(sessions []Session, results []Result, jobs []*string) []*string {
	holder := map[string]int{}
	for i, job := range jobs {
		if job == nil || results[i].State == Ended || sessions[i].Lifecycle == nil {
			continue
		}
		key := model.JobKey(*job)
		h, ok := holder[key]
		if !ok || cmp.Or(cmp.Compare(sessions[i].Lifecycle.LastStartAt, sessions[h].Lifecycle.LastStartAt), strings.Compare(sessions[i].ID, sessions[h].ID)) < 0 {
			holder[key] = i
		}
	}
	reported := make([]*string, len(sessions))
	for i, job := range jobs {
		switch {
		case job == nil:
		case results[i].State == Ended || sessions[i].Lifecycle == nil || holder[model.JobKey(*job)] == i:
			reported[i] = job
		}
	}
	return reported
}
