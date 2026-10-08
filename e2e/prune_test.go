package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/model"
)

const sid3 = "7d2e9f10-aaaa-4bbb-8ccc-ddddeeeeffff"

// eventFor is event for the session id.
func eventFor(id, name string, members ...string) string {
	return strings.Replace(event(name, members...), sid, id, 1)
}

// lifeSession starts and ends session id through the real hooks.
func lifeSession(t *testing.T, h *Harness, id string) {
	t.Helper()
	quiet(t, h.Hook("session-start", eventFor(id, "SessionStart", `"source":"startup"`)))
	quiet(t, h.Hook("session-end", eventFor(id, "SessionEnd", `"reason":"logout"`)))
}

var lastEvent = regexp.MustCompile(`("last_event_at"\s*:\s*")[^"]+(")`)

// age rewrites session id's last_event_at to ago before the real now. prune
// reads the real clock, which a test can't set, so the sessions are moved
// into the past instead: by days, against windows of 24 hours and 30 days,
// the margins are far wider than any delay in the test.
func age(t *testing.T, h *Harness, id string, ago time.Duration) {
	t.Helper()
	p := filepath.Join(h.Loc.StateDir, "sessions", id, "lifecycle.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	ts := string(model.FormatTimestamp(time.Now().Add(-ago)))
	b = lastEvent.ReplaceAll(b, []byte("${1}"+ts+"${2}"))
	if _, r := model.ReadLifecycle(b, id); !r.Usable {
		t.Fatalf("aged lifecycle.json unusable: %s", r.Reason())
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

type pruneResult struct {
	OK     bool `json:"ok"`
	Result struct {
		DryRun bool    `json:"dry_run"`
		Cutoff *string `json:"cutoff"`
		Pruned []struct {
			SessionID string `json:"session_id"`
			ID        *int64 `json:"id"`
			Headless  bool   `json:"headless"`
		} `json:"pruned"`
		KeptEnded           int `json:"kept_ended"`
		SkippedLocked       int `json:"skipped_locked"`
		ReservationsRemoved []struct {
			File      string  `json:"file"`
			Job       *string `json:"job"`
			CreatedAt *string `json:"created_at"`
			Reason    string  `json:"reason"`
		} `json:"reservations_removed"`
		ReservationsSkippedLocked bool `json:"reservations_skipped_locked"`
	} `json:"result"`
	Warnings []json.RawMessage `json:"warnings"`
}

func sesshinPrune(t *testing.T, h *Harness, args ...string) pruneResult {
	t.Helper()
	res := h.Sesshin(append([]string{"prune"}, args...)...)
	if res.Exit != 0 || res.Stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	var out pruneResult
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil || !out.OK || len(out.Warnings) != 0 {
		t.Fatalf("%v: %s", err, res.Stdout)
	}
	return out
}

func prunedIDs(r pruneResult) []string {
	ids := []string{}
	for _, p := range r.Result.Pruned {
		ids = append(ids, p.SessionID)
	}
	return ids
}

func sessionExists(h *Harness, id string) bool {
	_, err := os.Stat(filepath.Join(h.Loc.StateDir, "sessions", id))
	return err == nil
}

// Sessions made by the real hooks, then aged: a dry run reports and changes
// nothing; the run removes what the windows say; --retain-days narrows one.
func TestPrune(t *testing.T) {
	t.Parallel()
	h := New(t)
	lifeSession(t, h, sid) // sesshin ID 1: 40 days old
	lifeSession(t, h, sid2)
	h.Nested = true // started by another session: headless
	lifeSession(t, h, sid3)
	h.Nested = false
	age(t, h, sid, 40*24*time.Hour)
	age(t, h, sid3, 30*time.Hour)

	dry := sesshinPrune(t, h, "--dry-run")
	if !dry.Result.DryRun || dry.Result.Cutoff == nil || !slices.Equal(prunedIDs(dry), []string{sid, sid3}) || dry.Result.KeptEnded != 1 {
		t.Errorf("dry run: %+v", dry.Result)
	}
	if !sessionExists(h, sid) || !sessionExists(h, sid2) || !sessionExists(h, sid3) {
		t.Fatal("the dry run removed a session")
	}
	if p := dry.Result.Pruned; *p[0].ID != 1 || p[0].Headless || *p[1].ID != 3 || !p[1].Headless {
		t.Errorf("pruned %+v", p)
	}

	run := sesshinPrune(t, h)
	if run.Result.DryRun || !slices.Equal(prunedIDs(run), []string{sid, sid3}) || run.Result.KeptEnded != 1 || run.Result.SkippedLocked != 0 {
		t.Errorf("run: %+v", run.Result)
	}
	if sessionExists(h, sid) || sessionExists(h, sid3) || !sessionExists(h, sid2) {
		t.Error("the run left the wrong sessions")
	}
	left, _ := os.ReadDir(filepath.Join(h.Loc.StateDir, "sessions"))
	if len(left) != 1 || left[0].Name() != sid2 {
		t.Errorf("sessions/ holds %v", left)
	}

	// Within 30 days: kept, until the run narrows the window.
	age(t, h, sid2, 3*24*time.Hour)
	if r := sesshinPrune(t, h); len(r.Result.Pruned) != 0 || r.Result.KeptEnded != 1 {
		t.Errorf("rerun: %+v", r.Result)
	}
	narrow := sesshinPrune(t, h, "--retain-days", "2")
	if !slices.Equal(prunedIDs(narrow), []string{sid2}) || sessionExists(h, sid2) {
		t.Errorf("--retain-days 2: %+v", narrow.Result)
	}
	// The state directory's counter is untouched: IDs aren't reused.
	if _, err := os.Stat(filepath.Join(h.Loc.StateDir, "state.json")); err != nil {
		t.Errorf("state.json: %v", err)
	}
}

// Reservations are written by hand (nothing creates one yet): prune removes
// the stale and the unusable, keeps the fresh, and a dry run only says so.
func TestPruneReservations(t *testing.T) {
	t.Parallel()
	h := New(t)
	lifeSession(t, h, sid) // sessions/ exists, fresh
	dir := filepath.Join(h.Loc.StateDir, "reservations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const token = "3fa85f6457174562b3fc2c963f66afa6"
	reserve := func(job string, ago time.Duration, placement string) {
		t.Helper()
		jobText := "null"
		if job != "" {
			jobText = `"` + job + `"`
		}
		b := `{"schema":2,"job":` + jobText + `,"token":"` + token + `","created_at":"` +
			string(model.FormatTimestamp(time.Now().Add(-ago))) + `","placement":` + placement + `,"extra":{}}`
		name := model.ReservationName(job, token)
		if _, r := model.ReadReservation([]byte(b), name); !r.Usable {
			t.Fatalf("reservation unusable: %s", r.Reason())
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reserve("stranded", time.Hour, "null")
	reserve("expired", 48*time.Hour, "null")
	reserve("fresh", time.Second, "null")
	reserve("", time.Hour, "null") // no job: <token>.json
	// A launched reservation whose socket answers nothing is judged by age.
	reserve("launched", time.Hour, `{"terminal":"kitty","socket":"unix:`+filepath.Join(h.Loc.StateDir, "no-such-socket")+`","window_id":3}`)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600); err != nil { // named before tokens
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) pruneResult {
		t.Helper()
		res := h.Sesshin(append([]string{"prune"}, args...)...)
		var out pruneResult
		// The warnings are counted on stderr.
		if res.Exit != 0 || json.Unmarshal([]byte(res.Stdout), &out) != nil || !out.OK {
			t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
		}
		return out
	}
	jobs := func(r pruneResult) []string {
		out := []string{}
		for _, x := range r.Result.ReservationsRemoved {
			job := "-"
			if x.Job != nil {
				job = *x.Job
			}
			out = append(out, x.File+" "+job+":"+x.Reason)
		}
		return out
	}
	want := []string{
		"3fa85f6457174562b3fc2c963f66afa6.json -:stranded",
		"broken.json -:unusable",
		"expired_3fa85f6457174562b3fc2c963f66afa6.json expired:expired",
		"stranded_3fa85f6457174562b3fc2c963f66afa6.json stranded:stranded",
	}

	dry := run("--dry-run")
	if !slices.Equal(jobs(dry), want) || dry.Result.ReservationsSkippedLocked || len(dry.Warnings) != 1 {
		t.Errorf("dry run: %+v, %d warnings", dry.Result, len(dry.Warnings))
	}
	if left, _ := os.ReadDir(dir); len(left) != 7 {
		t.Fatalf("the dry run removed reservations: %v", left)
	}
	got := run()
	if !slices.Equal(jobs(got), want) || len(got.Warnings) != 1 || got.Result.ReservationsRemoved[0].CreatedAt == nil || got.Result.ReservationsRemoved[1].CreatedAt != nil {
		t.Errorf("run: %+v, %d warnings", got.Result, len(got.Warnings))
	}
	var names []string
	left, _ := os.ReadDir(dir)
	for _, e := range left {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"fresh_" + token + ".json", "launched_" + token + ".json", "notes.txt"}) {
		t.Errorf("reservations/ holds %v", names)
	}
	if again := run(); len(again.Result.ReservationsRemoved) != 0 || len(again.Warnings) != 0 {
		t.Errorf("rerun: %+v", again.Result)
	}
}

// The errors prune raises, as the binary prints them: the in-process tests
// cover liveness (the fake claude is gone by the time prune runs, so e2e has
// no live session to keep).
func TestPruneErrors(t *testing.T) {
	t.Parallel()
	h := New(t)
	for _, tc := range []struct {
		args []string
		kind string
	}{
		{[]string{"prune", "--retain-days", "0"}, `"kind":"invalid-input"`},
		{[]string{"prune", "--retain-days", "x"}, `"kind":"invalid-input"`},
	} {
		res := h.Sesshin(tc.args...)
		if res.Exit != 1 || !strings.Contains(res.Stdout, tc.kind) {
			t.Errorf("%v: exit %d, stdout %q", tc.args, res.Exit, res.Stdout)
		}
	}
	if err := os.MkdirAll(h.Loc.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Loc.ConfigDir, "config.toml"), []byte("retain_days = -1"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := h.Sesshin("prune")
	if res.Exit != 1 || !strings.Contains(res.Stdout, `"kind":"corrupt"`) {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	h.Unsetenv("HOME")
	res = h.Sesshin("prune")
	if res.Exit != 1 || !strings.Contains(res.Stdout, `"kind":"environment"`) {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
}
