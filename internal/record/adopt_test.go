package record

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

// Hooks-spec, Late adoption: every event but session-end creates the session,
// its directory, and its parents, mode 0700, and its files 0600; the status
// is the event's, or working for an event that sets none.
func TestLateAdoption(t *testing.T) {
	tests := []struct {
		ev     Event
		status string
	}{
		{Event{Kind: SessionStart, Source: "startup"}, "idle"},
		{Event{Kind: SessionStart, Source: "compact"}, "working"},
		{Event{Kind: SessionStart, Source: "teleport"}, "working"},
		{Event{Kind: UserPromptSubmit}, "working"},
		{Event{Kind: PostToolUse}, "working"},
		{Event{Kind: PostToolUseFailure}, "working"},
		{Event{Kind: Stop}, "waiting"},
		{Event{Kind: StopFailure}, "waiting"},
		{Event{Kind: PermissionPrompt}, "needs_approval"},
		{Event{Kind: ElicitationDialog}, "needs_approval"},
		{Event{Kind: ElicitationComplete}, "working"},
		{Event{Kind: PreCompact}, "working"},
		{Event{Kind: PostCompact}, "working"},
	}
	for _, tc := range tests {
		t.Run(baseType(tc.ev.Kind)+"/"+tc.ev.Source, func(t *testing.T) {
			f := newFix(t)
			f.setenv("CLAUDE_PID", "4242")
			f.setenv("CLAUDE_CODE_ENTRYPOINT", "sdk-cli")
			ev := tc.ev
			ev.Cwd, ev.TranscriptPath, ev.PermissionMode = "/work", "/work/t.jsonl", "plan"
			f.rec(ev)
			l := f.life(sid)
			if l.Status != tc.status {
				t.Errorf("status %q, want %q", l.Status, tc.status)
			}
			if l.EventSeq != 1 || l.StartedAt != model.FormatTimestamp(t0) || l.LastStartAt != l.StartedAt || l.LastEventAt != l.StartedAt {
				t.Errorf("clocks: seq %d, started %s, last start %s, last event %s", l.EventSeq, l.StartedAt, l.LastStartAt, l.LastEventAt)
			}
			if val(l.Cwd) != "/work" || val(l.TranscriptPath) != "/work/t.jsonl" || val(l.PermissionMode) != "plan" {
				t.Errorf("payload fields: %q %q %q", val(l.Cwd), val(l.TranscriptPath), val(l.PermissionMode))
			}
			// The pid and nested come from the lookup, once, given CLAUDE_PID.
			if f.lookups != 1 || f.lookedAt != "4242" {
				t.Errorf("lookup called %d times, with %q", f.lookups, f.lookedAt)
			}
			if cnt(l.PID) != 4242 || val(l.PIDStartedAt) != "linux:boot:77" || l.Nested == nil || *l.Nested {
				t.Errorf("pid %d, pid_started_at %q, nested %v", cnt(l.PID), val(l.PIDStartedAt), l.Nested)
			}
			if val(l.Entrypoint) != "sdk-cli" {
				t.Errorf("entrypoint %q", val(l.Entrypoint))
			}
			if l.Compactions != 0 && tc.ev.Kind != PostCompact {
				t.Errorf("compactions %d", l.Compactions)
			}
			if f.sesshinID(sid) != 1 {
				t.Errorf("sesshin id %d, want 1", f.sesshinID(sid))
			}

			for _, dir := range []string{f.path(), f.path("sessions"), f.sessionPath(sid)} {
				if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
					t.Errorf("%s: %v, %v; want mode 0700", dir, fi, err)
				}
			}
			for _, file := range []string{f.sessionPath(sid, "lifecycle.json"), f.sessionPath(sid, "sesshin.json"), f.path("state.json")} {
				if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
					t.Errorf("%s: %v, %v; want mode 0600", file, fi, err)
				}
			}
		})
	}
}

// Hooks-spec, Late adoption: an entrypoint that fails its guard is null, and
// an event that carries its own process lookup makes no second one.
func TestAdoptionEntrypointAndClaude(t *testing.T) {
	f := newFix(t)
	f.setenv("CLAUDE_CODE_ENTRYPOINT", "SDK CLI")
	f.rec(Event{Kind: PostToolUse})
	if ep := f.life(sid).Entrypoint; ep != nil {
		t.Errorf("entrypoint %q, want null", *ep)
	}

	g := newFix(t)
	g.rec(Event{Kind: SessionStart, Source: "startup", Claude: &proc.Claude{PID: 9, StartedAt: "linux:b:9"}})
	if g.lookups != 0 || cnt(g.life(sid).PID) != 9 {
		t.Errorf("lookups %d, pid %d", g.lookups, cnt(g.life(sid).PID))
	}
}

// Hooks-spec, A new lifecycle.json: a lookup that finds nothing leaves pid,
// pid_started_at, and nested null.
func TestAdoptionNoClaude(t *testing.T) {
	f := newFix(t)
	f.env.Lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{} }
	f.rec(Event{Kind: Stop})
	if l := f.life(sid); l.PID != nil || l.PIDStartedAt != nil || l.Nested != nil {
		t.Errorf("pid %v, pid_started_at %v, nested %v", l.PID, l.PIDStartedAt, l.Nested)
	}
}

// Hooks-spec, session-end and Recording an event step 1: a hook that can't
// adopt creates nothing and logs nothing, when there is no session directory,
// or no lifecycle.json in it.
func TestNoAdoption(t *testing.T) {
	f := newFix(t)
	if err := recordWith(f.env, Event{Kind: SessionEnd, Reason: "other"}); err != ErrNothingToRecord {
		t.Errorf("err %v, want ErrNothingToRecord", err)
	}
	if _, err := os.Stat(f.path()); !os.IsNotExist(err) {
		t.Errorf("session-end created the state directory: %v", err)
	}
	if f.lookups != 0 {
		t.Error("session-end looked up a process")
	}

	f.write(f.sessionPath(sid, "other.txt"), "x")
	if err := recordWith(f.env, Event{Kind: SessionEnd}); err != ErrNothingToRecord {
		t.Errorf("err %v, want ErrNothingToRecord", err)
	}
	for _, name := range []string{"lifecycle.json", "sesshin.json"} {
		if _, err := os.Stat(f.sessionPath(sid, name)); !os.IsNotExist(err) {
			t.Errorf("session-end wrote %s: %v", name, err)
		}
	}
	if _, err := os.Stat(f.path("state.json")); !os.IsNotExist(err) {
		t.Errorf("session-end issued an ID: %v", err)
	}
	if got := f.logged(); got != "" {
		t.Errorf("logged %q", got)
	}

	// A session that exists is recorded.
	f.rec(Event{Kind: PostToolUse})
	f.rec(Event{Kind: SessionEnd, Reason: "other"})
	if f.life(sid).EndedAt == nil {
		t.Error("session-end of a known session not recorded")
	}
}

// Hooks-spec, Recording an event step 2: an unusable lifecycle.json, or one in
// another format, is replaced as if it were missing, and logged. A hook that
// can't adopt treats it as missing, and writes nothing.
func TestUnusableLifecycle(t *testing.T) {
	bad := map[string]string{
		"not json":       "{",
		"empty":          "",
		"another format": `{"schema": 2}`,
		"a field":        `{"schema": 1, "session_id": "` + sid + `"}`,
		"another session": mustMarshal(t, model.LifecycleFile{SessionID: "11111111-1111-4111-8111-111111111111",
			StartedAt: "2026-10-03T00:00:00Z", LastStartAt: "2026-10-03T00:00:00Z", LastEventAt: "2026-10-03T00:00:00Z",
			Status: "idle", LastEventType: "start", EventSeq: 5}),
	}
	for name, content := range bad {
		t.Run(name, func(t *testing.T) {
			f := newFix(t)
			f.rec(Event{Kind: PostToolUse})
			f.rec(Event{Kind: PostToolUse})
			f.write(f.sessionPath(sid, "lifecycle.json"), content)
			f.rec(Event{Kind: Stop, Cwd: "/new"})
			l := f.life(sid)
			if l.EventSeq != 1 || l.StartedAt != model.FormatTimestamp(t0) || l.Status != "waiting" || val(l.Cwd) != "/new" {
				t.Errorf("replacement: %+v", l)
			}
			if got := f.logged(); !strings.Contains(got, "lifecycle.json unusable") {
				t.Errorf("log %q", got)
			}
			if got := f.sesshinID(sid); got != 1 {
				t.Errorf("replacing lifecycle.json changed the sesshin ID to %d", got)
			}

			f.write(f.sessionPath(sid, "lifecycle.json"), content)
			if err := recordWith(f.env, Event{Kind: SessionEnd}); err != ErrNothingToRecord {
				t.Errorf("session-end err %v", err)
			}
			if data, _ := os.ReadFile(f.sessionPath(sid, "lifecycle.json")); string(data) != content {
				t.Error("session-end rewrote an unusable lifecycle.json")
			}
		})
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonio.MarshalFile(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Hooks-spec, Recording an event step 2: a lifecycle.json that exists but
// can't be read is logged and left as it is, whichever hook finds it: replacing
// it would wipe event_seq, compactions, and session_title.
func TestUnreadableLifecycle(t *testing.T) {
	for name, kind := range map[string]Kind{"post-tool-use": PostToolUse, "session-start": SessionStart, "session-end": SessionEnd} {
		t.Run(name, func(t *testing.T) {
			f := newFix(t)
			f.rec(Event{Kind: UserPromptSubmit, SessionTitle: "kept"})
			f.rec(Event{Kind: PreCompact})
			before, err := os.ReadFile(f.sessionPath(sid, "lifecycle.json"))
			if err != nil {
				t.Fatal(err)
			}
			f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, "lifecycle.json", 1, syscall.EIO)}
			err = recordWith(f.env, Event{Kind: kind})
			if err == nil || errors.Is(err, ErrNothingToRecord) {
				t.Errorf("error %v, want the read's", err)
			}
			if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.EIO {
				t.Errorf("error %v is not EIO", err)
			}
			after, _ := os.ReadFile(f.sessionPath(sid, "lifecycle.json"))
			if string(after) != string(before) {
				t.Errorf("lifecycle.json written:\n%s\nwas\n%s", after, before)
			}
			if got := f.logged(); got != "read lifecycle.json: input/output error" {
				t.Errorf("log %q", got)
			}
		})
	}
}

// Hooks-spec, Recording an event step 4: a write that fails is logged, and
// the hook goes no further: no sesshin.json, no ID.
func TestLifecycleWriteFails(t *testing.T) {
	f := newFix(t)
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpCreateTemp, "", 1, syscall.ENOSPC)}
	err := recordWith(f.env, Event{Kind: PostToolUse})
	if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.ENOSPC {
		t.Errorf("err %v", err)
	}
	if got := f.logged(); !strings.Contains(got, "write lifecycle.json") {
		t.Errorf("log %q", got)
	}
	if _, err := os.Stat(f.sessionPath(sid, "sesshin.json")); !os.IsNotExist(err) {
		t.Errorf("sesshin.json written: %v", err)
	}
	if f.lastID() != -1 {
		t.Error("an ID was issued")
	}
}

// An event of no known kind is logged, and records nothing.
func TestUnknownKind(t *testing.T) {
	f := newFix(t)
	if err := recordWith(f.env, Event{}); err == nil {
		t.Error("no error")
	}
	if !strings.Contains(f.logged(), "unknown event kind") {
		t.Errorf("log %q", f.logged())
	}
	if _, err := os.Stat(f.path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("created %v", err)
	}
}
