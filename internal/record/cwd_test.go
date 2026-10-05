package record

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

// Hooks-spec, cwd-changed: cwd = the scrubbed new_cwd, and nothing else: not
// the clocks, not event_seq, not sesshin.json.
func TestSetCwd(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: UserPromptSubmit, Cwd: "/old"})
	before := f.life(sid)
	sesshinBefore, _ := os.ReadFile(f.sessionPath(sid, "sesshin.json"))
	f.env.Now = t0.Add(time.Hour)
	if err := SetCwd(f.env, "/new\x00dir "); err != nil {
		t.Fatal(err)
	}
	after := f.life(sid)
	if val(after.Cwd) != "/new dir " {
		t.Errorf("cwd %q", val(after.Cwd))
	}
	if after.LastEventAt != before.LastEventAt || after.EventSeq != before.EventSeq {
		t.Errorf("clocks moved: %v seq %d → %v seq %d", before.LastEventAt, before.EventSeq, after.LastEventAt, after.EventSeq)
	}
	after.Cwd = before.Cwd
	if *after.Cwd != "/old" || after.Status != before.Status || after.LastEventType != before.LastEventType ||
		after.StartedAt != before.StartedAt || after.Compactions != before.Compactions {
		t.Errorf("more than cwd changed: %+v", after)
	}
	if got, _ := os.ReadFile(f.sessionPath(sid, "sesshin.json")); string(got) != string(sesshinBefore) {
		t.Error("sesshin.json changed")
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}
}

// An empty cwd, a cwd already stored, and a session with no lifecycle.json
// write nothing; none adopts, and none creates the directory.
func TestSetCwdWritesNothing(t *testing.T) {
	f := newFix(t)
	if err := SetCwd(f.env, "/x"); err != ErrNothingToRecord {
		t.Errorf("no session: %v", err)
	}
	if _, err := os.Stat(f.path()); !os.IsNotExist(err) {
		t.Errorf("state directory created: %v", err)
	}
	f.rec(Event{Kind: UserPromptSubmit, Cwd: "/old"})
	file := f.sessionPath(sid, "lifecycle.json")
	raw, _ := os.ReadFile(file)
	for _, cwd := range []string{"", "/old"} {
		if err := SetCwd(f.env, cwd); err != nil {
			t.Errorf("cwd %q: %v", cwd, err)
		}
	}
	if got, _ := os.ReadFile(file); string(got) != string(raw) {
		t.Error("lifecycle.json rewritten")
	}
	// A directory with no lifecycle.json: nothing to record, and not logged.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := SetCwd(f.env, "/x"); err != ErrNothingToRecord {
		t.Errorf("no lifecycle.json: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("lifecycle.json created: %v", err)
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}
}

// An unusable lifecycle.json is logged and left alone, not replaced.
func TestSetCwdUnusable(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: UserPromptSubmit})
	f.write(f.sessionPath(sid, "lifecycle.json"), "{nope")
	if err := SetCwd(f.env, "/x"); err == nil {
		t.Error("no error")
	}
	if got, _ := os.ReadFile(f.sessionPath(sid, "lifecycle.json")); string(got) != "{nope" {
		t.Errorf("rewritten: %q", got)
	}
	if got := f.logged(); !strings.Contains(got, "lifecycle.json unusable") {
		t.Errorf("log %q", got)
	}
}

// SetCwd shares Record's lock: a held lock gives up within the deadline,
// logged; a directory renamed aside is retried once, then there is nothing
// to record.
func TestSetCwdLock(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: UserPromptSubmit, Cwd: "/old"})
	release := hold(t, f.sessionPath(sid))
	f.env.LockWait = 40 * time.Millisecond
	f.env.Deadline = time.Now().Add(80 * time.Millisecond)
	err := SetCwd(f.env, "/x")
	release()
	if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.EAGAIN {
		t.Errorf("err %v", err)
	}
	if got := f.logged(); !strings.Contains(got, "session lock") {
		t.Errorf("log %q", got)
	}
	if val(f.life(sid).Cwd) != "/old" {
		t.Error("written under a held lock")
	}

	g := newFix(t)
	g.rec(Event{Kind: UserPromptSubmit, Cwd: "/old"})
	g.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: renameAside(g.sessionPath(sid), 1)}
	if err := SetCwd(g.env, "/x"); err != ErrNothingToRecord {
		t.Errorf("moved: %v", err)
	}
}
