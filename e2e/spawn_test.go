package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/model"
)

// kittySpawnHarness is a harness run from a kitty window with remote control
// on, whose fake kitten answers a launch with window 7.
func kittySpawnHarness(t *testing.T) *Harness {
	t.Helper()
	h := New(t)
	h.Setenv("KITTY_LISTEN_ON", "unix:/e2e/kitty")
	h.Setenv("KITTY_WINDOW_ID", "3")
	h.Setenv("SHELL", "/bin/zsh") // never run: the fake kitten launches nothing
	h.Kitten("7")
	return h
}

type spawnEnvelope struct {
	OK     bool `json:"ok"`
	Result struct {
		Job       *string         `json:"job"`
		Placement json.RawMessage `json:"placement"`
		Session   json.RawMessage `json:"session"`
	} `json:"result"`
	Error *struct {
		Kind    string         `json:"kind"`
		Details map[string]any `json:"details"`
	} `json:"error"`
	Warnings []struct {
		Kind    string         `json:"kind"`
		Details map[string]any `json:"details"`
	} `json:"warnings"`
}

func sesshinSpawn(t *testing.T, h *Harness, args ...string) (spawnEnvelope, Result) {
	t.Helper()
	res := h.Sesshin(append([]string{"spawn"}, args...)...)
	var env spawnEnvelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil || strings.Count(res.Stdout, "\n") != 1 {
		t.Fatalf("exit %d, stdout %q, stderr %q: %v", res.Exit, res.Stdout, res.Stderr, err)
	}
	return env, res
}

// readReservation reads the one reservation of the job's key (the job-less
// one when job is ""), and whether it is there.
func readReservation(t *testing.T, h *Harness, job string) (model.ReservationFile, bool) {
	t.Helper()
	dir := filepath.Join(h.Loc.StateDir, "reservations")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return model.ReservationFile{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		if key, _, ok := model.ParseReservationName(e.Name()); ok && key == model.JobKey(job) {
			names = append(names, e.Name())
		}
	}
	switch len(names) {
	case 0:
		return model.ReservationFile{}, false
	case 1:
	default:
		t.Fatalf("reservations of %q: %v", job, names)
	}
	b, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	r, res := model.ReadReservation(b, names[0])
	if !res.Usable {
		t.Fatalf("reservation unusable: %s", res.Reason())
	}
	return r, true
}

// spawn with the fake kitten answering a window ID, start_timeout_secs 0: the
// reservation is written and then given its placement, the envelope says
// where, and a second spawn of the job is refused (operations.md, spawn).
func TestSpawn(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	args := []string{"--job", "api", "--cwd", h.Home, "--prompt", "fix $(it)", "--var", "project=api",
		"--start-timeout-secs", "0", "--", "--model", "opus"}
	env, res := sesshinSpawn(t, h, args...)
	if res.Exit != 0 || res.Stderr != "" || !env.OK || len(env.Warnings) != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	const placement = `{"terminal":"kitty","socket":"unix:/e2e/kitty","window_id":7}`
	if env.Result.Job == nil || *env.Result.Job != "api" || string(env.Result.Placement) != placement || string(env.Result.Session) != "null" {
		t.Errorf("result %+v", env.Result)
	}

	r, ok := readReservation(t, h, "api")
	if !ok {
		t.Fatal("no reservation")
	}
	pl, _ := json.Marshal(r.Placement)
	if r.Job == nil || *r.Job != "api" || !model.IsToken(r.Token) || string(pl) != placement {
		t.Errorf("reservation %+v placement %s", r, pl)
	}

	want := []string{"@", "--to", "unix:/e2e/kitty", "launch", "--type=tab", "--self", "--keep-focus", "--cwd=" + h.Home,
		"--tab-title=api", "--var=project=api",
		"--env=SESSHIN_JOB=api", "--env=SESSHIN_TOKEN=" + r.Token,
		"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--name", "api", "--model", "opus", "--", "fix $(it)"}
	if got := h.KittenCalls(); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Errorf("kitten calls %q\nwant %q", got, want)
	}
	// The window's own environment: sesshin never passes the caller's along.
	for _, a := range h.KittenCalls()[0] {
		if strings.Contains(a, "copy-env") {
			t.Errorf("argument %q", a)
		}
	}

	// A second spawn of the job is refused while the reservation is fresh, and
	// opens nothing: it only asks about the window (the fake's answer isn't
	// kitten @ ls's, so none, and the reservation is judged by age).
	env, res = sesshinSpawn(t, h, args...)
	if res.Exit != 1 || env.OK || env.Error == nil || env.Error.Kind != "conflict" ||
		env.Error.Details["rule"] != "job-taken" || !reflect.DeepEqual(env.Error.Details["sessions"], []any{}) {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	calls := h.KittenCalls()
	if len(calls) != 2 || !slices.Equal(calls[1], []string{"@", "--to", "unix:/e2e/kitty", "ls"}) {
		t.Errorf("kitten calls %q", calls)
	}
	if again, _ := readReservation(t, h, "api"); again.Token != r.Token {
		t.Error("the refused spawn changed the reservation")
	}
	// A different job is free.
	env, res = sesshinSpawn(t, h, "--job", "web", "--cwd", h.Home, "--start-timeout-secs", "0")
	if res.Exit != 0 || !env.OK {
		t.Errorf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
}

// With no job: the launch names none, and still reserves, so the session has
// its extra: <token>.json, a null job, and only SESSHIN_TOKEN in the window.
func TestSpawnWithoutJob(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	env, res := sesshinSpawn(t, h, "--cwd", h.Home, "--type", "split", "--name", "not a tab title for a split", "--start-timeout-secs", "0")
	if res.Exit != 0 || res.Stderr != "" || !env.OK || env.Result.Job != nil {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	r, ok := readReservation(t, h, "")
	if !ok || r.Job != nil || !model.IsToken(r.Token) || r.Placement == nil {
		t.Fatalf("reservation %+v, %v", r, ok)
	}
	if _, err := os.Stat(filepath.Join(h.Loc.StateDir, "reservations", r.Token+".json")); err != nil {
		t.Errorf("reservation file: %v", err)
	}
	calls := h.KittenCalls()
	if len(calls) != 1 || !slices.Contains(calls[0], "--type=window") || !slices.Contains(calls[0], "--env=SESSHIN_TOKEN="+r.Token) ||
		slices.ContainsFunc(calls[0], func(a string) bool {
			return strings.HasPrefix(a, "--tab-title") || strings.HasPrefix(a, "--env=SESSHIN_JOB=")
		}) {
		t.Errorf("kitten calls %q", calls)
	}
}

// A launch the backend refuses removes the reservation at once.
func TestSpawnLaunchFailed(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	h.Unsetenv(kittenEnv) // unconfigured: the fake exits 1
	env, res := sesshinSpawn(t, h, "--job", "api", "--cwd", h.Home)
	if res.Exit != 1 || env.OK || env.Error == nil || env.Error.Kind != "terminal" || env.Error.Details["reason"] != "launch-failed" || env.Error.Details["terminal"] != "kitty" {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if _, ok := readReservation(t, h, "api"); ok {
		t.Error("the reservation was kept")
	}
	// An answer that names no window may have opened one: kept.
	h.Kitten("not a window")
	env, res = sesshinSpawn(t, h, "--job", "api", "--cwd", h.Home)
	if res.Exit != 1 || env.Error == nil || env.Error.Details["reason"] != "launch-unknown" {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if r, ok := readReservation(t, h, "api"); !ok || r.Placement != nil {
		t.Errorf("reservation %+v, %v", r, ok)
	}
}

// A session that never starts: success, with not-started.
func TestSpawnNotStarted(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	env, res := sesshinSpawn(t, h, "--job", "api", "--cwd", h.Home, "--start-timeout-secs", "1")
	if res.Exit != 0 || !env.OK || string(env.Result.Session) != "null" || len(env.Warnings) != 1 || env.Warnings[0].Kind != "not-started" {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if res.Stderr != "sesshin: 1 warning (see .warnings in the output)\n" {
		t.Errorf("stderr %q", res.Stderr)
	}
	if d := env.Warnings[0].Details; d["job"] != "api" || d["waited_secs"] != float64(1) {
		t.Errorf("details %+v", d)
	}
	if res.Duration < 900_000_000 {
		t.Errorf("returned after %v", res.Duration)
	}
}

// Errors before anything is launched or written.
func TestSpawnErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		args  func(h *Harness) []string
		setup func(h *Harness)
		exit  int
		kind  string
	}{
		{"no cwd", func(h *Harness) []string { return []string{"--cwd", filepath.Join(h.Home, "missing")} }, nil, 1, "not-found"},
		{"~/ is HOME", func(h *Harness) []string { return []string{"--cwd", "~/missing"} }, nil, 1, "not-found"},
		{"bad job", func(h *Harness) []string { return []string{"--cwd", h.Home, "--job", "12"} }, nil, 1, "invalid-input"},
		{"bad var", func(h *Harness) []string { return []string{"--cwd", h.Home, "--var", "x"} }, nil, 1, "invalid-input"},
		{"both prompts", func(h *Harness) []string {
			return []string{"--cwd", h.Home, "--prompt", "a", "--prompt-file", "/dev/null"}
		}, nil, 2, "usage"},
		{"prompt file", func(h *Harness) []string {
			return []string{"--cwd", h.Home, "--prompt-file", filepath.Join(h.Home, "missing")}
		}, nil, 1, "io"},
		{"not in kitty", func(h *Harness) []string { return []string{"--cwd", h.Home} },
			func(h *Harness) { h.Unsetenv("KITTY_LISTEN_ON") }, 1, "terminal"},
		{"under tmux", func(h *Harness) []string { return []string{"--cwd", h.Home} },
			func(h *Harness) { h.Setenv("TMUX", "/tmp/tmux,1,0") }, 1, "terminal"},
		{"unusable HOME", func(h *Harness) []string { return []string{"--cwd", "/"} },
			func(h *Harness) { h.Setenv("HOME", "relative") }, 1, "environment"},
		{"corrupt config", func(h *Harness) []string { return []string{"--cwd", h.Home} },
			func(h *Harness) { writeConfig(t, h, "spawn_shell = []") }, 1, "corrupt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := kittySpawnHarness(t)
			if tc.setup != nil {
				tc.setup(h)
			}
			env, res := sesshinSpawn(t, h, tc.args(h)...)
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

func writeConfig(t *testing.T, h *Harness, content string) {
	t.Helper()
	if err := os.MkdirAll(h.Loc.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Loc.ConfigDir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The prompt file, and --input, reach the launch the same way.
func TestSpawnPromptFileAndInput(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	prompt := filepath.Join(h.Home, "prompt.txt")
	body := "first line\n  -second `line`\n"
	if err := os.WriteFile(prompt, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, res := sesshinSpawn(t, h, "--cwd", h.Home, "--prompt-file", prompt, "--start-timeout-secs", "0")
	if res.Exit != 0 {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	in, _ := json.Marshal(map[string]any{"cwd": h.Home, "prompt": body, "start_timeout_secs": 0})
	input := filepath.Join(h.Home, "in.json")
	if err := os.WriteFile(input, in, 0o600); err != nil {
		t.Fatal(err)
	}
	_, res = sesshinSpawn(t, h, "-i", input)
	if res.Exit != 0 {
		t.Fatalf("exit %d, stdout %q", res.Exit, res.Stdout)
	}
	calls := h.KittenCalls()
	if len(calls) != 2 {
		t.Fatalf("kitten calls %q", calls)
	}
	// The calls differ by the reservation's token alone.
	for i := range calls {
		calls[i] = slices.DeleteFunc(calls[i], func(a string) bool { return strings.HasPrefix(a, "--env=SESSHIN_TOKEN=") })
	}
	if !slices.Equal(calls[0], calls[1]) || calls[0][len(calls[0])-1] != body || calls[0][len(calls[0])-2] != "--" {
		t.Errorf("kitten calls %q", calls)
	}
}
