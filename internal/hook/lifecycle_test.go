package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

// send runs verb with a payload for the session, with extra members, and
// returns the state directory.
func send(t *testing.T, home, verb, members string) string {
	t.Helper()
	body := `{"session_id":"` + session + `"`
	if members != "" {
		body += "," + members
	}
	_, state := run(t, home, body+"}", verb)
	return state
}

func readLifecycle(t *testing.T, state string) model.LifecycleFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(state, "sessions", session, "lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	l, r := model.ReadLifecycle(b, session)
	if !r.Usable {
		t.Fatalf("lifecycle.json unusable: %s", r.Reason())
	}
	return l
}

func deref(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

func count(p *int64) string {
	if p == nil {
		return "<null>"
	}
	return string(rune('0' + *p))
}

// Hooks-spec, each verb's Effects and Degraded: the event a payload maps to,
// as the row of the effects table it records.
func TestVerbMapping(t *testing.T) {
	tests := []struct {
		name, verb, members string
		status, typ         string
		stall, tasks, crons string
	}{
		{"user-prompt", "user-prompt", `"hook_event_name":"UserPromptSubmit"`, "working", "prompt", "<null>", "<null>", "<null>"},
		{"user-prompt, no event name", "user-prompt", ``, "working", "prompt", "<null>", "<null>", "<null>"},
		{"post-tool-use", "post-tool-use", `"hook_event_name":"PostToolUse"`, "working", "tool", "<null>", "<null>", "<null>"},
		{"post-tool-use failure", "post-tool-use", `"hook_event_name":"PostToolUseFailure"`, "working", "toolfail", "<null>", "<null>", "<null>"},
		{"post-tool-use, no event name", "post-tool-use", ``, "working", "tool", "<null>", "<null>", "<null>"},
		{"post-tool-use, unknown event name", "post-tool-use", `"hook_event_name":"Other"`, "working", "tool", "<null>", "<null>", "<null>"},
		{"post-tool-use, event name of the wrong type", "post-tool-use", `"hook_event_name":7`, "working", "tool", "<null>", "<null>", "<null>"},
		{"stop", "stop", `"hook_event_name":"Stop"`, "waiting", "stop", "<null>", "0", "0"},
		{"stop, counted", "stop", `"hook_event_name":"Stop","background_tasks":[{},{}],"session_crons":[1]`, "waiting", "stop", "<null>", "2", "1"},
		{"stop, counts not arrays", "stop", `"hook_event_name":"Stop","background_tasks":"x","session_crons":{"a":1}`, "waiting", "stop", "<null>", "0", "0"},
		{"stopfailure", "stop", `"hook_event_name":"StopFailure","error":"rate_limit"`, "waiting", "stopfail", "rate_limit", "0", "0"},
		{"stopfailure, no error", "stop", `"hook_event_name":"StopFailure"`, "waiting", "stopfail", "unknown", "0", "0"},
		{"stopfailure, error fails its guard", "stop", `"hook_event_name":"StopFailure","error":"Bad Error"`, "waiting", "stopfail", "unknown", "0", "0"},
		{"stop, no event name", "stop", ``, "waiting", "stop", "<null>", "0", "0"},
		{"stop, unreadable event name, with error", "stop", `"hook_event_name":3,"error":"rate_limit"`, "waiting", "stopfail", "rate_limit", "0", "0"},
		{"stop, no event name, with error", "stop", `"error":"server_error"`, "waiting", "stopfail", "server_error", "0", "0"},
		{"stop, unreadable event name, bad error", "stop", `"error":"Bad Error"`, "waiting", "stop", "<null>", "0", "0"},
		{"permission_prompt", "notification", `"notification_type":"permission_prompt"`, "needs_approval", "notify", "<null>", "<null>", "<null>"},
		{"elicitation_dialog", "notification", `"notification_type":"elicitation_dialog"`, "needs_approval", "elicit", "<null>", "<null>", "<null>"},
		{"elicitation_complete", "notification", `"notification_type":"elicitation_complete"`, "working", "elicit_done", "<null>", "<null>", "<null>"},
		{"precompact", "compact", `"hook_event_name":"PreCompact","trigger":"auto"`, "working", "precompact", "<null>", "<null>", "<null>"},
		{"postcompact", "compact", `"hook_event_name":"PostCompact","trigger":"auto"`, "working", "compact:auto", "<null>", "<null>", "<null>"},
		{"postcompact, no trigger", "compact", `"hook_event_name":"PostCompact"`, "working", "compact", "<null>", "<null>", "<null>"},
		{"compact, no event name", "compact", `"trigger":"manual"`, "working", "precompact", "<null>", "<null>", "<null>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			state := send(t, home, tc.verb, tc.members)
			l := readLifecycle(t, state)
			if l.Status != tc.status || l.LastEventType != tc.typ || deref(l.StallReason) != tc.stall ||
				count(l.BackgroundTasks) != tc.tasks || count(l.SessionCrons) != tc.crons {
				t.Errorf("status %q, type %q, stall %s, tasks %s, crons %s; want %q, %q, %s, %s, %s",
					l.Status, l.LastEventType, deref(l.StallReason), count(l.BackgroundTasks), count(l.SessionCrons),
					tc.status, tc.typ, tc.stall, tc.tasks, tc.crons)
			}
			if l.EventSeq != 1 {
				t.Errorf("event_seq %d, want 1", l.EventSeq)
			}
			if _, err := os.Stat(filepath.Join(state, "sessions", session, "sesshin.json")); err != nil {
				t.Errorf("sesshin.json: %v", err)
			}
		})
	}
}

// A compaction counts once, on PostCompact; user-prompt stores the title and
// the prompt id; stop stores the prompt id of the turn it ended.
func TestVerbQualifiers(t *testing.T) {
	home := t.TempDir()
	send(t, home, "compact", `"hook_event_name":"PreCompact"`)
	state := send(t, home, "compact", `"hook_event_name":"PostCompact"`)
	if l := readLifecycle(t, state); l.Compactions != 1 || l.EventSeq != 2 {
		t.Errorf("compactions %d, event_seq %d; want 1, 2", l.Compactions, l.EventSeq)
	}
	send(t, home, "user-prompt", `"session_title":"my title"`)
	state = send(t, home, "stop", `"prompt_id":"p1","permission_mode":"acceptEdits"`)
	l := readLifecycle(t, state)
	if deref(l.SessionTitle) != "my title" || deref(l.EndedPromptID) != "p1" || deref(l.PermissionMode) != "acceptEdits" {
		t.Errorf("title %s, ended_prompt_id %s, permission_mode %s", deref(l.SessionTitle), deref(l.EndedPromptID), deref(l.PermissionMode))
	}
	// A straggler moves the clocks and nothing else.
	state = send(t, home, "post-tool-use", `"prompt_id":"p1"`)
	if l := readLifecycle(t, state); l.Status != "waiting" || l.LastEventType != "stop" || l.EventSeq != 5 {
		t.Errorf("straggler: status %q, type %q, event_seq %d", l.Status, l.LastEventType, l.EventSeq)
	}
}

// Hooks-spec, notification: any other type returns before any lock, creating
// nothing and logging nothing.
func TestNotificationOtherTypes(t *testing.T) {
	for _, members := range []string{
		`"notification_type":"idle_prompt"`,
		`"notification_type":"agent_needs_input"`,
		`"notification_type":"auth_success"`,
		`"notification_type":""`,
		`"notification_type":5`,
		``,
	} {
		home := t.TempDir()
		state := send(t, home, "notification", members)
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Errorf("%s: state directory created: %v", members, err)
		}
	}
	// Nor does one touch a session that exists.
	home := t.TempDir()
	send(t, home, "user-prompt", ``)
	state := send(t, home, "notification", `"notification_type":"idle_prompt"`)
	if l := readLifecycle(t, state); l.EventSeq != 1 {
		t.Errorf("event_seq %d after an ignored notification, want 1", l.EventSeq)
	}
	if got := readLog(t, state); got != "" {
		t.Errorf("hooks.log %q, want none", got)
	}
}

// Hooks-spec, session-end: sets ended_at and end_reason; a reason that fails
// its guard costs the qualifier; no lifecycle.json writes nothing.
func TestSessionEnd(t *testing.T) {
	home := t.TempDir()
	state := send(t, home, "session-end", `"reason":"other"`)
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Errorf("session-end with no session created %v", err)
	}
	send(t, home, "user-prompt", ``)
	state = send(t, home, "session-end", `"reason":"logout"`)
	l := readLifecycle(t, state)
	if l.EndedAt == nil || deref(l.EndReason) != "logout" || l.LastEventType != "end:logout" || l.EventSeq != 2 {
		t.Errorf("ended_at %v, end_reason %s, type %q, event_seq %d", l.EndedAt, deref(l.EndReason), l.LastEventType, l.EventSeq)
	}
	state = send(t, home, "session-end", `"reason":"Bad Reason"`)
	l = readLifecycle(t, state)
	if l.EndedAt == nil || l.EndReason != nil || l.LastEventType != "end" {
		t.Errorf("bad reason: ended_at %v, end_reason %s, type %q", l.EndedAt, deref(l.EndReason), l.LastEventType)
	}
}

// Hooks-spec, cwd-changed: cwd and nothing else; no clocks; no adoption.
func TestCwdChanged(t *testing.T) {
	home := t.TempDir()
	state := send(t, home, "cwd-changed", `"new_cwd":"/x"`)
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Errorf("cwd-changed with no session created %v", err)
	}
	send(t, home, "user-prompt", `"cwd":"/old"`)
	before := readLifecycle(t, state)
	if deref(before.Cwd) != "/old" {
		t.Fatalf("cwd %s", deref(before.Cwd))
	}
	file := filepath.Join(state, "sessions", session, "lifecycle.json")
	raw, _ := os.ReadFile(file)

	send(t, home, "cwd-changed", `"cwd":"/ignored","new_cwd":""`)
	send(t, home, "cwd-changed", `"cwd":"/ignored"`)
	if got, _ := os.ReadFile(file); string(got) != string(raw) {
		t.Errorf("an empty new_cwd wrote:\n%s", got)
	}

	send(t, home, "cwd-changed", `"cwd":"/ignored","new_cwd":"/new\nline"`)
	after := readLifecycle(t, state)
	if deref(after.Cwd) != "/new line" {
		t.Errorf("cwd %q, want %q", deref(after.Cwd), "/new line")
	}
	after.Cwd = before.Cwd
	a, _ := jsonio.MarshalFile(after)
	b, _ := jsonio.MarshalFile(before)
	if string(a) != string(b) {
		t.Errorf("more than cwd changed:\n%s\n%s", b, a)
	}
	if got := readLog(t, state); got != "" {
		t.Errorf("hooks.log %q", got)
	}
}

// An unusable lifecycle.json is left alone by cwd-changed, and logged.
func TestCwdChangedUnusable(t *testing.T) {
	home := t.TempDir()
	state := send(t, home, "user-prompt", ``)
	file := filepath.Join(state, "sessions", session, "lifecycle.json")
	if err := os.WriteFile(file, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	send(t, home, "cwd-changed", `"new_cwd":"/new"`)
	if got, _ := os.ReadFile(file); string(got) != "{not json" {
		t.Errorf("lifecycle.json rewritten: %q", got)
	}
	if got := readLog(t, state); !strings.Contains(got, "cwd-changed "+session+" lifecycle.json unusable") {
		t.Errorf("hooks.log %q", got)
	}
}
