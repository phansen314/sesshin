package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// start runs session-start under the fake claude with the given source and
// payload members, and requires a quiet exit 0.
func start(t *testing.T, h *Harness, source string, members ...string) Result {
	t.Helper()
	if source != "" {
		members = append([]string{`"source":"` + source + `"`}, members...)
	}
	res := h.Hook("session-start", event("SessionStart", members...))
	quiet(t, res)
	return res
}

// Hooks-spec, session-start: a new session, as the fake claude's descendant.
func TestSessionStartStartup(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.KittyWindow(kittySocket, "7")
	res := start(t, h, "startup", `"cwd":"/work"`, `"transcript_path":"/t.jsonl"`, `"model":"claude-x"`)
	l := readLifecycle(t, h)
	if l.Status != "idle" || l.LastEventType != "start" || l.EventSeq != 1 || l.Compactions != 0 {
		t.Errorf("status %q, type %q, seq %d, compactions %d", l.Status, l.LastEventType, l.EventSeq, l.Compactions)
	}
	if ptr(l.PID) != strconv.Itoa(res.ClaudePID) || !strings.HasPrefix(ptr(l.PIDStartedAt), "linux:") {
		t.Errorf("pid %s, pid_started_at %s; want the fake claude's %d", ptr(l.PID), ptr(l.PIDStartedAt), res.ClaudePID)
	}
	if ptr(l.Entrypoint) != "cli" || ptr(l.Nested) != "false" {
		t.Errorf("entrypoint %s, nested %s", ptr(l.Entrypoint), ptr(l.Nested))
	}
	if ptr(l.Cwd) != "/work" || ptr(l.TranscriptPath) != "/t.jsonl" || ptr(l.Model) != "claude-x" {
		t.Errorf("cwd %s, transcript_path %s, model %s", ptr(l.Cwd), ptr(l.TranscriptPath), ptr(l.Model))
	}
	if l.EndedAt != nil || l.EndReason != nil || l.EndedPromptID != nil || l.StallReason != nil {
		t.Errorf("ended_at %v, end_reason %s, ended_prompt_id %s, stall_reason %s",
			l.EndedAt, ptr(l.EndReason), ptr(l.EndedPromptID), ptr(l.StallReason))
	}
	if sesshin := readSesshin(t, h); sesshin.ID == nil || *sesshin.ID != 1 {
		t.Errorf("sesshin id %s, want 1", ptr(sesshin.ID))
	}
	want := `{"terminal":"kitty","socket":"` + kittySocket + `","window_id":7}`
	if got := readSesshinPlacement(t, h); got != want {
		t.Errorf("placement %s, want %s", got, want)
	}
	noLog(t, h)
}

// A resume of an ended session revives it and carries the rest on, the sesshin
// ID among it; clear and fork start a life as well.
func TestSessionStartResume(t *testing.T) {
	t.Parallel()
	h := New(t)
	start(t, h, "startup")
	quiet(t, h.Hook("compact", event("PostCompact", `"trigger":"auto"`)))
	quiet(t, h.Hook("session-end", event("SessionEnd", `"reason":"logout"`)))
	before := readLifecycle(t, h)
	if before.EndedAt == nil || before.Compactions != 1 || before.EventSeq != 3 {
		t.Fatalf("setup: ended_at %v, compactions %d, seq %d", before.EndedAt, before.Compactions, before.EventSeq)
	}
	res := start(t, h, "resume")
	l := readLifecycle(t, h)
	if l.Status != "idle" || l.LastEventType != "start:resume" || l.EndedAt != nil || l.EndReason != nil {
		t.Errorf("status %q, type %q, ended_at %v, end_reason %s", l.Status, l.LastEventType, l.EndedAt, ptr(l.EndReason))
	}
	if l.StartedAt != before.StartedAt || l.Compactions != 1 || l.EventSeq != 4 {
		t.Errorf("started_at %v (was %v), compactions %d, seq %d", l.StartedAt, before.StartedAt, l.Compactions, l.EventSeq)
	}
	if ptr(l.PID) != strconv.Itoa(res.ClaudePID) {
		t.Errorf("pid %s, want the new fake claude's %d", ptr(l.PID), res.ClaudePID)
	}
	if sesshin := readSesshin(t, h); sesshin.ID == nil || *sesshin.ID != 1 {
		t.Errorf("sesshin id %s, want 1 kept", ptr(sesshin.ID))
	}
}

func TestSessionStartClearFork(t *testing.T) {
	t.Parallel()
	for _, src := range []string{"clear", "fork"} {
		t.Run(src, func(t *testing.T) {
			h := New(t)
			start(t, h, "startup")
			quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`)))
			quiet(t, h.Hook("stop", event("Stop", `"prompt_id":"p1"`, `"background_tasks":[]`)))
			start(t, h, src)
			l := readLifecycle(t, h)
			if l.Status != "idle" || l.LastEventType != "start:"+src || l.EndedPromptID != nil ||
				l.BackgroundTasks != nil || l.SessionCrons != nil {
				t.Errorf("status %q, type %q, ended_prompt_id %s, tasks %s", l.Status, l.LastEventType,
					ptr(l.EndedPromptID), ptr(l.BackgroundTasks))
			}
		})
	}
}

// A compact source, and one missing or unknown, leave the status alone, and
// the pid and nested too when no Claude is found; a Claude that is found
// replaces them.
func TestSessionStartStatusNeutral(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, typ string }{
		{"compact", "compact", "start:compact"},
		{"unknown", "later", "start:later"},
		{"missing", "", "start"},
		{"bad shape", "Bad Source", "start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := New(t)
			start(t, h, "startup")
			quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`)))
			before := readLifecycle(t, h)
			res := start(t, h, tc.source)
			l := readLifecycle(t, h)
			if l.Status != "working" || l.LastEventType != tc.typ || l.EventSeq != before.EventSeq+1 {
				t.Errorf("status %q, type %q, seq %d", l.Status, l.LastEventType, l.EventSeq)
			}
			if l.LastStartAt != before.LastStartAt {
				t.Errorf("last_start_at moved")
			}
			if ptr(l.PID) != strconv.Itoa(res.ClaudePID) {
				t.Errorf("pid %s, want %d", ptr(l.PID), res.ClaudePID)
			}
		})
	}
}

// A compact source with no Claude found leaves pid, pid_started_at, and
// nested as they were.
func TestSessionStartCompactKeepsPID(t *testing.T) {
	t.Parallel()
	h := New(t)
	first := start(t, h, "startup")
	before := readLifecycle(t, h)
	hookOutsideClaude(t, h, event("SessionStart", `"source":"compact"`))
	l := readLifecycle(t, h)
	if l.LastEventType != "start:compact" || l.EventSeq != 2 {
		t.Fatalf("type %q, seq %d", l.LastEventType, l.EventSeq)
	}
	if ptr(l.PID) != strconv.Itoa(first.ClaudePID) || ptr(l.PIDStartedAt) != ptr(before.PIDStartedAt) ||
		ptr(l.Nested) != "false" {
		t.Errorf("pid %s, pid_started_at %s, nested %s", ptr(l.PID), ptr(l.PIDStartedAt), ptr(l.Nested))
	}
}

// A hook of a session started by another session: nested, and not placed.
func TestSessionStartNested(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.Nested = true
	h.KittyWindow(kittySocket, "7")
	start(t, h, "startup")
	l := readLifecycle(t, h)
	if ptr(l.Nested) != "true" {
		t.Errorf("nested %s, want true", ptr(l.Nested))
	}
	if got := readSesshinPlacement(t, h); got != "null" {
		t.Errorf("placement %s, want null", got)
	}
}

// Under tmux or screen the kitty variables name some other window.
func TestSessionStartMultiplexer(t *testing.T) {
	t.Parallel()
	for name, kv := range map[string][2]string{
		"tmux":   {"TMUX", "/tmp/tmux-1000/default,1,0"},
		"screen": {"STY", "123.pts-0.host"},
	} {
		t.Run(name, func(t *testing.T) {
			h := New(t)
			h.KittyWindow(kittySocket, "7")
			h.Setenv(kv[0], kv[1])
			start(t, h, "startup")
			if got := readSesshinPlacement(t, h); got != "null" {
				t.Errorf("placement %s, want null", got)
			}
			if ptr(readLifecycle(t, h).Nested) != "false" {
				t.Errorf("nested")
			}
		})
	}
}

// A SessionStart replaces the placement. The same window's tab_title and
// user_vars stay; another window's stay only on a resume, which opens its tab
// with them (design-spec.md, Placement).
func TestSessionStartResumePlacement(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.KittyWindow(kittySocket, "7")
	start(t, h, "startup")
	synced := `{"schema":2,"id":1,"job":null,"source":"hook","placement":{"terminal":"kitty","socket":"` + kittySocket +
		`","window_id":7,"tab_title":"api review","user_vars":{"project":"api"}},"extra":{}}`
	writeSesshin(t, h, synced)

	start(t, h, "resume")
	if got := readSesshinPlacement(t, h); !strings.Contains(got, `"tab_title":"api review"`) ||
		!strings.Contains(got, `"user_vars":{"project":"api"}`) || !strings.Contains(got, `"window_id":7`) {
		t.Errorf("same window: placement %s keeps the sync keys", got)
	}

	h.KittyWindow(kittySocket, "9")
	start(t, h, "resume")
	want := `{"terminal":"kitty","socket":"` + kittySocket + `","window_id":9,"tab_title":"api review","user_vars":{"project":"api"}}`
	if got := readSesshinPlacement(t, h); got != want {
		t.Errorf("resumed in another window: placement %s, want %s", got, want)
	}
	h.KittyWindow(kittySocket, "10")
	start(t, h, "startup")
	want = `{"terminal":"kitty","socket":"` + kittySocket + `","window_id":10}`
	if got := readSesshinPlacement(t, h); got != want {
		t.Errorf("started in another window: placement %s, want %s", got, want)
	}
	if sesshin := readSesshin(t, h); sesshin.ID == nil || *sesshin.ID != 1 {
		t.Errorf("sesshin id %s", ptr(sesshin.ID))
	}

	// Resumed outside kitty: the placement goes.
	h.Unsetenv("KITTY_LISTEN_ON")
	h.Unsetenv("KITTY_WINDOW_ID")
	start(t, h, "resume")
	if got := readSesshinPlacement(t, h); got != "null" {
		t.Errorf("outside kitty: placement %s, want null", got)
	}
}

// A payload without model or permission_mode leaves both as they were.
func TestSessionStartAbsentMembers(t *testing.T) {
	t.Parallel()
	h := New(t)
	start(t, h, "startup", `"model":"claude-x"`, `"permission_mode":"acceptEdits"`)
	start(t, h, "resume", `"cwd":"/w"`)
	l := readLifecycle(t, h)
	if ptr(l.Model) != "claude-x" || ptr(l.PermissionMode) != "acceptEdits" || ptr(l.Cwd) != "/w" {
		t.Errorf("model %s, permission_mode %s, cwd %s", ptr(l.Model), ptr(l.PermissionMode), ptr(l.Cwd))
	}
	h = New(t)
	start(t, h, "startup")
	l = readLifecycle(t, h)
	if l.Model != nil || l.PermissionMode != nil || l.Cwd != nil {
		t.Errorf("model %s, permission_mode %s, cwd %s; want null", ptr(l.Model), ptr(l.PermissionMode), ptr(l.Cwd))
	}
}

func hookConfig(t *testing.T, h *Harness, content string) {
	t.Helper()
	if err := os.MkdirAll(h.Loc.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Loc.ConfigDir, "hooks.properties"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// hookOutsideClaude runs the hook with no Claude among its ancestors: a
// background subshell that waits for its parent to exit, so that it is
// reparented away from the fake claude (and from any real one this test is
// itself running under). The inherited CLAUDE_PID then names a dead process.
func hookOutsideClaude(t *testing.T, h *Harness, payload string) {
	t.Helper()
	dir := t.TempDir()
	in, done := filepath.Join(dir, "in"), filepath.Join(dir, "done")
	if err := os.WriteFile(in, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	command := `outer=$$; (while kill -0 $outer 2>/dev/null; do sleep 0.01; done; ` +
		shQuote(h.HookPath) + ` session-start <` + shQuote(in) + `; echo $? >` + shQuote(done) +
		`) >/dev/null 2>&1 &`
	if res := h.Run(command, ""); res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(done)
		if err == nil && len(b) > 0 {
			if strings.TrimSpace(string(b)) != "0" {
				t.Fatalf("hook exited %s", b)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("hook did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Claude not found: pid is null, and the session is recorded all the same.
func TestSessionStartNoClaude(t *testing.T) {
	t.Parallel()
	h := New(t)
	hookOutsideClaude(t, h, event("SessionStart", `"source":"startup"`))
	l := readLifecycle(t, h)
	if l.Status != "idle" || l.PID != nil || l.PIDStartedAt != nil || l.Nested != nil {
		t.Errorf("status %q, pid %s, pid_started_at %s, nested %s",
			l.Status, ptr(l.PID), ptr(l.PIDStartedAt), ptr(l.Nested))
	}
	if sesshin := readSesshin(t, h); sesshin.ID == nil || *sesshin.ID != 1 {
		t.Errorf("sesshin id %s", ptr(sesshin.ID))
	}
}
