package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
)

// startWith runs session-start for the session with the payload members and
// the environment given, plus HOME, and returns the state directory. Under
// `go test` Claude is not an ancestor, but CLAUDE_PID names this process's
// parent, which proc.Find accepts, so a pid is found.
func startWith(t *testing.T, home string, env map[string]string, members string) string {
	t.Helper()
	return startAt(t, home, env, members, start)
}

// startAt is startWith at the time given.
func startAt(t *testing.T, home string, env map[string]string, members string, at time.Time) string {
	t.Helper()
	body := `{"session_id":"` + session + `","hook_event_name":"SessionStart"`
	if members != "" {
		body += "," + members
	}
	Run([]string{"session-start"}, Process{
		Stdin:  strings.NewReader(body + "}"),
		Stdout: &bytes.Buffer{},
		FS:     fsys.OS{},
		Getenv: func(k string) string {
			if k == "HOME" {
				return home
			}
			return env[k]
		},
		GOOS: "linux",
		Now:  func() time.Time { return at },
	})
	return filepath.Join(home, ".local", "state", "sesshin")
}

func readSesshinFile(t *testing.T, state string) model.SesshinFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(state, "sessions", session, "sesshin.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, r := model.ReadSesshin(b)
	if !r.Usable {
		t.Fatalf("sesshin.json unusable: %s", r.Reason())
	}
	return h
}

func withParentPID(env map[string]string) map[string]string {
	out := map[string]string{"CLAUDE_PID": strconv.Itoa(os.Getppid()), "CLAUDE_CODE_ENTRYPOINT": "cli"}
	for k, v := range env {
		out[k] = v
	}
	return out
}

// Hooks-spec, session-start, Effects: each source's row, with the pid found,
// and the sesshin ID issued. Placement, and a lookup that finds no Claude, depend on the
// ancestry of the test process, so the e2e tests cover them.
func TestSessionStartSources(t *testing.T) {
	tests := []struct {
		source, status, typ string
	}{
		{"startup", "idle", "start"},
		{"resume", "idle", "start:resume"},
		{"clear", "idle", "start:clear"},
		{"fork", "idle", "start:fork"},
		{"compact", "working", "start:compact"},
		{"unknown", "working", "start:unknown"},
		{"", "working", "start"},
	}
	for _, tc := range tests {
		t.Run(tc.source, func(t *testing.T) {
			members := `"cwd":"/w","transcript_path":"/t"`
			if tc.source != "" {
				members += `,"source":"` + tc.source + `"`
			}
			state := startWith(t, t.TempDir(), withParentPID(nil), members)
			l := readLifecycle(t, state)
			if l.Status != tc.status || l.LastEventType != tc.typ || l.EventSeq != 1 {
				t.Errorf("status %q, type %q, seq %d; want %q, %q, 1", l.Status, l.LastEventType, l.EventSeq, tc.status, tc.typ)
			}
			if l.PID == nil || *l.PID != int64(os.Getppid()) || l.PIDStartedAt == nil || l.Nested == nil {
				t.Errorf("pid %v, pid_started_at %v, nested %v", l.PID, l.PIDStartedAt, l.Nested)
			}
			if deref(l.Entrypoint) != "cli" || deref(l.Cwd) != "/w" || deref(l.TranscriptPath) != "/t" {
				t.Errorf("entrypoint %s, cwd %s, transcript_path %s", deref(l.Entrypoint), deref(l.Cwd), deref(l.TranscriptPath))
			}
			h := readSesshinFile(t, state)
			if h.ID == nil || *h.ID != 1 {
				t.Errorf("sesshin id %v, want 1", h.ID)
			}
		})
	}
}

// A resume of an ended session revives it.
func TestSessionStartRevives(t *testing.T) {
	home := t.TempDir()
	env := withParentPID(nil)
	startWith(t, home, env, `"source":"startup"`)
	send(t, home, "compact", `"hook_event_name":"PostCompact"`)
	state := send(t, home, "session-end", `"reason":"logout"`)
	before := readLifecycle(t, state)
	if before.EndedAt == nil {
		t.Fatal("not ended")
	}
	startAt(t, home, env, `"source":"resume"`, start.Add(time.Minute))
	l := readLifecycle(t, state)
	if l.LastStartAt == before.LastStartAt || l.LastStartAt != model.FormatTimestamp(start.Add(time.Minute)) {
		t.Errorf("last_start_at %v, was %v; want it moved to the resume's time", l.LastStartAt, before.LastStartAt)
	}
	if l.EndedAt != nil || l.EndReason != nil || l.Status != "idle" || l.StartedAt != before.StartedAt ||
		l.Compactions != 1 || l.EventSeq != 4 {
		t.Errorf("ended_at %v, end_reason %s, status %q, started_at %v, compactions %d, seq %d",
			l.EndedAt, deref(l.EndReason), l.Status, l.StartedAt, l.Compactions, l.EventSeq)
	}
	if h := readSesshinFile(t, state); h.ID == nil || *h.ID != 1 {
		t.Errorf("sesshin id %v", h.ID)
	}
}

// A payload with no model and no permission_mode leaves both as they were.
func TestSessionStartKeepsModelAndMode(t *testing.T) {
	home := t.TempDir()
	env := withParentPID(nil)
	startWith(t, home, env, `"source":"startup","model":"m","permission_mode":"plan"`)
	state := startWith(t, home, env, `"source":"resume"`)
	l := readLifecycle(t, state)
	if deref(l.Model) != "m" || deref(l.PermissionMode) != "plan" {
		t.Errorf("model %s, permission_mode %s", deref(l.Model), deref(l.PermissionMode))
	}
}
