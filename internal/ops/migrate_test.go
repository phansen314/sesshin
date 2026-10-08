package ops

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

const fixture001 = "../migrate/testdata/001-extra"

const (
	mSess1 = "11111111-1111-4111-8111-111111111111"
	mSess2 = "22222222-2222-4222-8222-222222222222"
	mSess3 = "33333333-3333-4333-8333-333333333333"
	mSess4 = "44444444-4444-4444-8444-444444444444"
	mSess5 = "55555555-5555-4555-8555-555555555555"
	mSess6 = "66666666-6666-4666-8666-666666666666"
	mSess7 = "77777777-7777-4777-8777-777777777777"
)

// migratedState is state.json after a migrate of the 001-extra fixture.
const migratedState = "{\n  \"schema\": 2,\n  \"last_id\": 41,\n  \"migration\": 1\n}\n"

func copyTo(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// migrateFixture is a pruneFixture whose state directory is the 001-extra
// fixture's before/ tree.
func migrateFixture(t *testing.T) *pruneFixture {
	t.Helper()
	f := newPruneFixture(t)
	copyTo(t, filepath.Join(fixture001, "before"), f.loc.StateDir)
	return f
}

func (f *pruneFixture) statePath() string { return filepath.Join(f.loc.StateDir, "state.json") }

func (f *pruneFixture) readState() string {
	f.t.Helper()
	b, err := os.ReadFile(f.statePath())
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

func (f *pruneFixture) migrate(dry bool) Envelope {
	f.t.Helper()
	env := Migrate(MigrateInput{DryRun: dry}, f.env())
	checkEnvelope(f.t, env, "migrate-output")
	return env
}

func (f *pruneFixture) migrated(dry bool) (MigrateOutput, []Warning) {
	f.t.Helper()
	env := f.migrate(dry)
	if !env.OK {
		f.t.Fatalf("migrate failed: %+v", env.Error)
	}
	return env.Result.(MigrateOutput), env.Warnings
}

// tree is the files under the state directory, without the temp files a
// failed write may leave (hidden, and ignored by reads).
func (f *pruneFixture) tree() map[string]string {
	f.t.Helper()
	m := snapshot(f.t, f.loc.StateDir)
	for p := range m {
		if strings.HasPrefix(filepath.Base(p), fsys.TempPrefix) {
			delete(m, p)
		}
	}
	return m
}

// afterTree is the fixture's after/ tree with state.json as migrate writes it.
func afterTree(t *testing.T) (map[string]string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	copyTo(t, filepath.Join(fixture001, "after"), dir)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(migratedState), 0o600); err != nil {
		t.Fatal(err)
	}
	return snapshot(t, dir), dir
}

// relTree is a snapshot with every path relative to root.
func relTree(m map[string]string, root string) map[string]string {
	out := map[string]string{}
	for p, v := range m {
		r, _ := filepath.Rel(root, p)
		out[r] = v
	}
	return out
}

// wantTree compares the state directory with want, a snapshot of afterTree's
// directory.
func (f *pruneFixture) wantTree(want map[string]string, wantRoot string) {
	f.t.Helper()
	g, w := relTree(f.tree(), f.loc.StateDir), relTree(want, wantRoot)
	for p, v := range w {
		if gv, ok := g[p]; !ok {
			f.t.Errorf("%s missing", p)
		} else if gv != v {
			f.t.Errorf("%s:\n got %q\nwant %q", p, gv, v)
		}
	}
	for p := range g {
		if _, ok := w[p]; !ok {
			f.t.Errorf("%s unexpected", p)
		}
	}
}

func TestMigrateFixture(t *testing.T) {
	f := migrateFixture(t)
	out, warns := f.migrated(false)
	if out.DryRun || out.From != 0 || out.To != 1 || len(out.Applied) != 1 || out.Applied[0] != (AppliedStep{1, "extra"}) {
		t.Errorf("output %+v", out)
	}
	wantChanged := []struct {
		id string
		n  *int64
	}{{mSess1, ptrTo(int64(12))}, {mSess2, ptrTo(int64(13))}, {mSess3, nil}}
	if len(out.Changed) != 3 {
		t.Fatalf("changed %+v", out.Changed)
	}
	for i, w := range wantChanged {
		c := out.Changed[i]
		if c.SessionID != w.id || (c.ID == nil) != (w.n == nil) || (w.n != nil && *c.ID != *w.n) ||
			!slices.Equal(c.Files, []string{"sesshin.json"}) {
			t.Errorf("changed[%d] = %+v", i, c)
		}
	}
	p6 := filepath.Join(f.loc.SessionDir(mSess6), "sesshin.json")
	if len(out.Unconverted) != 1 || out.Unconverted[0].Path != p6 || !strings.Contains(out.Unconverted[0].Detail, "cwd") {
		t.Errorf("unconverted %+v", out.Unconverted)
	}
	// The unconverted file and the newer one warn; the corrupt one is silent.
	if len(warns) != 2 {
		t.Fatalf("warnings %+v", warns)
	}
	for i, p := range []string{p6, filepath.Join(f.loc.SessionDir(mSess7), "sesshin.json")} {
		w := warns[i]
		if w.Kind != KindUnusableFile || w.Details["path"] != p || w.Details["reason"] != "unsupported-format" {
			t.Errorf("warning %d: %+v", i, w)
		}
	}
	f.wantTree(afterTree(t))

	// Idempotent: nothing is pending, so nothing is read or written.
	out, warns = f.migrated(false)
	if out.From != 1 || out.To != 1 || len(out.Applied) != 0 || len(out.Changed) != 0 || len(out.Unconverted) != 0 || len(warns) != 0 {
		t.Errorf("second run: %+v %+v", out, warns)
	}
	f.wantTree(afterTree(t))
}

func TestMigrateDryRun(t *testing.T) {
	f := migrateFixture(t)
	before := f.tree()
	dry, _ := f.migrated(true)
	if !dry.DryRun || len(dry.Changed) != 3 || len(dry.Unconverted) != 1 || dry.From != 0 || dry.To != 1 {
		t.Errorf("dry run: %+v", dry)
	}
	if got := f.tree(); !mapsEqual(before, got) {
		t.Error("dry run wrote")
	}
	real, _ := f.migrated(false)
	if !changedEqual(dry.Changed, real.Changed) {
		t.Errorf("dry run said %+v, the run did %+v", dry.Changed, real.Changed)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func changedEqual(a, b []ChangedSession) bool {
	return slices.EqualFunc(a, b, func(x, y ChangedSession) bool {
		return x.SessionID == y.SessionID && slices.Equal(x.Files, y.Files) && (x.ID == nil) == (y.ID == nil) && (x.ID == nil || *x.ID == *y.ID)
	})
}

// sesshinJSON is a schema 2 sesshin.json with the id.
func sesshinJSON(id string) string {
	return "{\n  \"schema\": 2,\n  \"id\": " + id + ",\n  \"job\": null,\n  \"source\": \"hook\",\n  \"placement\": null,\n  \"extra\": {}\n}\n"
}

func TestMigrateStep1(t *testing.T) {
	const s1 = `{"schema":1,"last_id":9}`
	cases := []struct {
		name     string
		state    *string // nil: missing
		sessions bool    // a current-format session with id 5
		from     int64
		written  string // state.json afterwards, "" for as it was
		errKind  string
		details  map[string]any
	}{
		{"schema 1", ptrTo(s1), true, 0, "{\n  \"schema\": 2,\n  \"last_id\": 9,\n  \"migration\": 1\n}\n", "", nil},
		{"schema 1 without sessions/", ptrTo(s1), false, 0, "{\n  \"schema\": 2,\n  \"last_id\": 9,\n  \"migration\": 1\n}\n", "", nil},
		{"recorded 0", ptrTo(`{"schema":2,"last_id":9,"migration":0}`), true, 0, "{\n  \"schema\": 2,\n  \"last_id\": 9,\n  \"migration\": 1\n}\n", "", nil},
		{"current", ptrTo(`{"schema":2,"last_id":9,"migration":1}`), true, 1, "", "", nil},
		{"missing with a session", nil, true, 0, "{\n  \"schema\": 2,\n  \"last_id\": 5,\n  \"migration\": 1\n}\n", "", nil},
		{"corrupt with a session", ptrTo(`nope`), true, 0, "{\n  \"schema\": 2,\n  \"last_id\": 5,\n  \"migration\": 1\n}\n", "", nil},
		{"missing without sessions", nil, false, 1, "", "", nil},
		{"corrupt without sessions", ptrTo(`nope`), false, 1, "", "", nil},
		{"newer schema", ptrTo(`{"schema":3,"last_id":9,"migration":1}`), true, 0, "", KindUnsupportedFormat,
			map[string]any{"field": "schema", "found": int64(3), "supported": int64(2)}},
		{"past the latest", ptrTo(`{"schema":2,"last_id":9,"migration":2}`), true, 0, "", KindUnsupportedFormat,
			map[string]any{"field": "migration", "found": int64(2), "supported": int64(1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newPruneFixture(t)
			if err := os.MkdirAll(f.loc.StateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if c.state != nil {
				if err := os.WriteFile(f.statePath(), []byte(*c.state), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if c.sessions {
				f.write(pidA, "sesshin.json", []byte(sesshinJSON("5")))
				// An older file, which an unsupported-format failure must leave.
				f.write(pidB, "sesshin.json", []byte(`{"schema":1,"id":3,"job":null,"source":"hook","placement":null}`))
			}
			before := f.tree()
			env := f.migrate(false)
			if c.errKind != "" {
				if env.OK || env.Error.Kind != c.errKind {
					t.Fatalf("got %+v", env)
				}
				for k, v := range c.details {
					if env.Error.Details[k] != v {
						t.Errorf("details[%s] = %v (%T), want %v", k, env.Error.Details[k], env.Error.Details[k], v)
					}
				}
				if env.Error.Details["path"] != f.statePath() {
					t.Errorf("path %v", env.Error.Details["path"])
				}
				if !mapsEqual(before, f.tree()) {
					t.Error("a failed run wrote")
				}
				return
			}
			if !env.OK {
				t.Fatalf("failed: %+v", env.Error)
			}
			out := env.Result.(MigrateOutput)
			if out.From != c.from || out.To != 1 {
				t.Errorf("from %d to %d, want %d", out.From, out.To, c.from)
			}
			if c.from == 1 && len(out.Applied) != 0 {
				t.Errorf("applied %+v", out.Applied)
			}
			if c.written == "" {
				if !mapsEqual(before, f.tree()) {
					t.Error("wrote when nothing was pending")
				}
			} else if got := f.readState(); got != c.written {
				t.Errorf("state.json %q, want %q", got, c.written)
			}
		})
	}
}

func TestMigrateStateUnreadable(t *testing.T) {
	f := newPruneFixture(t)
	if err := os.MkdirAll(f.statePath(), 0o700); err != nil { // a directory: EISDIR
		t.Fatal(err)
	}
	env := f.migrate(false)
	if env.OK || env.Error.Kind != KindIO || env.Error.Details["code"] != "EISDIR" {
		t.Fatalf("got %+v", env)
	}
}

func TestMigrateSessionFileUnreadable(t *testing.T) {
	f := migrateFixture(t)
	p := filepath.Join(f.loc.SessionDir(mSess3), "sesshin.json")
	os.Remove(p)
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	env := f.migrate(false)
	if env.OK || env.Error.Kind != KindIO || env.Error.Details["path"] != p {
		t.Fatalf("got %+v", env)
	}
	if f.readState() != "{\n  \"schema\": 1,\n  \"last_id\": 41\n}\n" {
		t.Error("state.json changed")
	}
}

// holdLock holds the flock of dir (a session or sessions/) until the test ends.
func holdLock(t *testing.T, dir string) {
	t.Helper()
	root, err := fsys.OS{}.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Unlock(); root.Close() })
}

func shortWait(t *testing.T) {
	old := migrateLockWait
	migrateLockWait = 50 * time.Millisecond
	t.Cleanup(func() { migrateLockWait = old })
}

func TestMigrateBusySession(t *testing.T) {
	shortWait(t)
	for _, dry := range []bool{false, true} {
		f := migrateFixture(t)
		holdLock(t, f.loc.SessionDir(mSess3))
		env := f.migrate(dry)
		if env.OK || env.Error.Kind != KindBusy || env.Error.Details["lock"] != "session" || env.Error.Details["session_id"] != mSess3 {
			t.Fatalf("dry %v: got %+v", dry, env)
		}
		if f.readState() != "{\n  \"schema\": 1,\n  \"last_id\": 41\n}\n" {
			t.Error("state.json changed")
		}
		// Sessions before it were converted (not with dry_run); the rest were not.
		b, _ := os.ReadFile(filepath.Join(f.loc.SessionDir(mSess1), "sesshin.json"))
		converted := strings.Contains(string(b), `"extra"`)
		if converted == dry {
			t.Errorf("dry %v: first session converted = %v", dry, converted)
		}
		b, _ = os.ReadFile(filepath.Join(f.loc.SessionDir(mSess3), "sesshin.json"))
		if strings.Contains(string(b), `"extra"`) {
			t.Error("the locked session was converted")
		}
	}
}

func TestMigrateBusyState(t *testing.T) {
	shortWait(t)
	for _, dry := range []bool{false, true} {
		f := migrateFixture(t)
		holdLock(t, f.loc.SessionsDir())
		env := f.migrate(dry)
		if env.OK || env.Error.Kind != KindBusy || env.Error.Details["lock"] != "state" {
			t.Fatalf("dry %v: got %+v", dry, env)
		}
		if f.readState() != "{\n  \"schema\": 1,\n  \"last_id\": 41\n}\n" {
			t.Error("state.json changed")
		}
	}
	// The sessions were converted by the failed run: a rerun finishes.
	f := migrateFixture(t)
	lockDir := f.loc.SessionsDir()
	root, _ := fsys.OS{}.OpenRoot(lockDir)
	lock, _ := root.Lock(0)
	if env := f.migrate(false); env.OK {
		t.Fatal("expected busy")
	}
	lock.Unlock()
	root.Close()
	out, _ := f.migrated(false)
	if out.From != 0 || len(out.Changed) != 0 {
		// The sessions are current now: only state.json is left.
		t.Errorf("rerun %+v", out)
	}
	if f.readState() != migratedState {
		t.Errorf("state.json %q", f.readState())
	}
}

// A session pruned between the scan and its lock is skipped.
func TestMigrateSkipsPrunedSession(t *testing.T) {
	f := migrateFixture(t)
	moved := false
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpLock && strings.HasSuffix(op.Root, mSess2) && !moved {
			moved = true
			return os.Rename(f.loc.SessionDir(mSess2), filepath.Join(f.loc.SessionsDir(), ".pruned"))
		}
		return nil
	}
	out, _ := f.migrated(false)
	if !moved {
		t.Fatal("the hook never ran")
	}
	var ids []string
	for _, c := range out.Changed {
		ids = append(ids, c.SessionID)
	}
	if !slices.Equal(ids, []string{mSess1, mSess3}) {
		t.Errorf("changed %v", ids)
	}
	b, _ := os.ReadFile(filepath.Join(f.loc.SessionsDir(), ".pruned", "sesshin.json"))
	if strings.Contains(string(b), `"extra"`) {
		t.Error("the pruned session was written")
	}
}

// A run ended after any of its writes is finished by a clean rerun, which
// reaches the same tree.
func TestMigrateCrashSafety(t *testing.T) {
	// Count the writes of a clean run: k renames.
	f := migrateFixture(t)
	n := 0
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpRename {
			n++
		}
		return nil
	}
	f.migrated(false)
	wantRel := relTree(f.tree(), f.loc.StateDir)
	if n != 4 {
		t.Fatalf("a clean run made %d writes, want 4", n)
	}
	for k := 0; k <= n; k++ {
		f := migrateFixture(t)
		seen := 0
		f.hook = func(op fsys.Op) error {
			if op.Mutating && op.Name != fsys.OpRemove && op.Name != fsys.OpRemoveAll {
				if seen >= k {
					return syscall.EIO
				}
			}
			if op.Name == fsys.OpRename {
				seen++
			}
			return nil
		}
		env := f.migrate(false)
		if k < n && env.OK {
			t.Errorf("k=%d: the run was not ended", k)
		}
		f.hook = nil
		out, _ := f.migrated(false)
		if k == n && out.From != 1 {
			t.Errorf("k=%d: rerun from %d", k, out.From)
		}
		if g := relTree(f.tree(), f.loc.StateDir); !mapsEqual(g, wantRel) {
			t.Errorf("k=%d: tree differs from a clean run's", k)
		}
	}
}

// A schema-1 state.json the step can't convert is listed in unconverted and
// left as it is, with its migration held; a dry run reports it the same, with
// or without sessions/.
func TestMigrateStateUnconverted(t *testing.T) {
	const bad = "{\"schema\": 1}\n" // no last_id
	for _, dry := range []bool{false, true} {
		for _, sessions := range []bool{true, false} {
			f := newPruneFixture(t)
			if sessions {
				copyTo(t, filepath.Join(fixture001, "before"), f.loc.StateDir)
			} else if err := os.MkdirAll(f.loc.StateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.statePath(), []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			out, _ := f.migrated(dry)
			var listed bool
			for _, u := range out.Unconverted {
				listed = listed || u.Path == f.statePath()
			}
			if !listed {
				t.Errorf("dry %v, sessions %v: unconverted %+v", dry, sessions, out.Unconverted)
			}
			if f.readState() != bad {
				t.Errorf("dry %v, sessions %v: state.json %q", dry, sessions, f.readState())
			}
		}
	}
}

// changeStateAtStep3 runs migrate over the fixture with state.json replaced by
// content just before step 3 reads it.
func changeStateAtStep3(t *testing.T, content string) (*pruneFixture, Envelope) {
	f := migrateFixture(t)
	reads := 0
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpReadFile && strings.HasSuffix(op.Path, "state.json") {
			if reads++; reads == 2 {
				return os.WriteFile(f.statePath(), []byte(content), 0o600)
			}
		}
		return nil
	}
	env := f.migrate(false)
	if reads < 2 {
		t.Fatalf("state.json read %d times", reads)
	}
	return f, env
}

// Another migrate finished between steps 1 and 3: nothing is left to do.
func TestMigrateStateFinishedByAnother(t *testing.T) {
	f, env := changeStateAtStep3(t, migratedState)
	if !env.OK {
		t.Fatalf("got %+v", env.Error)
	}
	if f.readState() != migratedState {
		t.Errorf("state.json %q", f.readState())
	}
}

// A state.json that a newer binary wrote in the meantime is not touched.
func TestMigrateStateNewerAtStep3(t *testing.T) {
	const newer = "{\n  \"schema\": 3,\n  \"last_id\": 41\n}\n"
	f, env := changeStateAtStep3(t, newer)
	if env.OK || env.Error.Kind != KindUnsupportedFormat {
		t.Fatalf("got %+v", env)
	}
	if f.readState() != newer {
		t.Errorf("state.json %q", f.readState())
	}
}
