package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/schematest"
)

// envelope runs sesshin and returns its envelope, checked against the schema.
func sesshinEnvelope(t *testing.T, h *Harness, wantExit int, args ...string) map[string]any {
	t.Helper()
	res := h.Sesshin(args...)
	if res.Exit != wantExit {
		t.Fatalf("sesshin %v: exit %d, stdout %q, stderr %q; want %d", args, res.Exit, res.Stdout, res.Stderr, wantExit)
	}
	if ok, f := schematest.Check(t, "envelope", []byte(res.Stdout)); !ok {
		t.Errorf("envelope rejects %s at %s", res.Stdout, f)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatal(err)
	}
	return env
}

// Sessions made by the real hooks: the fake claude has exited by the time
// sesshin runs, so they read as ended. list and show report them, by sesshin ID,
// by UUID, and by prefix, and fail as the spec says.
func TestListAndShow(t *testing.T) {
	t.Parallel()
	h := New(t)
	quiet(t, h.Hook("session-start", eventFor(sid, "SessionStart", `"source":"startup"`, `"cwd":"/work"`)))
	quiet(t, h.Hook("session-start", eventFor(sid2, "SessionStart", `"source":"startup"`)))
	h.Nested = true
	quiet(t, h.Hook("session-start", eventFor(sid3, "SessionStart", `"source":"startup"`)))
	h.Nested = false

	// Nothing is live, and the headless one is hidden.
	env := sesshinEnvelope(t, h, 0, "list")
	if r := env["result"].(map[string]any); r["total"] != float64(0) || len(r["sessions"].([]any)) != 0 || r["truncated"] != false {
		t.Errorf("live: %v", r)
	}
	env = sesshinEnvelope(t, h, 0, "list", "--liveness", "all")
	r := env["result"].(map[string]any)
	sessions := r["sessions"].([]any)
	if r["total"] != float64(2) || len(sessions) != 2 {
		t.Fatalf("all: %v", r)
	}
	ids := map[string]float64{}
	for _, s := range sessions {
		s := s.(map[string]any)
		ids[s["session_id"].(string)] = s["id"].(float64)
		if s["liveness"] != "ended" || s["status"] == "" || s["headless"] != false {
			t.Errorf("%v", s)
		}
		// The hooks create sesshin.json without a job, as a claude of your own.
		if s["job"] != nil || s["source"] != "hook" {
			t.Errorf("job %v, source %v", s["job"], s["source"])
		}
	}
	if ids[sid] != 1 || ids[sid2] != 2 {
		t.Errorf("sesshin IDs %v", ids)
	}
	env = sesshinEnvelope(t, h, 0, "list", "--liveness", "all", "--include-headless", "--limit", "1", "--fields", "name,cwd")
	r = env["result"].(map[string]any)
	if r["total"] != float64(3) || r["truncated"] != true || len(r["sessions"].([]any)) != 1 {
		t.Errorf("headless: %v", r)
	}
	if got := r["sessions"].([]any)[0].(map[string]any); len(got) != 4 || got["name"] == nil {
		t.Errorf("fields: %v", got)
	}
	if env := sesshinEnvelope(t, h, 1, "list", "--fields", "nope"); env["error"].(map[string]any)["kind"] != "invalid-input" {
		t.Errorf("%v", env)
	}

	for _, sel := range []string{"1", sid, strings.ToUpper(sid), sid[:8]} {
		env = sesshinEnvelope(t, h, 0, "show", sel)
		s := env["result"].(map[string]any)["session"].(map[string]any)
		if s["session_id"] != sid || s["id"] != float64(1) || s["cwd"] != "/work" || s["name"] != "#1" || s["job"] != nil || s["source"] != "hook" {
			t.Errorf("show %s: %v", sel, s)
		}
		if _, ok := env["result"].(map[string]any)["statusline_payload"]; ok {
			t.Errorf("show %s: a payload without include_payload", sel)
		}
	}
	env = sesshinEnvelope(t, h, 0, "show", "2", "--include-payload")
	if p, ok := env["result"].(map[string]any)["statusline_payload"]; !ok || p != nil {
		t.Errorf("no statusline.json: %v", env["result"])
	}

	// A tick gives the session a payload, shown verbatim.
	quiet2 := h.Hook("statusline", `{"session_id":"`+sid+`","cwd":"/tmp","model":{"id":"claude-x"}}`)
	if quiet2.Exit != 0 {
		t.Fatalf("%+v", quiet2)
	}
	env = sesshinEnvelope(t, h, 0, "show", "1", "--include-payload")
	out := env["result"].(map[string]any)
	p, _ := out["statusline_payload"].(map[string]any)
	if p["cwd"] != "/tmp" || out["session"].(map[string]any)["model"] != "claude-x" || out["session"].(map[string]any)["metrics"] == nil {
		t.Errorf("%v", out)
	}

	env = sesshinEnvelope(t, h, 1, "show", "99")
	if e := env["error"].(map[string]any); e["kind"] != "not-found" {
		t.Errorf("%v", e)
	}
	env = sesshinEnvelope(t, h, 1, "show", "0")
	if e := env["error"].(map[string]any); e["kind"] != "invalid-input" {
		t.Errorf("%v", e)
	}
	// Usage errors: no session, and one too many.
	for _, args := range [][]string{{"show"}, {"show", "1", "2"}, {"list", "1"}} {
		env = sesshinEnvelope(t, h, 2, args...)
		if e := env["error"].(map[string]any); e["kind"] != "usage" {
			t.Errorf("%v: %v", args, e)
		}
	}
}
