package record

import (
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

// snap is the part of lifecycle.json the effects table speaks of. "" is
// null for the strings, -1 for the counts.
type snap struct {
	Status      string
	Type        string
	Stall       string
	Ended       string
	BG, Crons   int64
	Compactions int64
	EndedAt     string
	EndReason   string
}

func snapOf(l model.LifecycleFile) snap {
	s := snap{
		Status: l.Status, Type: l.LastEventType, Stall: val(l.StallReason), Ended: val(l.EndedPromptID),
		BG: cnt(l.BackgroundTasks), Crons: cnt(l.SessionCrons), Compactions: l.Compactions,
		EndReason: val(l.EndReason),
	}
	if l.EndedAt != nil {
		s.EndedAt = string(*l.EndedAt)
	}
	return s
}

// base is the state every effects-table case starts from: a turn that died
// of an API error with two background tasks and a cron pending, then a
// compaction. Every field a row sets or clears has a value to be changed.
var base = snap{
	Status: "waiting", Type: "compact:auto", Stall: "api_error", Ended: "p1",
	BG: 2, Crons: 1, Compactions: 1,
}

// prior records the events that reach base.
func prior(f *fix) {
	f.rec(Event{Kind: SessionStart, Source: "startup", Cwd: "/work", Claude: &proc.Claude{}})
	f.rec(Event{Kind: UserPromptSubmit, PromptID: "p0"})
	f.rec(Event{Kind: StopFailure, PromptID: "p1", Error: "api_error", BackgroundTasks: 2, SessionCrons: 1})
	f.rec(Event{Kind: PostCompact, Trigger: "auto"})
}

// Hooks-spec, Effects table and its notes: every row, and each qualifier that
// fails its guard.
func TestEffectsTable(t *testing.T) {
	now := t0.Add(time.Hour)
	end := string(model.FormatTimestamp(now))
	with := func(f func(*snap)) snap { s := base; f(&s); return s }
	tests := []struct {
		name string
		ev   Event
		want snap
	}{
		{"SessionStart startup", Event{Kind: SessionStart, Source: "startup"},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "idle", "start", "", "", -1, -1 })},
		{"SessionStart resume", Event{Kind: SessionStart, Source: "resume"},
			with(func(s *snap) {
				s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "idle", "start:resume", "", "", -1, -1
			})},
		{"SessionStart clear", Event{Kind: SessionStart, Source: "clear"},
			with(func(s *snap) {
				s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "idle", "start:clear", "", "", -1, -1
			})},
		{"SessionStart fork", Event{Kind: SessionStart, Source: "fork"},
			with(func(s *snap) {
				s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "idle", "start:fork", "", "", -1, -1
			})},
		{"SessionStart compact", Event{Kind: SessionStart, Source: "compact"},
			with(func(s *snap) { s.Type = "start:compact" })},
		{"SessionStart unknown source", Event{Kind: SessionStart, Source: "teleport"},
			with(func(s *snap) { s.Type = "start:teleport" })},
		{"SessionStart no source", Event{Kind: SessionStart},
			with(func(s *snap) { s.Type = "start" })},
		{"SessionStart source fails its guard", Event{Kind: SessionStart, Source: "Bad Source!"},
			with(func(s *snap) { s.Type = "start" })},
		{"UserPromptSubmit", Event{Kind: UserPromptSubmit, PromptID: "p2", SessionTitle: "api work"},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "working", "prompt", "", "", -1, -1 })},
		{"PostToolUse", Event{Kind: PostToolUse, PromptID: "p2"},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.BG, s.Crons = "working", "tool", "", -1, -1 })},
		{"PostToolUseFailure", Event{Kind: PostToolUseFailure, PromptID: "p2"},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.BG, s.Crons = "working", "toolfail", "", -1, -1 })},
		{"Stop", Event{Kind: Stop, PromptID: "p3", BackgroundTasks: 4},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "waiting", "stop", "", "p3", 4, 0 })},
		{"Stop with no prompt_id", Event{Kind: Stop},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "waiting", "stop", "", "", 0, 0 })},
		{"StopFailure", Event{Kind: StopFailure, PromptID: "p3", Error: "rate_limit", SessionCrons: 3},
			with(func(s *snap) {
				s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "waiting", "stopfail", "rate_limit", "p3", 0, 3
			})},
		{"StopFailure with no error", Event{Kind: StopFailure, PromptID: "p3"},
			with(func(s *snap) {
				s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "waiting", "stopfail", "unknown", "p3", 0, 0
			})},
		{"StopFailure error fails its guard", Event{Kind: StopFailure, PromptID: "p3", Error: "Rate Limit"},
			with(func(s *snap) {
				s.Status, s.Type, s.Stall, s.Ended, s.BG, s.Crons = "waiting", "stopfail", "unknown", "p3", 0, 0
			})},
		{"Notification permission_prompt", Event{Kind: PermissionPrompt, PromptID: "p2"},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.BG, s.Crons = "needs_approval", "notify", "", -1, -1 })},
		{"Notification elicitation_dialog", Event{Kind: ElicitationDialog, PromptID: "p2"},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.BG, s.Crons = "needs_approval", "elicit", "", -1, -1 })},
		{"Notification elicitation_complete", Event{Kind: ElicitationComplete, PromptID: "p2"},
			with(func(s *snap) { s.Status, s.Type, s.Stall, s.BG, s.Crons = "working", "elicit_done", "", -1, -1 })},
		{"PreCompact", Event{Kind: PreCompact},
			with(func(s *snap) { s.Type = "precompact" })},
		{"PreCompact ignores its trigger", Event{Kind: PreCompact, Trigger: "manual"},
			with(func(s *snap) { s.Type = "precompact" })},
		{"PostCompact auto", Event{Kind: PostCompact, Trigger: "auto"},
			with(func(s *snap) { s.Type, s.Compactions = "compact:auto", 2 })},
		{"PostCompact manual", Event{Kind: PostCompact, Trigger: "manual"},
			with(func(s *snap) { s.Type, s.Compactions = "compact:manual", 2 })},
		{"PostCompact no trigger", Event{Kind: PostCompact},
			with(func(s *snap) { s.Type, s.Compactions = "compact", 2 })},
		{"PostCompact trigger fails its guard", Event{Kind: PostCompact, Trigger: "AUTO"},
			with(func(s *snap) { s.Type, s.Compactions = "compact", 2 })},
		{"SessionEnd", Event{Kind: SessionEnd, Reason: "logout"},
			with(func(s *snap) { s.Type, s.EndedAt, s.EndReason = "end:logout", end, "logout" })},
		{"SessionEnd no reason", Event{Kind: SessionEnd},
			with(func(s *snap) { s.Type, s.EndedAt = "end", end })},
		{"SessionEnd reason fails its guard", Event{Kind: SessionEnd, Reason: "log out"},
			with(func(s *snap) { s.Type, s.EndedAt = "end", end })},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFix(t)
			prior(f)
			if got := snapOf(f.life(sid)); got != base {
				t.Fatalf("base state is %+v, want %+v", got, base)
			}
			before := f.life(sid)
			f.env.Now = now
			f.rec(tc.ev)
			l := f.life(sid)
			if got := snapOf(l); got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
			// The clocks move for every event (Recording an event, step 3).
			if l.EventSeq != before.EventSeq+1 || string(l.LastEventAt) != end {
				t.Errorf("event_seq %d, last_event_at %s; want %d, %s", l.EventSeq, l.LastEventAt, before.EventSeq+1, end)
			}
			if tc.ev.Kind == UserPromptSubmit && val(l.SessionTitle) != "api work" {
				t.Errorf("session_title %q", val(l.SessionTitle))
			}
		})
	}
}

// Hooks-spec, user-prompt: an empty title leaves the stored one alone.
func TestSessionTitle(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: UserPromptSubmit, SessionTitle: "first"})
	f.rec(Event{Kind: UserPromptSubmit})
	if got := val(f.life(sid).SessionTitle); got != "first" {
		t.Errorf("session_title %q after a prompt with none", got)
	}
	f.rec(Event{Kind: UserPromptSubmit, SessionTitle: "second"})
	if got := val(f.life(sid).SessionTitle); got != "second" {
		t.Errorf("session_title %q", got)
	}
}

// Hooks-spec, Recording an event step 3: permission_mode is set by every
// event that has a valid one, and kept by one that has none or a bad one.
func TestPermissionMode(t *testing.T) {
	f := newFix(t)
	f.rec(Event{Kind: PostToolUse, PermissionMode: "acceptEdits"})
	if got := val(f.life(sid).PermissionMode); got != "acceptEdits" {
		t.Errorf("permission_mode %q", got)
	}
	f.rec(Event{Kind: PostToolUse})
	f.rec(Event{Kind: PostToolUse, PermissionMode: "bad mode"})
	if got := val(f.life(sid).PermissionMode); got != "acceptEdits" {
		t.Errorf("permission_mode %q after events with none or a bad one", got)
	}
	f.rec(Event{Kind: Stop, PermissionMode: "plan"})
	if got := val(f.life(sid).PermissionMode); got != "plan" {
		t.Errorf("permission_mode %q", got)
	}
}

// Hooks-spec, session-start Effects: a source that starts a life sets the
// process, and revives an ended session; compact sets them only when known.
func TestSessionStartEffects(t *testing.T) {
	f := newFix(t)
	nested := true
	found := proc.Claude{PID: 100, StartedAt: "linux:b:1", Nested: &nested}
	f.setenv("CLAUDE_CODE_ENTRYPOINT", "sdk-cli")
	f.rec(Event{Kind: SessionStart, Source: "startup", Cwd: "/a", TranscriptPath: "/a/t.jsonl", Model: "claude-opus-5-5", Claude: &found})
	l := f.life(sid)
	if val(l.Cwd) != "/a" || val(l.TranscriptPath) != "/a/t.jsonl" || val(l.Model) != "claude-opus-5-5" ||
		cnt(l.PID) != 100 || val(l.PIDStartedAt) != "linux:b:1" || l.Nested == nil || !*l.Nested || val(l.Entrypoint) != "sdk-cli" {
		t.Errorf("after startup: %+v", l)
	}
	if f.lookups != 0 {
		t.Errorf("Lookup called %d times when the event carried Claude's process", f.lookups)
	}
	started := l.StartedAt

	// An ended session is revived by a resume; started_at carries on, and the
	// process is looked up afresh, null when not found.
	f.env.Now = t0.Add(time.Hour)
	f.rec(Event{Kind: SessionEnd, Reason: "other"})
	if f.life(sid).EndedAt == nil {
		t.Fatal("ended_at not set")
	}
	f.env.Now = t0.Add(2 * time.Hour)
	f.setenv("CLAUDE_CODE_ENTRYPOINT", "")
	f.rec(Event{Kind: SessionStart, Source: "resume", Claude: &proc.Claude{}})
	l = f.life(sid)
	if l.EndedAt != nil || l.EndReason != nil || l.PID != nil || l.PIDStartedAt != nil || l.Nested != nil || l.Entrypoint != nil {
		t.Errorf("after resume: ended %v/%v, pid %v/%v, nested %v, entrypoint %v", l.EndedAt, l.EndReason, l.PID, l.PIDStartedAt, l.Nested, l.Entrypoint)
	}
	if l.StartedAt != started || l.LastStartAt != model.FormatTimestamp(f.env.Now) || l.EventSeq != 3 {
		t.Errorf("started_at %s, last_start_at %s, event_seq %d", l.StartedAt, l.LastStartAt, l.EventSeq)
	}
	if val(l.Cwd) != "/a" || val(l.Model) != "claude-opus-5-5" {
		t.Errorf("a resume with no cwd or model kept %q, %q; want /a and the model", val(l.Cwd), val(l.Model))
	}

	// compact: the process only when found, nested only when known,
	// entrypoint only when set.
	f.rec(Event{Kind: SessionStart, Source: "compact", Claude: &proc.Claude{}})
	l = f.life(sid)
	if l.PID != nil || l.Entrypoint != nil || l.LastStartAt != model.FormatTimestamp(f.env.Now) {
		t.Errorf("compact with no process: pid %v, entrypoint %v, last_start_at %s", l.PID, l.Entrypoint, l.LastStartAt)
	}
	f.setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	f.rec(Event{Kind: SessionStart, Source: "compact", Claude: &found})
	l = f.life(sid)
	if cnt(l.PID) != 100 || l.Nested == nil || !*l.Nested || val(l.Entrypoint) != "cli" {
		t.Errorf("compact with a process: %+v", l)
	}
	f.setenv("CLAUDE_CODE_ENTRYPOINT", "")
	f.rec(Event{Kind: SessionStart, Source: "compact", Claude: &proc.Claude{PID: 200, StartedAt: "linux:b:2"}})
	l = f.life(sid)
	if cnt(l.PID) != 200 || l.Nested == nil || !*l.Nested || val(l.Entrypoint) != "cli" {
		t.Errorf("compact with a process whose nesting is unknown, and no entrypoint: pid %d, nested %v, entrypoint %q", cnt(l.PID), l.Nested, val(l.Entrypoint))
	}
}

// Hooks-spec, Straggler guard: a guarded event with the ended turn's
// prompt_id moves the clocks and permission_mode, and nothing else.
func TestStraggler(t *testing.T) {
	guarded := []Kind{PostToolUse, PostToolUseFailure, PermissionPrompt, ElicitationDialog, ElicitationComplete}
	for _, k := range guarded {
		t.Run("guarded "+baseType(k), func(t *testing.T) {
			f := newFix(t)
			f.rec(Event{Kind: UserPromptSubmit, PromptID: "p1"})
			f.rec(Event{Kind: StopFailure, PromptID: "p1", Error: "api_error", BackgroundTasks: 1})
			before := f.life(sid)
			f.env.Now = t0.Add(time.Minute)
			f.rec(Event{Kind: k, PromptID: "p1", PermissionMode: "plan"})
			l := f.life(sid)
			if l.EventSeq != before.EventSeq+1 || l.LastEventAt != model.FormatTimestamp(f.env.Now) || val(l.PermissionMode) != "plan" {
				t.Errorf("clocks or permission_mode not moved: %+v", l)
			}
			want := snapOf(before)
			if got := snapOf(l); got != want {
				t.Errorf("straggler changed state: got %+v, want %+v", got, want)
			}
			// A turn that is not the ended one, or an event with no
			// prompt_id, is no straggler.
			f.rec(Event{Kind: k, PromptID: "p2"})
			if got := snapOf(f.life(sid)).Type; got != baseType(k) {
				t.Errorf("event of another turn recorded as %q, want %q", got, baseType(k))
			}
			f.rec(Event{Kind: StopFailure, PromptID: "p3"})
			f.rec(Event{Kind: k})
			if got := snapOf(f.life(sid)).Type; got != baseType(k) {
				t.Errorf("event with no prompt_id recorded as %q, want %q", got, baseType(k))
			}
		})
	}

	unguarded := []Event{
		{Kind: Stop, PromptID: "p1", BackgroundTasks: 9},
		{Kind: StopFailure, PromptID: "p1", Error: "x"},
		{Kind: PreCompact, PromptID: "p1"},
		{Kind: PostCompact, PromptID: "p1"},
		{Kind: SessionEnd, PromptID: "p1"},
		{Kind: SessionStart, PromptID: "p1", Source: "resume"},
		{Kind: UserPromptSubmit, PromptID: "p1"},
	}
	for _, ev := range unguarded {
		t.Run("unguarded "+baseType(ev.Kind), func(t *testing.T) {
			f := newFix(t)
			f.rec(Event{Kind: Stop, PromptID: "p1"})
			before := snapOf(f.life(sid))
			f.rec(ev)
			if snapOf(f.life(sid)) == before {
				t.Errorf("an unguarded event with the ended turn's prompt_id was withheld")
			}
		})
	}

	// Compactions count even after the turn ended, and UserPromptSubmit
	// clears ended_prompt_id, so the new turn's events are not stale.
	f := newFix(t)
	f.rec(Event{Kind: Stop, PromptID: "p1"})
	f.rec(Event{Kind: PostCompact, PromptID: "p1"})
	if f.life(sid).Compactions != 1 {
		t.Error("PostCompact of the ended turn not counted")
	}
	f.rec(Event{Kind: UserPromptSubmit, PromptID: "p1"})
	if l := f.life(sid); l.EndedPromptID != nil || l.Status != "working" {
		t.Errorf("UserPromptSubmit left ended_prompt_id %v, status %s", l.EndedPromptID, l.Status)
	}
	f.rec(Event{Kind: PostToolUse, PromptID: "p1"})
	if got := snapOf(f.life(sid)).Type; got != "tool" {
		t.Errorf("event of the new turn recorded as %q", got)
	}
}
