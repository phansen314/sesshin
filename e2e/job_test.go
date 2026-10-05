package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

// Design-spec, Reservations, Adopt: a session started with SESSHIN_JOB takes the
// job when no one holds it, and a second one with the same SESSHIN_JOB gets none,
// while the first lives. The fake claude has exited by the time the second
// hook runs, so the first is made to read as live with the pid left unknown.
func TestHookAdoptsSesshinJob(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.Setenv("SESSHIN_JOB", "api")
	quiet(t, h.Hook("session-start", eventFor(sid, "SessionStart", `"source":"startup"`)))
	if sesshin := readSesshin(t, h); sesshin.Job == nil || *sesshin.Job != "api" || sesshin.Source != "hook" {
		t.Fatalf("job %s, source %s; want api, hook", ptr(sesshin.Job), sesshin.Source)
	}

	p := filepath.Join(sessionDir(h), "lifecycle.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	l, r := model.ReadLifecycle(b, sid)
	if !r.Usable {
		t.Fatalf("lifecycle.json unusable: %s", r.Reason())
	}
	l.PID, l.PIDStartedAt = nil, nil
	if b, err = jsonio.MarshalFile(l); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}

	quiet(t, h.Hook("session-start", eventFor(sid2, "SessionStart", `"source":"startup"`)))
	b, err = os.ReadFile(filepath.Join(h.Loc.StateDir, "sessions", sid2, "sesshin.json"))
	if err != nil {
		t.Fatal(err)
	}
	sesshin, r := model.ReadSesshin(b)
	if !r.Usable || sesshin.Job != nil || sesshin.Source != "hook" || sesshin.ID == nil || *sesshin.ID != 2 {
		t.Errorf("second session: %s", b)
	}
	if log := hooksLog(h); !strings.Contains(log, "job api held by #1") {
		t.Errorf("hooks.log %q", log)
	}
}
