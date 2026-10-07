package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const extraText = `{"shingi-unit":"auth-3","koan-task":57,"ratio":1.10}`

// spawn --extra: the fake kitten is given SESSHIN_EXTRA as compact JSON, in
// the order and with the numbers as given, after the job's variables, and
// with or without a job; without --extra, none (operations.md, spawn).
func TestSpawnExtra(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	env, res := sesshinSpawn(t, h, "--job", "api", "--cwd", h.Home, "--start-timeout-secs", "0",
		"--extra", `{ "shingi-unit" : "auth-3", "koan-task": 57, "ratio": 1.10 }`)
	if res.Exit != 0 || !env.OK {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	r, _ := readReservation(t, h, "api")
	want := []string{"--env=SESSHIN_JOB=api", "--env=SESSHIN_TOKEN=" + r.Token, "--env=SESSHIN_EXTRA=" + extraText}
	calls := h.KittenCalls()
	i := slices.Index(calls[0], want[0])
	if i < 0 || !slices.Equal(calls[0][i:i+3], want) {
		t.Errorf("kitten call %q, want it to hold %q", calls[0], want)
	}

	env, res = sesshinSpawn(t, h, "--cwd", h.Home, "--start-timeout-secs", "0", "--extra", `{}`)
	if res.Exit != 0 || !env.OK {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if call := h.KittenCalls()[1]; !slices.Contains(call, "--env=SESSHIN_EXTRA={}") || slices.Contains(call, "--env=SESSHIN_JOB=api") {
		t.Errorf("kitten call %q", call)
	}

	env, res = sesshinSpawn(t, h, "--cwd", h.Home, "--start-timeout-secs", "0")
	if res.Exit != 0 || !env.OK {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	for _, a := range h.KittenCalls()[2] {
		if strings.Contains(a, "SESSHIN_EXTRA") {
			t.Errorf("argument %q without --extra", a)
		}
	}

	// A bad value is refused before anything is launched.
	n := len(h.KittenCalls())
	env, res = sesshinSpawn(t, h, "--cwd", h.Home, "--extra", `{"a":`)
	if res.Exit != 1 || env.OK || env.Error == nil || env.Error.Kind != "invalid-input" || len(h.KittenCalls()) != n {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
}

// A session made by sesshin-hook under SESSHIN_EXTRA has it in sesshin.json
// and in list --fields extra, as written; it survives the session's end, a
// resume (which passes no SESSHIN_EXTRA, and whose session-start finds another
// in its environment) and later hooks.
func TestExtraSurvives(t *testing.T) {
	t.Parallel()
	h := New(t)
	transcript := filepath.Join(h.Home, "t.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.KittyWindow(kittySocket, "7")
	h.Setenv("SESSHIN_EXTRA", extraText)
	start(t, h, "startup", `"cwd":"`+h.Home+`"`, `"transcript_path":"`+transcript+`"`)
	if got := readSesshin(t, h); got.Extra == nil || got.Extra.Len() != 3 {
		t.Fatalf("extra %+v", got.Extra)
	}

	listed := func() string {
		t.Helper()
		res := h.Sesshin("list", "--liveness", "all", "--fields", "extra")
		if res.Exit != 0 {
			t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
		}
		return res.Stdout
	}
	const member = `"extra":` + extraText
	if out := listed(); !strings.Contains(out, member) {
		t.Errorf("list: %s", out)
	}

	quiet(t, h.Hook("session-end", event("SessionEnd", `"reason":"other"`)))
	if out := listed(); !strings.Contains(out, member) {
		t.Errorf("list after the end: %s", out)
	}

	h.KittyWindow("unix:/e2e/kitty", "3")
	h.Setenv("SHELL", "/bin/zsh")
	h.Kitten("9")
	env, res := sesshinResume(t, h, "1", "--start-timeout-secs", "0")
	if res.Exit != 0 || !env.OK {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	for _, a := range h.KittenCalls()[0] {
		if strings.Contains(a, "SESSHIN_EXTRA") {
			t.Errorf("resume passed %q", a)
		}
	}

	// The resumed session's hooks find another SESSHIN_EXTRA, and ignore it.
	h.Setenv("SESSHIN_EXTRA", `{"other":true}`)
	start(t, h, "resume", `"cwd":"`+h.Home+`"`, `"transcript_path":"`+transcript+`"`)
	quiet(t, h.Hook("user-prompt-submit", event("UserPromptSubmit", `"prompt":"hi"`)))
	if out := listed(); !strings.Contains(out, member) {
		t.Errorf("list after the resume: %s", out)
	}
	b, _ := os.ReadFile(sesshinPath(h))
	if !strings.Contains(string(b), "\"extra\": {\n    \"shingi-unit\": \"auth-3\",\n    \"koan-task\": 57,\n    \"ratio\": 1.10\n  }") {
		t.Errorf("sesshin.json:\n%s", b)
	}
}

// A SESSHIN_EXTRA that is unusable is ignored, and session-start logs it
// (the log is the hook's own, not its output).
func TestExtraBadValue(t *testing.T) {
	t.Parallel()
	h := New(t)
	h.Setenv("SESSHIN_EXTRA", `[1]`)
	start(t, h, "startup")
	if got := readSesshin(t, h); got.Extra == nil || got.Extra.Len() != 0 {
		t.Errorf("extra %+v", got.Extra)
	}
	if log := hooksLog(h); !strings.Contains(log, "SESSHIN_EXTRA") {
		t.Errorf("log %q", log)
	}
}
