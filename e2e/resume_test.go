package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sesshinResume(t *testing.T, h *Harness, args ...string) (spawnEnvelope, Result) {
	t.Helper()
	res := h.Sesshin(append([]string{"resume"}, args...)...)
	var env spawnEnvelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil || strings.Count(res.Stdout, "\n") != 1 {
		t.Fatalf("exit %d, stdout %q, stderr %q: %v", res.Exit, res.Stdout, res.Stderr, err)
	}
	return env, res
}

// endedSession is session #1, ended: started in kitty window 7 in h.Home with a
// transcript, its tab title and user variables as the sync would have left
// them, and job set ("" for none). The harness is then in the caller's window.
func endedSession(t *testing.T, h *Harness, job string) {
	t.Helper()
	transcript := filepath.Join(h.Home, "t.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.KittyWindow(kittySocket, "7")
	start(t, h, "startup", `"cwd":"`+h.Home+`"`, `"transcript_path":"`+transcript+`"`)
	jobJSON := "null"
	if job != "" {
		jobJSON = `"` + job + `"`
	}
	writeSesshin(t, h, `{"schema":1,"id":1,"job":`+jobJSON+`,"source":"spawn","placement":{"terminal":"kitty","socket":"`+kittySocket+
		`","window_id":7,"tab_title":"api review","user_vars":{"project":"api"}}}`)
	quiet(t, h.Hook("session-end", event("SessionEnd", `"reason":"other"`)))
	h.KittyWindow("unix:/e2e/kitty", "3")
	h.Setenv("SHELL", "/bin/zsh")
	h.Kitten("9")
}

// resume with the fake kitten answering a window ID, start_timeout_secs 0: the
// reservation is claimed and placed, the tab opens under the stored title and
// variables, and a second resume is refused while it is fresh.
func TestResume(t *testing.T) {
	t.Parallel()
	h := New(t)
	endedSession(t, h, "api")
	env, res := sesshinResume(t, h, "1", "--start-timeout-secs", "0", "--", "--model", "opus")
	if res.Exit != 0 || res.Stderr != "" || !env.OK || len(env.Warnings) != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	const placement = `{"terminal":"kitty","socket":"unix:/e2e/kitty","window_id":9}`
	if env.Result.Job == nil || *env.Result.Job != "api" || string(env.Result.Placement) != placement || string(env.Result.Session) != "null" {
		t.Errorf("result %+v", env.Result)
	}
	r, ok := readReservation(t, h, "api")
	if !ok {
		t.Fatal("no reservation")
	}
	want := []string{"@", "--to", "unix:/e2e/kitty", "launch", "--type=tab", "--self", "--keep-focus", "--cwd=" + h.Home,
		"--tab-title=api review", "--var=project=api",
		"--env=SESSHIN_JOB=api", "--env=SESSHIN_TOKEN=" + r.Token,
		"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--resume", sid, "--model", "opus"}
	if got := h.KittenCalls(); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Errorf("kitten calls %q\nwant %q", got, want)
	}

	// The job is held until the session starts or the reservation strands.
	env, res = sesshinResume(t, h, "1", "--start-timeout-secs", "0")
	if res.Exit != 1 || env.OK || env.Error == nil || env.Error.Kind != "conflict" || env.Error.Details["rule"] != "job-taken" {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	// Another job is free, and the job selector finds the session.
	env, res = sesshinResume(t, h, "api", "--job", "api-old", "--start-timeout-secs", "0")
	if res.Exit != 0 || !env.OK || env.Result.Job == nil || *env.Result.Job != "api-old" {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
}

// The resumed session's own session-start adopts the reservation, takes the
// job, and keeps the tab title and variables though it runs in another window.
func TestResumeStartsKeepingTitle(t *testing.T) {
	t.Parallel()
	h := New(t)
	endedSession(t, h, "api")
	_, res := sesshinResume(t, h, "1", "--job", "api-old", "--start-timeout-secs", "0")
	if res.Exit != 0 {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	r, _ := readReservation(t, h, "api-old")
	h.KittyWindow("unix:/e2e/kitty", "9")
	h.Setenv("SESSHIN_JOB", "api-old")
	h.Setenv("SESSHIN_TOKEN", r.Token)
	start(t, h, "resume")
	sesshin := readSesshin(t, h)
	if sesshin.Job == nil || *sesshin.Job != "api-old" || sesshin.ID == nil || *sesshin.ID != 1 {
		t.Errorf("sesshin.json %+v", sesshin)
	}
	want := `{"terminal":"kitty","socket":"unix:/e2e/kitty","window_id":9,"tab_title":"api review","user_vars":{"project":"api"}}`
	if got := readSesshinPlacement(t, h); got != want {
		t.Errorf("placement %s, want %s", got, want)
	}
	if _, ok := readReservation(t, h, "api-old"); ok {
		t.Error("the reservation was kept")
	}
}

// Errors before anything is launched or written.
func TestResumeErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		args  []string
		setup func(h *Harness)
		exit  int
		kind  string
	}{
		{"no such session", []string{"99"}, nil, 1, "not-found"},
		{"no such job", []string{"nope"}, nil, 1, "not-found"},
		{"bad selector", []string{"Bad Selector"}, nil, 1, "invalid-input"},
		{"bad job", []string{"1", "--job", "12"}, nil, 1, "invalid-input"},
		{"no selector", nil, nil, 2, "usage"},
		{"not in kitty", []string{"1"}, func(h *Harness) { h.Unsetenv("KITTY_LISTEN_ON") }, 1, "terminal"},
		{"under tmux", []string{"1"}, func(h *Harness) { h.Setenv("TMUX", "/tmp/tmux,1,0") }, 1, "terminal"},
		{"corrupt config", []string{"1"}, func(h *Harness) { writeConfig(t, h, "spawn_shell = []") }, 1, "corrupt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			endedSession(t, h, "api")
			if tc.setup != nil {
				tc.setup(h)
			}
			env, res := sesshinResume(t, h, tc.args...)
			if tc.kind == "usage" {
				if res.Exit != tc.exit {
					t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
				}
				return
			}
			if res.Exit != tc.exit || env.OK || env.Error == nil || env.Error.Kind != tc.kind {
				t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
			}
			if len(h.KittenCalls()) != 0 {
				t.Errorf("kitten was run: %q", h.KittenCalls())
			}
			if _, err := os.Stat(filepath.Join(h.Loc.StateDir, "reservations")); err == nil {
				t.Error("a reservation was made")
			}
		})
	}
}
