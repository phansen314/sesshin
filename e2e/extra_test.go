package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const extraText = `{"ticket":"auth-3","n":57,"ratio":1.10}`

// spawn --extra: the extra goes into the reservation, compact in the order
// and with the numbers as given, and never into the launch's environment,
// which holds SESSHIN_TOKEN always and SESSHIN_JOB with a job
// (operations.md, spawn).
func TestSpawnExtra(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	env, res := sesshinSpawn(t, h, "--job", "api", "--cwd", h.Home, "--start-timeout-secs", "0",
		"--extra", `{ "ticket" : "auth-3", "n": 57, "ratio": 1.10 }`)
	if res.Exit != 0 || !env.OK {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	r, _ := readReservation(t, h, "api")
	want := []string{"--env=SESSHIN_JOB=api", "--env=SESSHIN_TOKEN=" + r.Token}
	calls := h.KittenCalls()
	i := slices.Index(calls[0], want[0])
	if i < 0 || !slices.Equal(calls[0][i:i+2], want) {
		t.Errorf("kitten call %q, want it to hold %q", calls[0], want)
	}
	b, err := os.ReadFile(filepath.Join(h.Loc.StateDir, "reservations", "api_"+r.Token+".json"))
	if err != nil || !strings.Contains(string(b), "\"extra\": {\n    \"ticket\": \"auth-3\",\n    \"n\": 57,\n    \"ratio\": 1.10\n  }") {
		t.Errorf("reservation %s, %v", b, err)
	}

	// Without a job, the same, in <token>.json.
	env, res = sesshinSpawn(t, h, "--cwd", h.Home, "--start-timeout-secs", "0", "--extra", `{}`)
	if res.Exit != 0 || !env.OK {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	r2, ok := readReservation(t, h, "")
	if !ok || r2.Extra == nil || r2.Extra.Len() != 0 {
		t.Fatalf("job-less reservation %+v, %v", r2, ok)
	}
	if call := h.KittenCalls()[1]; !slices.Contains(call, "--env=SESSHIN_TOKEN="+r2.Token) || slices.Contains(call, "--env=SESSHIN_JOB=api") {
		t.Errorf("kitten call %q", call)
	}

	for _, call := range h.KittenCalls() {
		for _, a := range call {
			if strings.Contains(a, "SESSHIN_EXTRA") {
				t.Errorf("argument %q", a)
			}
		}
	}

	// A bad value is refused before anything is launched.
	n := len(h.KittenCalls())
	env, res = sesshinSpawn(t, h, "--cwd", h.Home, "--extra", `{"a":`)
	if res.Exit != 1 || env.OK || env.Error == nil || env.Error.Kind != "invalid-input" || len(h.KittenCalls()) != n {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
}

// The reservation hands the session its extra at its first hook; a /clear
// session of the same process starts at {}; update changes it, live or ended;
// and it survives the end, the resume, and later hooks (design-spec.md,
// User-owned extra).
func TestExtraSurvives(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	transcript := filepath.Join(h.Home, "t.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, res := sesshinSpawn(t, h, "--job", "api", "--cwd", h.Home, "--start-timeout-secs", "0", "--extra", extraText)
	if res.Exit != 0 || !env.OK {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	r, _ := readReservation(t, h, "api")

	// The session starts, in the window sesshin launched.
	h.Setenv("SESSHIN_JOB", "api")
	h.Setenv("SESSHIN_TOKEN", r.Token)
	h.KittyWindow(kittySocket, "7")
	start(t, h, "startup", `"cwd":"`+h.Home+`"`, `"transcript_path":"`+transcript+`"`)
	got := readSesshin(t, h)
	if got.Job == nil || *got.Job != "api" || got.Source != "spawn" || got.Extra == nil || got.Extra.Len() != 3 {
		t.Fatalf("sesshin.json %+v", got)
	}
	if _, ok := readReservation(t, h, "api"); ok {
		t.Error("the reservation was kept")
	}

	listed := func(session string) string {
		t.Helper()
		res := h.Sesshin("list", "--liveness", "all", "--fields", "extra,job")
		if res.Exit != 0 {
			t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
		}
		return res.Stdout
	}
	const member = `"extra":` + extraText
	if out := listed(sid); !strings.Contains(out, member) {
		t.Errorf("list: %s", out)
	}

	// A /clear: another session, the same environment, and {}.
	const sid2 = "1c1d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3f"
	quiet(t, h.Hook("session-end", event("SessionEnd", `"reason":"clear"`)))
	clear := strings.Replace(event("SessionStart", `"source":"clear"`, `"cwd":"`+h.Home+`"`), sid, sid2, 1)
	quiet(t, h.Hook("session-start", clear))
	b, err := os.ReadFile(filepath.Join(h.Loc.StateDir, "sessions", sid2, "sesshin.json"))
	if err != nil || !strings.Contains(string(b), "\"extra\": {}") {
		t.Errorf("the cleared session's sesshin.json: %s, %v", b, err)
	}

	// update changes the ended session's extra, in place, and says so.
	res = h.Sesshin("update", "1", "--extra-merge", `{"status":"review"}`, "--extra-remove", "n")
	var upd struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				Extra json.RawMessage `json:"extra"`
			} `json:"session"`
			Changed []string `json:"changed"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &upd); err != nil || res.Exit != 0 || !upd.OK ||
		string(upd.Result.Session.Extra) != `{"ticket":"auth-3","ratio":1.10,"status":"review"}` || !slices.Equal(upd.Result.Changed, []string{"extra"}) {
		t.Fatalf("exit %d, stdout %q, stderr %q: %v", res.Exit, res.Stdout, res.Stderr, err)
	}
	const updated = `{"ticket":"auth-3","ratio":1.10,"status":"review"}`
	if out := listed(sid); !strings.Contains(out, `"extra":`+updated) {
		t.Errorf("list after update: %s", out)
	}
	// The same update again changes nothing, and the file is untouched.
	before, _ := os.ReadFile(sesshinPath(h))
	res = h.Sesshin("update", "1", "--extra-merge", `{"status":"review"}`)
	if err := json.Unmarshal([]byte(res.Stdout), &upd); err != nil || res.Exit != 0 || len(upd.Result.Changed) != 0 {
		t.Fatalf("exit %d, stdout %q: %v", res.Exit, res.Stdout, err)
	}
	if after, _ := os.ReadFile(sesshinPath(h)); string(after) != string(before) {
		t.Error("an update that changed nothing rewrote the file")
	}
	// No option is invalid input; self outside any session is not found.
	if res := h.Sesshin("update", "1"); res.Exit != 1 || !strings.Contains(res.Stdout, `"kind":"invalid-input"`) {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if res := h.Sesshin("update", "self", "--extra-merge", `{"a":1}`); res.Exit != 1 || !strings.Contains(res.Stdout, `"kind":"not-found"`) {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}

	// Resume: the session keeps its extra, whatever the reservation holds.
	h.KittyWindow("unix:/e2e/kitty", "3")
	h.Setenv("SHELL", "/bin/zsh")
	h.Kitten("9")
	env, res = sesshinResume(t, h, "1", "--start-timeout-secs", "0")
	if res.Exit != 0 || !env.OK {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	for _, call := range h.KittenCalls()[1:] {
		for _, a := range call {
			if strings.Contains(a, "SESSHIN_EXTRA") {
				t.Errorf("resume passed %q", a)
			}
		}
	}
	rr, ok := readReservation(t, h, "api")
	if !ok || rr.Extra == nil || rr.Extra.Len() != 0 {
		t.Fatalf("resume's reservation %+v, %v", rr, ok)
	}
	h.Setenv("SESSHIN_TOKEN", rr.Token)
	start(t, h, "resume", `"cwd":"`+h.Home+`"`, `"transcript_path":"`+transcript+`"`)
	quiet(t, h.Hook("user-prompt-submit", event("UserPromptSubmit", `"prompt":"hi"`)))
	if out := listed(sid); !strings.Contains(out, `"extra":`+updated) {
		t.Errorf("list after the resume: %s", out)
	}
}

// A session started without a reservation has {}; no hook reads extra from
// the environment.
func TestExtraNotFromEnvironment(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.Setenv("SESSHIN_EXTRA", `{"a":1}`)
	start(t, h, "startup")
	if got := readSesshin(t, h); got.Extra == nil || got.Extra.Len() != 0 {
		t.Errorf("extra %+v", got.Extra)
	}
	noLog(t, h)
}
