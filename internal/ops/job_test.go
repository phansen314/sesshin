package ops

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/model"
)

// sesshinWith writes a usable sesshin.json with id, job ("" for null), and source.
func (f *pruneFixture) sesshinWith(session string, id int64, job, source string) {
	f.t.Helper()
	jobJSON := "null"
	if job != "" {
		jobJSON = fmt.Sprintf("%q", job)
	}
	b := fmt.Sprintf(`{"schema":1,"id":%d,"job":%s,"source":%q,"placement":null}`, id, jobJSON, source)
	if _, r := model.ReadSesshin([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture sesshin.json unusable: %s", r.Reason())
	}
	f.write(session, "sesshin.json", []byte(b))
}

// startedAgo is a lifecycle whose current life started d before now.
func startedAgo(f *pruneFixture, d time.Duration) func(*model.LifecycleFile) {
	return func(l *model.LifecycleFile) { l.LastStartAt = model.FormatTimestamp(f.ago(d)) }
}

// jobs is the reported job of each session list shows, by UUID, "-" for
// null.
func jobs(l listed) map[string]string {
	out := map[string]string{}
	for _, s := range l.Sessions {
		j := "-"
		if s["job"] != nil {
			j = s["job"].(string)
		}
		out[s["session_id"].(string)] = j
	}
	return out
}

func TestViewJobAndSource(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshinWith(uuidA, 1, "api-review", "spawn")
	f.running(uuidB, 2*time.Minute, 12)
	f.sesshinWith(uuidB, 2, "", "hook")
	f.running(uuidC, 3*time.Minute, 13) // no sesshin.json
	f.running(uuidD, 4*time.Minute, 14)
	f.write(uuidD, "sesshin.json", []byte(`{"schema":1,"id":4,"placement":null}`)) // from before job and source

	env, l := f.list(`{}`)
	want := map[string][2]any{
		uuidA: {"api-review", "spawn"},
		uuidB: {nil, "hook"},
		uuidC: {nil, nil},
		uuidD: {nil, nil},
	}
	for _, s := range l.Sessions {
		id := s["session_id"].(string)
		if got := [2]any{s["job"], s["source"]}; got != want[id] {
			t.Errorf("%s: job, source %v, want %v", id, got, want[id])
		}
	}
	if got := warnKinds(env); !slices.Equal(got, []string{KindUnusableFile}) {
		t.Errorf("warnings %v", got)
	}
	// The view's order, after name; projections keep it.
	if got := keys(t, l.Raw[0]); !slices.Equal(got[:5], []string{"id", "session_id", "name", "job", "source"}) {
		t.Errorf("keys %v", got)
	}
	_, p := f.list(`{"fields":["source","job"]}`)
	if got := keys(t, p.Raw[0]); !slices.Equal(got, []string{"id", "session_id", "job", "source"}) {
		t.Errorf("projection keys %v", got)
	}
	for i, s := range p.Sessions {
		if got := [2]any{s["job"], s["source"]}; got != want[s["session_id"].(string)] {
			t.Errorf("projection %d: %v", i, got)
		}
	}

	// show reports the same.
	s := f.shown(uuidA)
	if s["job"] != "api-review" || s["source"] != "spawn" {
		t.Errorf("show: %v %v", s["job"], s["source"])
	}
	if s := f.shown(uuidC); s["job"] != nil || s["source"] != nil {
		t.Errorf("show without sesshin.json: %v %v", s["job"], s["source"])
	}
}

// Design-spec, Reservations, Revived by hand: among live sessions storing one
// job, the earliest last_start_at holds it, then the lower UUID; the others
// report null. A session whose liveness is unknown counts as live; an ended
// one reports what it stored.
func TestReportedJob(t *testing.T) {
	t.Run("the earliest start holds it", func(t *testing.T) {
		f := newPruneFixture(t)
		f.running(uuidB, time.Minute, 11, startedAgo(f, time.Hour)) // earliest, despite the higher UUID
		f.running(uuidA, time.Minute, 12, startedAgo(f, 30*time.Minute))
		f.running(uuidC, time.Minute, 13, startedAgo(f, 10*time.Minute))
		for i, id := range []string{uuidA, uuidB, uuidC} {
			f.sesshinWith(id, int64(i+1), "api", "hook")
		}
		_, l := f.list(`{}`)
		want := map[string]string{uuidA: "-", uuidB: "api", uuidC: "-"}
		if got := jobs(l); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("jobs %v, want %v", got, want)
		}
		// show derives it over the whole read: the loser reports null though
		// the holder isn't selected.
		if s := f.shown(uuidA); s["job"] != nil {
			t.Errorf("show of the loser: %v", s["job"])
		}
		if s := f.shown(uuidB); s["job"] != "api" {
			t.Errorf("show of the holder: %v", s["job"])
		}
	})
	t.Run("the lower UUID holds a tie", func(t *testing.T) {
		f := newPruneFixture(t)
		f.running(uuidB, time.Minute, 11, startedAgo(f, time.Hour))
		f.running(uuidA, time.Minute, 12, startedAgo(f, time.Hour))
		f.sesshinWith(uuidA, 1, "api", "hook")
		f.sesshinWith(uuidB, 2, "api", "hook")
		_, l := f.list(`{}`)
		if got, want := jobs(l), map[string]string{uuidA: "api", uuidB: "-"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("jobs %v, want %v", got, want)
		}
	})
	t.Run("another job is its own", func(t *testing.T) {
		f := newPruneFixture(t)
		f.running(uuidA, time.Minute, 11, startedAgo(f, time.Hour))
		f.running(uuidB, time.Minute, 12, startedAgo(f, time.Minute))
		f.running(uuidC, time.Minute, 13)
		f.sesshinWith(uuidA, 1, "api", "hook")
		f.sesshinWith(uuidB, 2, "web", "hook")
		f.sesshinWith(uuidC, 3, "", "hook")
		_, l := f.list(`{}`)
		if got, want := jobs(l), map[string]string{uuidA: "api", uuidB: "web", uuidC: "-"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("jobs %v, want %v", got, want)
		}
	})
	t.Run("an unknown session holds its job", func(t *testing.T) {
		f := newPruneFixture(t)
		// A's process can't be looked up: liveness unknown, and it started first.
		f.tableErr[11] = fmt.Errorf("unreadable process table")
		f.session(uuidA, time.Minute, notEnded, withPID(11, "s11"), startedAgo(f, time.Hour))
		f.running(uuidB, time.Minute, 12, startedAgo(f, 10*time.Minute))
		f.sesshinWith(uuidA, 1, "api", "hook")
		f.sesshinWith(uuidB, 2, "api", "hook")
		_, l := f.list(`{}`)
		if got, want := jobs(l), map[string]string{uuidA: "api", uuidB: "-"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("jobs %v, want %v", got, want)
		}
		for _, s := range l.Sessions {
			if want := map[string]string{uuidA: "unknown", uuidB: "live"}[s["session_id"].(string)]; s["liveness"] != want {
				t.Errorf("%s: liveness %v, want %s", s["session_id"], s["liveness"], want)
			}
		}
		// And the other way round: a live holder keeps it from an unknown one.
		f2 := newPruneFixture(t)
		f2.tableErr[11] = fmt.Errorf("unreadable process table")
		f2.session(uuidA, time.Minute, notEnded, withPID(11, "s11"), startedAgo(f2, 10*time.Minute))
		f2.running(uuidB, time.Minute, 12, startedAgo(f2, time.Hour))
		f2.sesshinWith(uuidA, 1, "api", "hook")
		f2.sesshinWith(uuidB, 2, "api", "hook")
		_, l = f2.list(`{}`)
		if got, want := jobs(l), map[string]string{uuidA: "-", uuidB: "api"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("reverse: jobs %v, want %v", got, want)
		}
	})
	t.Run("an ended session reports its own", func(t *testing.T) {
		f := newPruneFixture(t)
		f.running(uuidA, time.Minute, 11, startedAgo(f, 10*time.Minute))
		f.session(uuidB, time.Hour, startedAgo(f, 2*time.Hour)) // ended, started earlier
		f.session(uuidC, 2*time.Hour, startedAgo(f, 3*time.Hour))
		for i, id := range []string{uuidA, uuidB, uuidC} {
			f.sesshinWith(id, int64(i+1), "api", "hook")
		}
		_, l := f.list(`{"liveness":"all"}`)
		if got, want := jobs(l), map[string]string{uuidA: "api", uuidB: "api", uuidC: "api"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("jobs %v, want %v", got, want)
		}
		if s := f.shown(uuidB); s["job"] != "api" || s["liveness"] != "ended" {
			t.Errorf("show of the ended: %v %v", s["job"], s["liveness"])
		}
	})
	t.Run("a superseded session is ended", func(t *testing.T) {
		f := newPruneFixture(t)
		// Two sessions of one process: the later start is live, the other
		// superseded, and both hold a job as stored.
		f.table[50] = "s50"
		f.session(uuidA, 10*time.Minute, notEnded, withPID(50, "s50"), startedAgo(f, time.Hour))
		f.session(uuidB, time.Minute, notEnded, withPID(50, "s50"), startedAgo(f, time.Minute))
		f.sesshinWith(uuidA, 1, "api", "hook")
		f.sesshinWith(uuidB, 2, "api", "hook")
		_, l := f.list(`{"liveness":"all"}`)
		if got, want := jobs(l), map[string]string{uuidA: "api", uuidB: "api"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("jobs %v, want %v", got, want)
		}
	})
	t.Run("an unusable sesshin.json holds nothing", func(t *testing.T) {
		f := newPruneFixture(t)
		f.running(uuidA, time.Minute, 11, startedAgo(f, time.Hour))
		f.running(uuidB, time.Minute, 12, startedAgo(f, time.Minute))
		f.write(uuidA, "sesshin.json", []byte(`{"schema":1,"id":1,"placement":null,"job":"api"}`)) // no source
		f.sesshinWith(uuidB, 2, "api", "hook")
		_, l := f.list(`{}`)
		if got, want := jobs(l), map[string]string{uuidA: "-", uuidB: "api"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("jobs %v, want %v", got, want)
		}
	})
}

// The reported job is derived over every session read, before the narrowing:
// a holder the narrowing hides still takes the job from the others.
func TestReportedJobNarrowing(t *testing.T) {
	f := newPruneFixture(t)
	// A is headless, so list hides it by default; it started first.
	f.running(uuidA, time.Minute, 11, nested, startedAgo(f, time.Hour))
	f.running(uuidB, time.Minute, 12, startedAgo(f, time.Minute))
	f.running(uuidC, time.Minute, 13, startedAgo(f, time.Minute))
	f.sesshinWith(uuidA, 1, "api", "hook")
	f.sesshinWith(uuidB, 2, "api", "hook")
	f.sesshinWith(uuidC, 3, "web", "hook")
	_, l := f.list(`{}`)
	if got, want := jobs(l), map[string]string{uuidB: "-", uuidC: "web"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("hidden holder: jobs %v, want %v", got, want)
	}
	_, l = f.list(`{"include_headless":true}`)
	if got, want := jobs(l), map[string]string{uuidA: "api", uuidB: "-", uuidC: "web"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("shown holder: jobs %v, want %v", got, want)
	}
	// A projection that leaves job out changes nothing for the rest, and one
	// that keeps it reports the same.
	_, l = f.list(`{"fields":["job"]}`)
	if got, want := jobs(l), map[string]string{uuidB: "-", uuidC: "web"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("projection: jobs %v, want %v", got, want)
	}
}
