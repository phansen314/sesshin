package record

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
)

// set returns an apply that stores next and says it changed, and counts its
// calls.
func set(next *jsonio.Object, calls *int) func(*jsonio.Object) (*jsonio.Object, bool) {
	return func(*jsonio.Object) (*jsonio.Object, bool) {
		*calls++
		return next, true
	}
}

func synced() *jsonio.Object {
	p := kitty(5)
	p.Set("tab_title", "api")
	return p
}

// Hooks-spec, terminal-sync: apply sees the stored placement; a change is
// written to sesshin.json, with the id kept, and lifecycle.json is untouched.
func TestSetPlacementSync(t *testing.T) {
	f := newFix(t)
	p, _, _ := placed(kitty(5))
	f.env.Placement = p
	f.rec(Event{Kind: UserPromptSubmit})
	lifeBefore, _ := os.ReadFile(f.sessionPath(sid, "lifecycle.json"))
	var old *jsonio.Object
	err := SetPlacementSync(f.env, func(o *jsonio.Object) (*jsonio.Object, bool) {
		old = o
		return synced(), true
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := old.Get("window_id"); v == nil {
		t.Errorf("apply was given %v", old)
	}
	h := f.sesshin(sid)
	if title, _ := h.Placement.Get("tab_title"); title != "api" {
		t.Errorf("tab_title %v", title)
	}
	if h.ID == nil || *h.ID != 1 {
		t.Errorf("id %v", h.ID)
	}
	if got, _ := os.ReadFile(f.sessionPath(sid, "lifecycle.json")); string(got) != string(lifeBefore) {
		t.Error("lifecycle.json changed")
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}
}

// Nothing is written, and apply is not asked, for a null placement; an apply
// that reports no change writes nothing, not even the same bytes again.
func TestSetPlacementSyncWritesNothing(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: UserPromptSubmit}) // no backend: null placement
	file := f.sessionPath(sid, "sesshin.json")
	raw, _ := os.ReadFile(file)
	calls := 0
	if err := SetPlacementSync(f.env, set(synced(), &calls)); err != nil || calls != 0 {
		t.Errorf("null placement: %v, %d calls", err, calls)
	}
	if got, _ := os.ReadFile(file); string(got) != string(raw) {
		t.Error("sesshin.json rewritten for a null placement")
	}

	p, _, _ := placed(kitty(5))
	f.env.Placement = p
	f.write(file, `{"schema": 1, "id": 3, "job": null, "source": "hook", "placement": {"terminal": "kitty", "socket": "unix:/x", "window_id": 5}}`)
	raw, _ = os.ReadFile(file)
	noChange := func(o *jsonio.Object) (*jsonio.Object, bool) { return synced(), false }
	if err := SetPlacementSync(f.env, noChange); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file); string(got) != string(raw) {
		t.Error("sesshin.json rewritten without a change")
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}
}

// No session directory is created; a directory with no sesshin.json is nothing
// to record, not logged; an unusable sesshin.json is logged and left alone.
func TestSetPlacementSyncNoFile(t *testing.T) {
	f := newFix(t)
	calls := 0
	if err := SetPlacementSync(f.env, set(synced(), &calls)); err != ErrNothingToRecord {
		t.Errorf("no session: %v", err)
	}
	if _, err := os.Stat(f.path()); !os.IsNotExist(err) {
		t.Errorf("state directory created: %v", err)
	}
	if err := os.MkdirAll(f.sessionPath(sid), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetPlacementSync(f.env, set(synced(), &calls)); err != ErrNothingToRecord {
		t.Errorf("no sesshin.json: %v", err)
	}
	if _, err := os.Stat(f.sessionPath(sid, "sesshin.json")); !os.IsNotExist(err) {
		t.Errorf("sesshin.json created: %v", err)
	}
	if f.logged() != "" {
		t.Errorf("log %q", f.logged())
	}
	f.write(f.sessionPath(sid, "sesshin.json"), "{")
	if err := SetPlacementSync(f.env, set(synced(), &calls)); err == nil {
		t.Error("unusable sesshin.json: no error")
	}
	if got, _ := os.ReadFile(f.sessionPath(sid, "sesshin.json")); string(got) != "{" {
		t.Errorf("sesshin.json replaced: %q", got)
	}
	if !strings.Contains(f.logged(), "sesshin.json unusable") || calls != 0 {
		t.Errorf("log %q, %d calls", f.logged(), calls)
	}
}

// H4: a held session lock makes it give up within the deadline, log, and
// write nothing.
func TestSetPlacementSyncLockHeld(t *testing.T) {
	f := newFix(t)
	p, _, _ := placed(kitty(5))
	f.env.Placement = p
	f.rec(Event{Kind: UserPromptSubmit})
	raw, _ := os.ReadFile(f.sessionPath(sid, "sesshin.json"))
	hold(t, f.sessionPath(sid))
	f.env.LockWait = 40 * time.Millisecond
	f.env.Deadline = time.Now().Add(80 * time.Millisecond)
	calls := 0
	start := time.Now()
	err := SetPlacementSync(f.env, set(synced(), &calls))
	elapsed := time.Since(start)
	if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.EAGAIN {
		t.Errorf("err %v", err)
	}
	if elapsed < 30*time.Millisecond || elapsed > 80*time.Millisecond+250*time.Millisecond {
		t.Errorf("gave up after %v, want about 40ms, within the 80ms deadline", elapsed)
	}
	if got := f.logged(); !strings.Contains(got, "session lock") || calls != 0 {
		t.Errorf("log %q, %d calls", got, calls)
	}
	if got, _ := os.ReadFile(f.sessionPath(sid, "sesshin.json")); string(got) != string(raw) {
		t.Error("sesshin.json changed")
	}
}

// terminal-sync changes the placement only: job and source stay as stored.
func TestSetPlacementSyncKeepsJobAndSource(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: UserPromptSubmit})
	f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 1, "id": 3, "job": "api-review", "source": "spawn", "placement": {"terminal": "kitty", "socket": "unix:/x", "window_id": 5}}`)
	if err := SetPlacementSync(f.env, set(synced(), new(int))); err != nil {
		t.Fatal(err)
	}
	h := f.sesshin(sid)
	if h.Job == nil || *h.Job != "api-review" || h.Source != "spawn" || h.ID == nil || *h.ID != 3 {
		t.Errorf("sesshin.json %+v", h)
	}
}
