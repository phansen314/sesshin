package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

const sid = "0b0d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3e"

func event(name string, members ...string) string {
	body := `{"session_id":"` + sid + `","hook_event_name":"` + name + `"`
	for _, m := range members {
		body += "," + m
	}
	return body + "}"
}

// quiet fails the test unless the hook exited 0 with nothing on stdout or
// stderr (H1, H3).
func quiet(t *testing.T, res Result) {
	t.Helper()
	if res.Exit != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 0 and silence", res.Exit, res.Stdout, res.Stderr)
	}
}

func sessionDir(h *Harness) string { return filepath.Join(h.Loc.StateDir, "sessions", sid) }

func readLifecycle(t *testing.T, h *Harness) model.LifecycleFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(sessionDir(h), "lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	l, r := model.ReadLifecycle(b, sid)
	if !r.Usable {
		t.Fatalf("lifecycle.json unusable: %s", r.Reason())
	}
	return l
}

// A realistic turn: a prompt, three tools, a stop; a straggler after it; a
// compaction; a cwd change; the end.
func TestLifecycleSession(t *testing.T) {
	t.Parallel()
	h := New(t)
	quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`, `"cwd":"/work"`, `"permission_mode":"default"`)))
	l := readLifecycle(t, h)
	if l.Status != "working" || l.LastEventType != "prompt" || l.EventSeq != 1 {
		t.Errorf("after prompt: %q %q seq %d", l.Status, l.LastEventType, l.EventSeq)
	}
	for i := 0; i < 3; i++ {
		quiet(t, h.Hook("post-tool-use", event("PostToolUse", `"prompt_id":"p1"`)))
	}
	if l = readLifecycle(t, h); l.Status != "working" || l.LastEventType != "tool" || l.EventSeq != 4 {
		t.Errorf("after tools: %q %q seq %d", l.Status, l.LastEventType, l.EventSeq)
	}
	quiet(t, h.Hook("stop", event("Stop", `"prompt_id":"p1"`, `"background_tasks":[]`)))
	l = readLifecycle(t, h)
	if l.Status != "waiting" || l.LastEventType != "stop" || l.EventSeq != 5 || ptr(l.EndedPromptID) != "p1" {
		t.Errorf("after stop: %q %q seq %d ended %s", l.Status, l.LastEventType, l.EventSeq, ptr(l.EndedPromptID))
	}
	if sesshin := readSesshin(t, h); sesshin.ID == nil || *sesshin.ID != 1 {
		t.Errorf("sesshin id %s; want 1", ptr(sesshin.ID))
	}

	// A straggler moves the clocks only.
	quiet(t, h.Hook("post-tool-use", event("PostToolUseFailure", `"prompt_id":"p1"`)))
	if l = readLifecycle(t, h); l.Status != "waiting" || l.LastEventType != "stop" || l.EventSeq != 6 {
		t.Errorf("after straggler: %q %q seq %d", l.Status, l.LastEventType, l.EventSeq)
	}

	// A compaction counts once, on PostCompact.
	quiet(t, h.Hook("compact", event("PreCompact", `"trigger":"auto"`)))
	quiet(t, h.Hook("compact", event("PostCompact", `"trigger":"auto"`)))
	if l = readLifecycle(t, h); l.Compactions != 1 || l.LastEventType != "compact:auto" || l.Status != "waiting" || l.EventSeq != 8 {
		t.Errorf("after compaction: %d %q %q seq %d", l.Compactions, l.LastEventType, l.Status, l.EventSeq)
	}

	// cwd-changed moves cwd and nothing else.
	before := readLifecycle(t, h)
	quiet(t, h.Hook("cwd-changed", `{"session_id":"`+sid+`","cwd":"/work","new_cwd":"/elsewhere"}`))
	after := readLifecycle(t, h)
	if ptr(after.Cwd) != "/elsewhere" {
		t.Errorf("cwd %s", ptr(after.Cwd))
	}
	after.Cwd = before.Cwd
	a, _ := jsonio.MarshalFile(after)
	b, _ := jsonio.MarshalFile(before)
	if string(a) != string(b) {
		t.Errorf("cwd-changed changed more than cwd:\n%s\n%s", b, a)
	}

	quiet(t, h.Hook("session-end", event("SessionEnd", `"reason":"logout"`)))
	l = readLifecycle(t, h)
	if l.EndedAt == nil || ptr(l.EndReason) != "logout" || l.LastEventType != "end:logout" || l.EventSeq != 9 {
		t.Errorf("after end: ended_at %v reason %s type %q seq %d", l.EndedAt, ptr(l.EndReason), l.LastEventType, l.EventSeq)
	}
}

// notification records its three types and nothing else.
func TestNotificationE2E(t *testing.T) {
	t.Parallel()
	h := New(t)
	quiet(t, h.Hook("notification", event("Notification", `"notification_type":"idle_prompt"`)))
	if _, err := os.Stat(h.Loc.StateDir); !os.IsNotExist(err) {
		t.Errorf("idle_prompt created the state directory: %v", err)
	}
	quiet(t, h.Hook("notification", event("Notification", `"notification_type":"permission_prompt"`)))
	if l := readLifecycle(t, h); l.Status != "needs_approval" || l.LastEventType != "notify" {
		t.Errorf("%q %q", l.Status, l.LastEventType)
	}
}

// session-end and cwd-changed adopt nothing and create nothing.
func TestNoAdoptionE2E(t *testing.T) {
	t.Parallel()
	h := New(t)
	quiet(t, h.Hook("session-end", event("SessionEnd", `"reason":"other"`)))
	quiet(t, h.Hook("cwd-changed", `{"session_id":"`+sid+`","new_cwd":"/x"}`))
	if _, err := os.Stat(h.Loc.StateDir); !os.IsNotExist(err) {
		t.Errorf("state directory created: %v", err)
	}
}

// Implementation-spec, Performance gate: 8 concurrent writers × 100 events
// to one session leave event_seq at exactly 800.
func TestConcurrentWriters(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("spawns 800 hooks")
	}
	h := New(t)
	quiet(t, h.Hook("user-prompt", event("UserPromptSubmit")))
	const writers, each = 8, 100
	loop := fmt.Sprintf(`i=0; while [ $i -lt %d ]; do printf '%%s' '%s' | %s post-tool-use || exit 1; i=$((i+1)); done`,
		each, event("PostToolUse"), shQuote(h.HookPath))
	start := time.Now()
	var wg sync.WaitGroup
	results := make([]Result, writers)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[w] = h.Run(loop, "")
		}()
	}
	wg.Wait()
	t.Logf("%d hooks in %v", writers*each, time.Since(start))
	for w, res := range results {
		if res.Exit != 0 || res.Stdout != "" || res.Stderr != "" {
			t.Errorf("writer %d: exit %d, stdout %q, stderr %q", w, res.Exit, res.Stdout, res.Stderr)
		}
	}
	if l := readLifecycle(t, h); l.EventSeq != 1+writers*each {
		t.Errorf("event_seq %d, want %d", l.EventSeq, 1+writers*each)
	}
	noLog(t, h)
}
