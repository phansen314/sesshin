package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

var pruneNow = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// Session UUIDs, in the order of their names.
const (
	pidA = "00000000-0000-4000-8000-000000000001"
	pidB = "00000000-0000-4000-8000-000000000002"
	pidC = "00000000-0000-4000-8000-000000000003"
	pidD = "00000000-0000-4000-8000-000000000004"
	pidE = "00000000-0000-4000-8000-000000000005"
	pidF = "00000000-0000-4000-8000-000000000006"
)

// pruneFixture is a temp HOME with a fixed clock and a fake process table.
type pruneFixture struct {
	t     *testing.T
	home  string
	loc   loc.Locations
	now   time.Time
	table map[int64]string // pid -> pid_started_at; a missing pid has no process
	// tableErr, when set for a pid, makes its lookup fail as an unreadable
	// process table would.
	tableErr map[int64]error
	hook     fsys.Hook
	// windows answers the window question; nil gives no answer.
	windows func(placement *jsonio.Object) ([]int64, bool)
}

func newPruneFixture(t *testing.T) *pruneFixture {
	t.Helper()
	f := &pruneFixture{t: t, home: t.TempDir(), now: pruneNow, table: map[int64]string{}, tableErr: map[int64]error{}}
	l, err := loc.Resolve("linux", f.getenv)
	if err != nil {
		t.Fatal(err)
	}
	f.loc = l
	return f
}

func (f *pruneFixture) getenv(k string) string {
	if k == "HOME" {
		return f.home
	}
	return ""
}

func (f *pruneFixture) env() ReadEnv {
	var fsy fsys.FS = fsys.OS{}
	if f.hook != nil {
		fsy = fsys.Fault{FS: fsy, Hook: f.hook}
	}
	return ReadEnv{
		FS:      fsy,
		Getenv:  f.getenv,
		GOOS:    "linux",
		Now:     func() time.Time { return f.now },
		Windows: f.windows,
		StartedAt: func(pid int64) (string, error) {
			if err := f.tableErr[pid]; err != nil {
				return "", err
			}
			if s, ok := f.table[pid]; ok {
				return s, nil
			}
			return "", proc.ErrNoProcess
		},
	}
}

// run runs prune, validates the envelope, and returns it.
func (f *pruneFixture) run(in PruneInput) Envelope {
	f.t.Helper()
	env := Prune(in, f.env())
	checkEnvelope(f.t, env, "prune-output")
	return env
}

// output is the result of a prune that must have succeeded.
func (f *pruneFixture) output(in PruneInput) (PruneOutput, []Warning) {
	f.t.Helper()
	env := f.run(in)
	if !env.OK {
		f.t.Fatalf("prune failed: %+v", env.Error)
	}
	return env.Result.(PruneOutput), env.Warnings
}

func (f *pruneFixture) config(content string) {
	f.t.Helper()
	if err := os.MkdirAll(f.loc.ConfigDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.loc.ConfigDir, "config.toml"), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *pruneFixture) ago(d time.Duration) time.Time { return f.now.Add(-d) }

const day = 24 * time.Hour

// lifecycle is a usable, ended lifecycle.json seen at last_event_at.
func (f *pruneFixture) lifecycle(id string, seen time.Time, mod ...func(*model.LifecycleFile)) model.LifecycleFile {
	ts := model.FormatTimestamp(seen)
	l := model.LifecycleFile{
		SessionID: id, StartedAt: ts, LastStartAt: ts, Status: "idle",
		LastEventType: "stop", LastEventAt: ts, EventSeq: 1,
		EndedAt: &ts,
	}
	for _, m := range mod {
		m(&l)
	}
	return l
}

func (f *pruneFixture) write(id, name string, data []byte) {
	f.t.Helper()
	dir := f.loc.SessionDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// session writes a usable lifecycle.json, ended and last seen ago, and
// returns the id.
func (f *pruneFixture) session(id string, ago time.Duration, mod ...func(*model.LifecycleFile)) string {
	f.t.Helper()
	f.writeLifecycle(f.lifecycle(id, f.ago(ago), mod...))
	return id
}

func (f *pruneFixture) writeLifecycle(l model.LifecycleFile) {
	f.t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, r := model.ReadLifecycle(b, l.SessionID); !r.Usable {
		f.t.Fatalf("fixture lifecycle.json unusable: %s", r.Reason())
	}
	f.write(l.SessionID, "lifecycle.json", b)
}

func (f *pruneFixture) writeStatusline(id string, receivedAt time.Time, pid int64, started string) {
	f.t.Helper()
	pidJSON, startedJSON := "null", "null"
	if pid != 0 {
		pidJSON, startedJSON = fmt.Sprint(pid), `"`+started+`"`
	}
	b := fmt.Sprintf(`{"schema":1,"received_at":%q,"received_ns":1,"payload":{},"git_branch":null,"cost_sample":null,"burn_usd_per_hour":null,"pid":%s,"pid_started_at":%s}`,
		model.FormatTimestamp(receivedAt), pidJSON, startedJSON)
	if _, r := model.ReadStatusline([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture statusline.json unusable: %s", r.Reason())
	}
	f.write(id, "statusline.json", []byte(b))
}

func (f *pruneFixture) exists(id string) bool {
	_, err := os.Stat(f.loc.SessionDir(id))
	return err == nil
}

// entries lists everything under sessions/, recursively, for comparing the
// disk before and after.
func (f *pruneFixture) entries() []string {
	f.t.Helper()
	var out []string
	err := filepath.WalkDir(f.loc.SessionsDir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %v %d %d", p, info.Mode(), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func nested(l *model.LifecycleFile)   { t := true; l.Nested = &t }
func sdk(l *model.LifecycleFile)      { e := "sdk-cli"; l.Entrypoint = &e }
func notEnded(l *model.LifecycleFile) { l.EndedAt = nil }
func withPID(pid int64, started string) func(*model.LifecycleFile) {
	return func(l *model.LifecycleFile) { l.PID, l.PIDStartedAt = &pid, &started }
}

func ids(o PruneOutput) []string {
	out := []string{}
	for _, p := range o.Pruned {
		out = append(out, p.SessionID)
	}
	return out
}

func str(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

func i64(n int64) *int64 { return &n }

// The five sessions of the window cases: F and A are plain and 50 and 40
// days old, B plain and 10 days, C and D headless (nested; an SDK's
// entrypoint) and 30 hours, E headless and 2 hours. F is the oldest and has
// the highest name but one, so the order of pruned isn't the order of names.
func windowSessions(f *pruneFixture) {
	f.session(pidF, 50*day)
	f.session(pidA, 40*day)
	f.session(pidB, 10*day)
	f.session(pidC, 30*time.Hour, nested)
	f.session(pidD, 30*time.Hour, sdk)
	f.session(pidE, 2*time.Hour, nested)
}

func TestPruneWindows(t *testing.T) {
	retain5 := PruneInput{RetainDays: i64(5)}
	for _, tc := range []struct {
		name         string
		config       string
		in           PruneInput
		cutoff, hCut time.Duration // how far back; 0 for null
		want         []string
		kept         int
	}{
		{"defaults: both windows", "", PruneInput{}, 30 * day, 24 * time.Hour, []string{pidF, pidA, pidC, pidD}, 2},
		{"retain_days only", "retain_headless_hours = 0", PruneInput{}, 30 * day, 0, []string{pidF, pidA}, 4},
		{"headless only", "retain_days = 0", PruneInput{}, 0, 24 * time.Hour, []string{pidC, pidD}, 4},
		{"config 0 and explicit input", "retain_days = 0", retain5, 5 * day, 24 * time.Hour, []string{pidF, pidA, pidB, pidC, pidD}, 1},
		{"config 0 and explicit input, no headless window", "retain_days = 0\nretain_headless_hours = 0", retain5, 5 * day, 0, []string{pidF, pidA, pidB}, 3},
		{"both off", "retain_days = 0\nretain_headless_hours = 0", PruneInput{}, 0, 0, []string{}, 6},
		{"explicit input beats the config", "retain_days = 5", PruneInput{RetainDays: i64(20)}, 20 * day, 24 * time.Hour, []string{pidF, pidA, pidC, pidD}, 2},
		{"headless inside both windows", "retain_days = 1\nretain_headless_hours = 1000", PruneInput{}, day, 1000 * time.Hour, []string{pidF, pidA, pidB, pidC, pidD}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPruneFixture(t)
			if tc.config != "" {
				f.config(tc.config)
			}
			windowSessions(f)
			out, warnings := f.output(tc.in)
			if len(warnings) != 0 {
				t.Errorf("warnings %+v", warnings)
			}
			back := func(d time.Duration) string {
				if d == 0 {
					return "<null>"
				}
				return string(model.FormatTimestamp(f.now.Add(-d)))
			}
			if got, want := str(out.Cutoff), back(tc.cutoff); got != want {
				t.Errorf("cutoff %s, want %s", got, want)
			}
			if got, want := str(out.HeadlessCutoff), back(tc.hCut); got != want {
				t.Errorf("headless_cutoff %s, want %s", got, want)
			}
			if got := ids(out); !slices.Equal(got, tc.want) {
				t.Errorf("pruned %v, want %v", got, tc.want)
			}
			if out.KeptEnded != tc.kept || out.SkippedLocked != 0 {
				t.Errorf("kept_ended %d (want %d), skipped_locked %d", out.KeptEnded, tc.kept, out.SkippedLocked)
			}
			for _, id := range []string{pidA, pidB, pidC, pidD, pidE, pidF} {
				if want := !slices.Contains(tc.want, id); f.exists(id) != want {
					t.Errorf("%s exists %v, want %v", id, f.exists(id), want)
				}
			}
			// Nothing but the sessions' directories is left behind.
			left, _ := os.ReadDir(f.loc.SessionsDir())
			for _, e := range left {
				if strings.HasPrefix(e.Name(), ".") {
					t.Errorf("leftover %s", e.Name())
				}
			}
		})
	}
}

// pruned carries what the output says about each removed session.
func TestPrunedItems(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 40*day)
	f.write(pidA, "sesshin.json", []byte(`{"schema":1,"id":7,"job":null,"source":"hook","placement":null,"extra":{}}`))
	f.session(pidB, 40*day, nested)
	f.write(pidB, "sesshin.json", []byte(`{"schema":1,"id":null,"job":null,"source":"hook","placement":null,"extra":{}}`))
	f.session(pidC, 40*day, sdk) // no sesshin.json
	f.session(pidD, 40*day)
	f.write(pidD, "sesshin.json", []byte(`{"schema":2}`)) // unusable
	out, _ := f.output(PruneInput{})
	want := []PrunedItem{
		{pidA, i64(7), model.FormatTimestamp(f.ago(40 * day)), false},
		{pidB, nil, model.FormatTimestamp(f.ago(40 * day)), true},
		{pidC, nil, model.FormatTimestamp(f.ago(40 * day)), true},
		{pidD, nil, model.FormatTimestamp(f.ago(40 * day)), false},
	}
	if len(out.Pruned) != len(want) {
		t.Fatalf("pruned %+v", out.Pruned)
	}
	for i, w := range want {
		g := out.Pruned[i]
		if g.SessionID != w.SessionID || str(ptrStr(g.ID)) != str(ptrStr(w.ID)) || g.LastSeen != w.LastSeen || g.Headless != w.Headless {
			t.Errorf("pruned[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func ptrStr(n *int64) *string {
	if n == nil {
		return nil
	}
	s := fmt.Sprint(*n)
	return &s
}

func TestPruneLiveness(t *testing.T) {
	const started = "linux:boot:100"
	t.Run("live process is never pruned", func(t *testing.T) {
		f := newPruneFixture(t)
		f.table[100] = started
		f.session(pidA, 400*day, notEnded, withPID(100, started))
		out, _ := f.output(PruneInput{})
		if len(out.Pruned) != 0 || out.KeptEnded != 0 || !f.exists(pidA) {
			t.Errorf("%+v", out)
		}
	})
	t.Run("a process that is gone is ended", func(t *testing.T) {
		f := newPruneFixture(t)
		f.session(pidA, 40*day, notEnded, withPID(100, started))
		f.session(pidB, 10*day, notEnded, withPID(101, started))
		f.table[102] = "linux:boot:other" // reused pid: another start time
		f.session(pidC, 40*day, notEnded, withPID(102, started))
		out, _ := f.output(PruneInput{})
		if got := ids(out); !slices.Equal(got, []string{pidA, pidC}) || out.KeptEnded != 1 {
			t.Errorf("pruned %v, kept_ended %d", got, out.KeptEnded)
		}
	})
	t.Run("a lookup that failed is unknown, never pruned", func(t *testing.T) {
		f := newPruneFixture(t)
		f.tableErr[100] = errors.New("unreadable process table")
		f.session(pidA, 400*day, notEnded, withPID(100, started))
		out, _ := f.output(PruneInput{})
		if len(out.Pruned) != 0 || out.KeptEnded != 0 || !f.exists(pidA) {
			t.Errorf("%+v", out)
		}
	})
	t.Run("no pid inside the unknown pid limit is live", func(t *testing.T) {
		f := newPruneFixture(t)
		f.session(pidA, 20*time.Hour, notEnded, nested)
		out, _ := f.output(PruneInput{})
		if len(out.Pruned) != 0 || out.KeptEnded != 0 {
			t.Errorf("%+v", out)
		}
	})
	t.Run("no pid outside it is ended", func(t *testing.T) {
		f := newPruneFixture(t)
		f.session(pidA, 30*time.Hour, notEnded, nested)
		f.session(pidB, 30*time.Hour, notEnded)
		out, _ := f.output(PruneInput{})
		// B is ended too, but a plain session 30h old is inside retain_days.
		if got := ids(out); !slices.Equal(got, []string{pidA}) || out.KeptEnded != 1 {
			t.Errorf("pruned %v, kept_ended %d", got, out.KeptEnded)
		}
	})
	t.Run("a statusline's pid is the fallback, and its time is last seen", func(t *testing.T) {
		f := newPruneFixture(t)
		f.table[100] = started
		f.session(pidA, 400*day, notEnded) // no pid of its own
		f.writeStatusline(pidA, f.ago(time.Hour), 100, started)
		f.session(pidB, 40*day) // ended, but its statusline was received recently
		f.writeStatusline(pidB, f.ago(2*day), 0, "")
		out, _ := f.output(PruneInput{})
		if len(out.Pruned) != 0 || out.KeptEnded != 1 || !f.exists(pidA) || !f.exists(pidB) {
			t.Errorf("%+v", out)
		}
	})
	t.Run("an unusable statusline is as a missing one", func(t *testing.T) {
		f := newPruneFixture(t)
		f.session(pidA, 40*day)
		f.write(pidA, "statusline.json", []byte(`{"schema":1}`))
		out, warnings := f.output(PruneInput{})
		if got := ids(out); !slices.Equal(got, []string{pidA}) || len(warnings) != 0 {
			t.Errorf("pruned %v, warnings %+v", got, warnings)
		}
	})
}

// Rule 3: the session that started last in a process is its live one; the
// others are ended, and prunable.
func TestPruneSuperseded(t *testing.T) {
	const started = "linux:boot:100"
	f := newPruneFixture(t)
	f.table[100] = started
	f.session(pidA, 40*day, notEnded, withPID(100, started))
	f.session(pidB, day, notEnded, withPID(100, started))
	out, _ := f.output(PruneInput{})
	if got := ids(out); !slices.Equal(got, []string{pidA}) || f.exists(pidA) || !f.exists(pidB) {
		t.Errorf("pruned %v", got)
	}
}

func TestPruneUnusableClock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *pruneFixture)
	}{
		{"lifecycle in the future", func(f *pruneFixture) { f.session(pidB, -time.Hour) }},
		{"statusline in the future", func(f *pruneFixture) {
			f.session(pidB, 10*day)
			f.writeStatusline(pidB, f.now.Add(time.Hour), 0, "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPruneFixture(t)
			f.session(pidA, 400*day)
			tc.setup(f)
			before := f.entries()
			out, _ := f.output(PruneInput{})
			if out.Cutoff != nil || out.HeadlessCutoff != nil || len(out.Pruned) != 0 || out.KeptEnded != 0 || out.SkippedLocked != 0 {
				t.Errorf("%+v", out)
			}
			if !slices.Equal(before, f.entries()) {
				t.Error("the disk changed")
			}
		})
	}
	// A clock that is exactly the newest event is usable: now is not earlier.
	f := newPruneFixture(t)
	f.session(pidA, 400*day)
	f.session(pidB, 0)
	out, _ := f.output(PruneInput{})
	if out.Cutoff == nil || !slices.Equal(ids(out), []string{pidA}) {
		t.Errorf("%+v", out)
	}
}

func TestPruneUnusableLifecycle(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 400*day)
	f.write(pidB, "lifecycle.json", []byte(`{"schema":1`)) // not JSON
	f.write(pidC, "sesshin.json", []byte(`{"schema":1,"id":3,"job":null,"source":"hook","placement":null,"extra":{}}`))
	f.write(pidD, "lifecycle.json", []byte(`{"schema":2}`))
	// A lifecycle.json whose session_id isn't its directory's.
	wrong, _ := json.Marshal(f.lifecycle(pidA, f.ago(400*day)))
	f.write(pidE, "lifecycle.json", wrong)
	// A directory in its place.
	if err := os.MkdirAll(filepath.Join(f.loc.SessionDir(pidF), "lifecycle.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	out, warnings := f.output(PruneInput{})
	if got := ids(out); !slices.Equal(got, []string{pidA}) || out.KeptEnded != 0 {
		t.Errorf("pruned %v, kept_ended %d", got, out.KeptEnded)
	}
	var paths []string
	for _, w := range warnings {
		if w.Kind != "unusable-file" {
			t.Errorf("warning %+v", w)
		}
		paths = append(paths, w.Details["path"].(string))
	}
	var want []string
	for _, id := range []string{pidB, pidC, pidD, pidE, pidF} {
		want = append(want, filepath.Join(f.loc.SessionDir(id), "lifecycle.json"))
	}
	if !slices.Equal(paths, want) {
		t.Errorf("warning paths %v, want %v", paths, want)
	}
	for _, id := range []string{pidB, pidC, pidD, pidE, pidF} {
		if !f.exists(id) {
			t.Errorf("%s was removed", id)
		}
	}
}

func TestPruneIgnoresEntries(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 400*day)
	sessions := f.loc.SessionsDir()
	for _, name := range []string{".sesshin-tmp-0123", ".00000000-0000-4000-8000-0000000000ff", "not-a-uuid", "0000000A-0000-4000-8000-00000000000A"} {
		if err := os.MkdirAll(filepath.Join(sessions, name), 0o700); err != nil {
			t.Fatal(err)
		}
		// Each looks like an old session inside, so only its name keeps it.
		b, _ := json.Marshal(f.lifecycle(strings.ToLower(pidB), f.ago(400*day)))
		if err := os.WriteFile(filepath.Join(sessions, name, "lifecycle.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A file, and a symlink to a directory, with a session's name.
	if err := os.WriteFile(filepath.Join(sessions, pidC), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sessions, "not-a-uuid"), filepath.Join(sessions, pidD)); err != nil {
		t.Fatal(err)
	}
	out, warnings := f.output(PruneInput{})
	if got := ids(out); !slices.Equal(got, []string{pidA}) || len(warnings) != 0 || out.KeptEnded != 0 {
		t.Errorf("pruned %v, warnings %+v, kept_ended %d", got, warnings, out.KeptEnded)
	}
	left, _ := os.ReadDir(sessions)
	if len(left) != 6 {
		t.Errorf("%d entries left, want 6", len(left))
	}
}

func TestPruneLockedSession(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry_run=%v", dry), func(t *testing.T) {
			f := newPruneFixture(t)
			f.session(pidA, 40*day)
			f.session(pidB, 41*day)
			// Hold A's lock through fsys, as a hook would.
			root, err := fsys.OS{}.OpenRoot(f.loc.SessionDir(pidA))
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			lock, err := root.Lock(0)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := f.output(PruneInput{DryRun: dry})
			if got := ids(out); !slices.Equal(got, []string{pidB}) || out.SkippedLocked != 1 || out.KeptEnded != 0 {
				t.Errorf("pruned %v, skipped_locked %d, kept_ended %d", got, out.SkippedLocked, out.KeptEnded)
			}
			if !f.exists(pidA) {
				t.Error("the locked session was removed")
			}
			// The lock is the session's own: prune released the one it took on B.
			lock.Unlock()
			out, _ = f.output(PruneInput{DryRun: dry})
			wantIDs := []string{pidA}
			if dry {
				wantIDs = []string{pidB, pidA}
			}
			if got := ids(out); !slices.Equal(got, wantIDs) || out.SkippedLocked != 0 {
				t.Errorf("after unlock: pruned %v, skipped_locked %d", got, out.SkippedLocked)
			}
		})
	}
}

// A session revived between the scan and the lock is judged again under it.
func TestPruneRejudgesUnderLock(t *testing.T) {
	const started = "linux:boot:100"
	f := newPruneFixture(t)
	f.table[100] = started
	revive := f.session(pidA, 40*day)    // SessionStart revives it: live
	refresh := f.session(pidB, 40*day)   // an event keeps it ended, but recent
	untouched := f.session(pidC, 40*day) // nothing happens
	broken := f.session(pidD, 40*day)    // its lifecycle.json is replaced by garbage
	f.hook = func(op fsys.Op) error {
		if op.Name != fsys.OpLock {
			return nil
		}
		switch filepath.Base(op.Root) {
		case revive:
			f.writeLifecycle(f.lifecycle(revive, f.now, notEnded, withPID(100, started)))
		case refresh:
			f.writeLifecycle(f.lifecycle(refresh, f.ago(time.Hour)))
		case broken:
			f.write(broken, "lifecycle.json", []byte("{"))
		}
		return nil
	}
	out, warnings := f.output(PruneInput{})
	if got := ids(out); !slices.Equal(got, []string{untouched}) {
		t.Errorf("pruned %v", got)
	}
	if out.KeptEnded != 1 { // refresh only: revive is live, broken unknown
		t.Errorf("kept_ended %d", out.KeptEnded)
	}
	if len(warnings) != 1 || warnings[0].Details["path"] != filepath.Join(f.loc.SessionDir(broken), "lifecycle.json") {
		t.Errorf("warnings %+v", warnings)
	}
	for _, id := range []string{revive, refresh, broken} {
		if !f.exists(id) {
			t.Errorf("%s was removed", id)
		}
	}
	if f.exists(untouched) {
		t.Error("the untouched session is still there")
	}
}

func TestPruneDryRun(t *testing.T) {
	f := newPruneFixture(t)
	windowSessions(f)
	f.write(pidA, "sesshin.json", []byte(`{"schema":1,"id":4,"job":null,"source":"hook","placement":null,"extra":{}}`))
	before := f.entries()
	dry, _ := f.output(PruneInput{DryRun: true})
	if !dry.DryRun || !slices.Equal(before, f.entries()) {
		t.Errorf("dry_run %v, or the disk changed", dry.DryRun)
	}
	real, _ := f.output(PruneInput{})
	dry.DryRun = false
	a, _ := json.Marshal(dry)
	b, _ := json.Marshal(real)
	if string(a) != string(b) {
		t.Errorf("dry run said %s, the run %s", a, b)
	}
	if f.exists(pidA) {
		t.Error("the run kept A")
	}
}

func TestPruneRenameFails(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 50*day)
	f.session(pidB, 45*day)
	f.session(pidC, 40*day)
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpRename && op.Path == pidB {
			return syscall.EACCES
		}
		return nil
	}
	env := f.run(PruneInput{})
	if env.OK || env.Error.Kind != KindIO || env.Error.Details["path"] != f.loc.SessionDir(pidB) || env.Error.Details["code"] != "EACCES" {
		t.Fatalf("%+v", env)
	}
	if f.exists(pidA) || !f.exists(pidB) || !f.exists(pidC) {
		t.Errorf("A %v, B %v, C %v", f.exists(pidA), f.exists(pidB), f.exists(pidC))
	}
	// Retry-safe: the rerun judges what is left; A is gone and not listed.
	f.hook = nil
	out, _ := f.output(PruneInput{})
	if got := ids(out); !slices.Equal(got, []string{pidB, pidC}) {
		t.Errorf("rerun pruned %v", got)
	}
}

func TestPruneErrors(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		f := newPruneFixture(t)
		f.home = ""
		env := f.run(PruneInput{})
		if env.OK || env.Error.Kind != "environment" || env.Error.Details["variable"] != "HOME" {
			t.Errorf("%+v", env)
		}
	})
	t.Run("corrupt config, before the state directory is looked at", func(t *testing.T) {
		f := newPruneFixture(t)
		f.config("retain_days = -1")
		env := f.run(PruneInput{})
		path := filepath.Join(f.loc.ConfigDir, "config.toml")
		if env.OK || env.Error.Kind != "corrupt" || env.Error.Details["path"] != path || env.Error.Details["detail"] == "" {
			t.Errorf("%+v", env)
		}
	})
	t.Run("io on the config", func(t *testing.T) {
		f := newPruneFixture(t)
		if err := os.MkdirAll(filepath.Join(f.loc.ConfigDir, "config.toml"), 0o700); err != nil {
			t.Fatal(err)
		}
		env := f.run(PruneInput{})
		if env.OK || env.Error.Kind != KindIO || env.Error.Details["code"] != "EISDIR" {
			t.Errorf("%+v", env)
		}
	})
	t.Run("io on sessions", func(t *testing.T) {
		f := newPruneFixture(t)
		if err := os.MkdirAll(f.loc.StateDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.loc.SessionsDir(), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		env := f.run(PruneInput{})
		if env.OK || env.Error.Kind != KindIO || env.Error.Details["code"] != "ENOTDIR" || env.Error.Details["path"] != f.loc.SessionsDir() {
			t.Errorf("%+v", env)
		}
	})
	t.Run("invalid input comes before everything", func(t *testing.T) {
		for _, in := range []string{`{"retain_days":0}`, `{"retain_days":"7"}`, `{"retain_days":1.5}`, `{"retain_days":-3}`, `{"dry_run":"yes"}`, `{"x":1}`} {
			_, e := DecodeInput([]byte(in), DecodePruneInput)
			if e == nil || e.Kind != KindInvalidInput {
				t.Errorf("%s: %+v", in, e)
			}
		}
		got, e := DecodeInput([]byte(`{"dry_run":true,"retain_days":9223372036854775807}`), DecodePruneInput)
		if e != nil || !got.DryRun || *got.RetainDays != math.MaxInt64 {
			t.Errorf("%+v %+v", got, e)
		}
		if got, e := DecodeInput([]byte(`{}`), DecodePruneInput); e != nil || got.DryRun || got.RetainDays != nil {
			t.Errorf("%+v %+v", got, e)
		}
	})
}

func TestPruneMissingDirectories(t *testing.T) {
	f := newPruneFixture(t)
	out, warnings := f.output(PruneInput{})
	if len(out.Pruned) != 0 || out.KeptEnded != 0 || len(warnings) != 0 || out.Cutoff == nil {
		t.Errorf("%+v", out)
	}
	if _, err := os.Stat(f.loc.StateDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the state directory was created: %v", err)
	}
	if err := os.MkdirAll(f.loc.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	f.output(PruneInput{})
	if _, err := os.Stat(f.loc.SessionsDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("sessions/ was created: %v", err)
	}
	if _, err := os.Stat(f.loc.ReservationsDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reservations/ was created: %v", err)
	}
	// An empty sessions/ is as good.
	if err := os.MkdirAll(f.loc.SessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, _ := f.output(PruneInput{}); len(out.Pruned) != 0 {
		t.Errorf("%+v", out)
	}
}

// state.json is never read or written: a corrupt one changes nothing.
func TestPruneLeavesState(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 40*day)
	state := filepath.Join(f.loc.StateDir, "state.json")
	if err := os.WriteFile(state, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, warnings := f.output(PruneInput{}); len(out.Pruned) != 1 || len(warnings) != 0 {
		t.Errorf("%+v %+v", out, warnings)
	}
	if b, _ := os.ReadFile(state); string(b) != "not json" {
		t.Errorf("state.json is %q", b)
	}
}

// The largest values of every setting saturate rather than wrap into a
// cutoff in the future (implementation-spec.md, Configuration).
func TestPruneSaturates(t *testing.T) {
	f := newPruneFixture(t)
	f.config("retain_days = 9223372036854775807\nretain_headless_hours = 9223372036854775807")
	windowSessions(f)
	out, _ := f.output(PruneInput{})
	if len(out.Pruned) != 0 || out.Cutoff == nil || out.HeadlessCutoff == nil {
		t.Errorf("%+v", out)
	}
	out, _ = f.output(PruneInput{RetainDays: i64(math.MaxInt64)})
	if len(out.Pruned) != 0 {
		t.Errorf("explicit: %+v", out)
	}
}
