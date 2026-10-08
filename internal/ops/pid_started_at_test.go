package ops

import (
	"slices"
	"testing"
	"time"
)

// Operations.md, Session view: pid_started_at follows pid and comes from the
// same file: lifecycle.json's, else statusline.json's; null exactly when pid
// is. (f.list checks every result against the schema.)
func TestViewPIDStartedAt(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11) // pair in lifecycle.json
	f.writeStatusline(uuidA, f.ago(time.Second), 99, "other")
	f.table[22] = "s22"
	f.session(uuidB, time.Minute, notEnded) // no pid in lifecycle.json
	f.writeStatusline(uuidB, f.ago(time.Second), 22, "s22")
	f.session(uuidC, time.Minute, notEnded) // neither

	_, l := f.list(`{"liveness":"all"}`)
	byID := map[string]map[string]any{}
	for _, s := range l.Sessions {
		byID[s["session_id"].(string)] = s
	}
	for _, c := range []struct {
		id      string
		pid     any
		started any
	}{
		{uuidA, float64(11), "s11"},
		{uuidB, float64(22), "s22"},
		{uuidC, nil, nil},
	} {
		s := byID[c.id]
		if s["pid"] != c.pid || s["pid_started_at"] != c.started {
			t.Errorf("%s: pid %v, pid_started_at %v; want %v, %v", c.id, s["pid"], s["pid_started_at"], c.pid, c.started)
		}
	}
	if got := keys(t, l.Raw[0]); slices.Index(got, "pid_started_at") != slices.Index(got, "pid")+1 {
		t.Errorf("keys %v", got)
	}

	// Two sessions of one process share the pair; the earlier is superseded.
	g := newPruneFixture(t)
	g.table[50] = "s50"
	g.session(uuidA, 10*time.Minute, notEnded, withPID(50, "s50"))
	g.session(uuidB, time.Minute, notEnded, withPID(50, "s50"))
	_, l = g.list(`{"liveness":"all"}`)
	if len(l.Sessions) != 2 {
		t.Fatalf("%d sessions", len(l.Sessions))
	}
	for _, s := range l.Sessions {
		if s["pid"] != float64(50) || s["pid_started_at"] != "s50" {
			t.Errorf("%v: pid %v, pid_started_at %v", s["session_id"], s["pid"], s["pid_started_at"])
		}
		if s["session_id"] == uuidA && s["end_reason"] != "superseded" {
			t.Errorf("earlier: end_reason %v", s["end_reason"])
		}
	}
}
