package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

const sid = "0b0d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3e"

var t0 = time.Date(2026, 10, 3, 18, 31, 51, 0, time.UTC)

// fix is a state directory in a temp directory, and the Env that records
// into it.
type fix struct {
	t   *testing.T
	env Env

	mu   sync.Mutex
	logs []string
	vars map[string]string
	// lookups counts calls of the fake Lookup, and holds the CLAUDE_PID it
	// was given last.
	lookups  int
	lookedAt string
}

// claude is what the fake lookup finds.
func claude() proc.Claude {
	no := false
	return proc.Claude{PID: 4242, StartedAt: "linux:boot:77", Nested: &no}
}

// nestedClaude is a lookup that finds a Claude started by another session.
func nestedClaude(fsys.FS, string) proc.Claude {
	yes := true
	return proc.Claude{PID: 4243, StartedAt: "linux:boot:78", Nested: &yes}
}

func newFix(t *testing.T) *fix {
	t.Helper()
	f := &fix{t: t, vars: map[string]string{}}
	f.env = Env{
		FS:        fsys.OS{},
		Loc:       loc.Locations{StateDir: filepath.Join(t.TempDir(), "state")},
		SessionID: sid,
		Now:       t0,
		LockWait:  2 * time.Second,
		Getenv:    f.getenv,
		Log:       f.log,
		Lookup: func(_ fsys.FS, pid string) proc.Claude {
			f.mu.Lock()
			f.lookups++
			f.lookedAt = pid
			f.mu.Unlock()
			return claude()
		},
	}
	return f
}

func (f *fix) getenv(k string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vars[k]
}

func (f *fix) setenv(k, v string) {
	f.mu.Lock()
	f.vars[k] = v
	f.mu.Unlock()
}

func (f *fix) log(msg string) {
	f.mu.Lock()
	f.logs = append(f.logs, msg)
	f.mu.Unlock()
}

// logged is every line logged so far, joined.
func (f *fix) logged() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.logs, "\n")
}

func (f *fix) path(parts ...string) string {
	return filepath.Join(append([]string{f.env.Loc.StateDir}, parts...)...)
}

func (f *fix) sessionPath(id string, parts ...string) string {
	return f.path(append([]string{"sessions", id}, parts...)...)
}

// recordWith records ev with env, giving it a lock deadline of its own, far
// away, unless env sets one.
func recordWith(env Env, ev Event) error {
	if env.Deadline.IsZero() {
		env.Deadline = time.Now().Add(30 * time.Second)
	}
	return Record(env, ev)
}

// rec records ev, and fails the test when it returns an error.
func (f *fix) rec(ev Event) {
	f.t.Helper()
	if err := recordWith(f.env, ev); err != nil {
		f.t.Fatalf("Record(%v): %v\nlog:\n%s", ev.Kind, err, f.logged())
	}
}

// as is the Env for another session of the same state directory.
func (f *fix) as(id string) Env {
	e := f.env
	e.SessionID = id
	return e
}

// life reads session id's lifecycle.json, which must be usable.
func (f *fix) life(id string) model.LifecycleFile {
	f.t.Helper()
	data, err := os.ReadFile(f.sessionPath(id, "lifecycle.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	l, r := model.ReadLifecycle(data, id)
	if !r.Usable {
		f.t.Fatalf("lifecycle.json unusable: %s\n%s", r.Reason(), data)
	}
	return l
}

// sesshin reads session id's sesshin.json, which must be usable.
func (f *fix) sesshin(id string) model.SesshinFile {
	f.t.Helper()
	data, err := os.ReadFile(f.sessionPath(id, "sesshin.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	h, r := model.ReadSesshin(data)
	if !r.Usable {
		f.t.Fatalf("sesshin.json unusable: %s\n%s", r.Reason(), data)
	}
	return h
}

// sesshinID is session id's sesshin ID, 0 for null.
func (f *fix) sesshinID(id string) int64 {
	f.t.Helper()
	if h := f.sesshin(id); h.ID != nil {
		return *h.ID
	}
	return 0
}

// lastID reads state.json's last_id, -1 when the file is missing.
func (f *fix) lastID() int64 {
	f.t.Helper()
	data, err := os.ReadFile(f.path("state.json"))
	if os.IsNotExist(err) {
		return -1
	}
	if err != nil {
		f.t.Fatal(err)
	}
	s, r := model.ReadState(data)
	if !r.Usable {
		f.t.Fatalf("state.json unusable: %s\n%s", r.Reason(), data)
	}
	return s.LastID
}

func (f *fix) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func ptr(s string) *string { return &s }

// val is a pointer field's value, "" for nil.
func val(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// cnt is a nullable count's value, -1 for null.
func cnt(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// kitty is a placement as the kitty backend would record it.
func kitty(window int) *jsonio.Object {
	return &jsonio.Object{Members: []jsonio.Member{
		{Key: "terminal", Value: "kitty"},
		{Key: "socket", Value: "unix:/tmp/kitty-1"},
		{Key: "window_id", Value: json.Number(string(rune('0' + window)))},
	}}
}

// placed returns a Placement that returns obj, and counts its calls, and
// remembers the old placement it was given.
func placed(obj *jsonio.Object) (func(*jsonio.Object, bool) *jsonio.Object, *int, **jsonio.Object) {
	calls := 0
	var old *jsonio.Object
	return func(o *jsonio.Object, _ bool) *jsonio.Object {
		calls++
		old = o
		return obj
	}, &calls, &old
}
