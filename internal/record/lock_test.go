package record

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

// hold takes the lock on directory dir, as another hook would, and returns
// its release.
func hold(t *testing.T, dir string) (release func()) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := fsys.OS{}.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			lock.Unlock()
			root.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// spyFS records the wait of every Lock, and passes each to the real lock with
// a single try, after pausing for delay as if it had waited that long.
type spyFS struct {
	fsys.FS
	delay time.Duration
	mu    sync.Mutex
	waits []time.Duration
}

type spyRoot struct {
	fsys.Root
	s *spyFS
}

func (s *spyFS) OpenRoot(p string) (fsys.Root, error) {
	r, err := s.FS.OpenRoot(p)
	if err != nil {
		return nil, err
	}
	return &spyRoot{Root: r, s: s}, nil
}

func (r *spyRoot) Lock(wait time.Duration) (fsys.Lock, error) {
	r.s.mu.Lock()
	r.s.waits = append(r.s.waits, wait)
	r.s.mu.Unlock()
	time.Sleep(r.s.delay)
	return r.Root.Lock(0)
}

// Hooks-spec, H4; implementation-spec, Locks: every wait is the smaller of
// hook_lock_wait_ms and what is left of the lock deadline.
func TestWait(t *testing.T) {
	e := Env{LockWait: time.Second}
	if got := e.wait(); got != time.Second {
		t.Errorf("no deadline: %v", got)
	}
	e.Deadline = time.Now().Add(time.Hour)
	if got := e.wait(); got != time.Second {
		t.Errorf("far deadline: %v", got)
	}
	e.Deadline = time.Now().Add(100 * time.Millisecond)
	if got := e.wait(); got <= 0 || got > 100*time.Millisecond {
		t.Errorf("near deadline: %v", got)
	}
	e.Deadline = time.Now().Add(-time.Second)
	if got := e.wait(); got != 0 {
		t.Errorf("past deadline: %v, want a single try", got)
	}
}

// Hooks-spec, H4: the session lock, and the state lock after it, together
// never wait past the deadline: the second gets what the first left.
func TestWaitsShareTheDeadline(t *testing.T) {
	f := newFix(t)
	spy := &spyFS{FS: fsys.OS{}, delay: 60 * time.Millisecond}
	f.env.FS = spy
	f.env.LockWait = 100 * time.Millisecond
	f.env.Deadline = time.Now().Add(130 * time.Millisecond)
	f.rec(Event{Kind: PostToolUse})
	if len(spy.waits) != 2 {
		t.Fatalf("lock waits %v, want the session lock's and the state lock's", spy.waits)
	}
	if spy.waits[0] != 100*time.Millisecond {
		t.Errorf("session lock waited %v, want hook_lock_wait_ms", spy.waits[0])
	}
	if w := spy.waits[1]; w <= 0 || w > 70*time.Millisecond {
		t.Errorf("state lock waited %v, want what is left of the 130ms deadline after 60ms", w)
	}
}

// Hooks-spec, Recording an event step 1: a held session lock makes the hook
// give up within the deadline, log, and write nothing.
func TestSessionLockHeld(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: PostToolUse})
	hold(t, f.sessionPath(sid))
	f.env.LockWait = 40 * time.Millisecond
	f.env.Deadline = time.Now().Add(80 * time.Millisecond)
	start := time.Now()
	err := Record(f.env, Event{Kind: PostToolUse})
	elapsed := time.Since(start)
	if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.EAGAIN {
		t.Errorf("err %v", err)
	}
	if elapsed < 30*time.Millisecond || elapsed > 80*time.Millisecond+250*time.Millisecond {
		t.Errorf("gave up after %v, want about 40ms, within the 80ms deadline", elapsed)
	}
	if got := f.logged(); !strings.Contains(got, "session lock") {
		t.Errorf("log %q", got)
	}
	// The event is lost: nothing was written.
	if got := f.life(sid).EventSeq; got != 1 {
		t.Errorf("event_seq %d, want 1", got)
	}
}

// A lock the deadline has left nothing for is a single try, so a free lock is
// still taken.
func TestDeadlinePassed(t *testing.T) {
	f := newFix(t)
	f.env.Deadline = time.Now().Add(-time.Second)
	if err := Record(f.env, Event{Kind: PostToolUse}); err != nil {
		t.Fatal(err)
	}
	if f.life(sid).EventSeq != 1 || f.sesshinID(sid) != 1 {
		t.Error("not recorded")
	}
}

// renameAside is a Fault hook that renames the session directory aside just
// before the first n Locks of it, as a prune does between the hook's open and
// its lock.
func renameAside(dir string, n int) fsys.Hook {
	var mu sync.Mutex
	seen := 0
	return func(op fsys.Op) error {
		if op.Name != fsys.OpLock || op.Root != dir {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		seen++
		if seen <= n {
			return os.Rename(dir, dir+".aside"+string(rune('0'+seen)))
		}
		return nil
	}
}

// Hooks-spec, Recording an event step 1: a directory renamed aside between
// open and lock is retried once, and the event lands in the directory the
// path names now.
func TestMovedRetriedOnce(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: PostToolUse})
	dir := f.sessionPath(sid)
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: renameAside(dir, 1)}
	f.rec(Event{Kind: Stop})
	if got := f.life(sid); got.EventSeq != 1 || got.Status != "waiting" {
		t.Errorf("new directory's lifecycle.json: seq %d, status %s; want a new record from the retry", got.EventSeq, got.Status)
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}
	data, err := os.ReadFile(dir + ".aside1/lifecycle.json")
	if err != nil || !strings.Contains(string(data), `"event_seq": 1`) || strings.Contains(string(data), `"waiting"`) {
		t.Errorf("the directory renamed aside was written: %v\n%s", err, data)
	}
}

// A directory moved again on the retry loses the event, logged.
func TestMovedTwice(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: PostToolUse})
	dir := f.sessionPath(sid)
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: renameAside(dir, 2)}
	if err := recordWith(f.env, Event{Kind: Stop}); err == nil {
		t.Error("no error")
	}
	if got := f.logged(); !strings.Contains(got, "moved again") {
		t.Errorf("log %q", got)
	}
	// A hook that can't adopt retrying finds the directory gone: nothing to
	// record.
	g := newFix(t)
	g.rec(Event{Kind: PostToolUse})
	g.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: renameAside(g.sessionPath(sid), 1)}
	if err := recordWith(g.env, Event{Kind: SessionEnd}); err != ErrNothingToRecord {
		t.Errorf("session-end err %v", err)
	}
}

// Hooks-spec, Creating sesshin.json step 1: a held state lock yields a sesshin.json
// with a null id, logged; the next lifecycle hook completes it, keeping its
// placement, and the session's lifecycle.json is recorded either way.
func TestStateLockHeld(t *testing.T) {
	f := newFix(t)
	f.setenv("CLAUDE_PID", "1")
	first, _, _ := placed(kitty(1))
	f.env.Placement = first
	f.rec(Event{Kind: PostToolUse}) // creates the directories
	if err := os.Remove(f.sessionPath(sid, "sesshin.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.path("state.json")); err != nil {
		t.Fatal(err)
	}

	release := hold(t, f.path("sessions"))
	f.env.LockWait = 30 * time.Millisecond
	f.env.Deadline = time.Now().Add(60 * time.Millisecond)
	start := time.Now()
	err := Record(f.env, Event{Kind: PostToolUse})
	if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.EAGAIN {
		t.Errorf("err %v", err)
	}
	if elapsed := time.Since(start); elapsed > 60*time.Millisecond+250*time.Millisecond {
		t.Errorf("took %v", elapsed)
	}
	if got := f.logged(); !strings.Contains(got, "state lock") || !strings.Contains(got, "without an id") {
		t.Errorf("log %q", got)
	}
	if f.life(sid).EventSeq != 2 {
		t.Error("lifecycle.json not recorded")
	}
	h := f.sesshin(sid)
	if h.ID != nil || h.Placement == nil {
		t.Errorf("sesshin.json id %v, placement %v; want a null id and the placement", h.ID, h.Placement)
	}
	if f.lastID() != -1 {
		t.Error("an ID was issued without the lock")
	}

	// Still held: the next hook leaves the file as it is, and logs again.
	f.logs = nil
	if err := Record(f.env, Event{Kind: PostToolUse}); err == nil {
		t.Error("no error while the state lock is held")
	}
	if got := f.logged(); !strings.Contains(got, "keeps no id") {
		t.Errorf("log %q", got)
	}
	if h := f.sesshin(sid); h.ID != nil || h.Placement == nil {
		t.Errorf("sesshin.json id %v, placement %v", h.ID, h.Placement)
	}

	// Released: the next hook completes it, keeping the placement; only
	// session-start replaces one.
	release()
	second, calls, _ := placed(kitty(2))
	f.env.Placement = second
	f.env.LockWait = time.Second
	f.env.Deadline = time.Time{}
	f.rec(Event{Kind: PostToolUse})
	h = f.sesshin(sid)
	if h.ID == nil || *h.ID != 1 || f.lastID() != 1 {
		t.Errorf("id %v, last_id %d; want 1", h.ID, f.lastID())
	}
	if w, _ := h.Placement.Get("window_id"); w == nil || string(w.(json.Number)) != "1" {
		t.Errorf("placement %v, want the first's kept", h.Placement)
	}
	if *calls != 0 {
		t.Errorf("the backend was asked for a placement %d times while completing", *calls)
	}
}

// Hooks-spec, Creating sesshin.json: a state.json that can't be written leaves
// sesshin.json without an id, logged, for the next hook.
func TestStateWriteFails(t *testing.T) {
	f := newFix(t)
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpCreateTemp && op.Root == f.path() {
			return syscall.ENOSPC
		}
		return nil
	}}
	err := recordWith(f.env, Event{Kind: PostToolUse})
	if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.ENOSPC {
		t.Errorf("err %v", err)
	}
	if got := f.logged(); !strings.Contains(got, "write state.json") || !strings.Contains(got, "without an id") {
		t.Errorf("log %q", got)
	}
	if h := f.sesshin(sid); h.ID != nil {
		t.Errorf("id %d", *h.ID)
	}
	f.env.FS = fsys.OS{}
	f.rec(Event{Kind: PostToolUse})
	if f.sesshinID(sid) != 1 || f.lastID() != 1 {
		t.Errorf("not completed: id %d, last_id %d", f.sesshinID(sid), f.lastID())
	}
}
