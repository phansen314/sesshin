package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// setupEnvelope is install's and uninstall's output, as much as these tests
// read.
type setupEnvelope struct {
	OK     bool `json:"ok"`
	Result struct {
		DryRun       bool      `json:"dry_run"`
		HookBinary   string    `json:"hook_binary"`
		SettingsPath string    `json:"settings_path"`
		ProposalPath *string   `json:"proposal_path"`
		Apply        *[]string `json:"apply"`
		Changes      []struct {
			What   string `json:"what"`
			Action string `json:"action"`
		} `json:"changes"`
	} `json:"result"`
	Error *struct {
		Kind    string         `json:"kind"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
	Warnings []struct {
		Kind string `json:"kind"`
	} `json:"warnings"`
}

// sesshinSetup runs sesshin with args and decodes its one-line envelope.
func sesshinSetup(t *testing.T, h *Harness, args ...string) (setupEnvelope, Result) {
	t.Helper()
	res := h.Sesshin(args...)
	var env setupEnvelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil || strings.Count(res.Stdout, "\n") != 1 {
		t.Fatalf("exit %d, stdout %q, stderr %q: %v", res.Exit, res.Stdout, res.Stderr, err)
	}
	return env, res
}

// requireIdentified skips a test that needs install to accept sesshin-hook: a
// build with no VCS information and no module version can't be identified
// (operations.md, install step 2).
func requireIdentified(t *testing.T, h *Harness) {
	t.Helper()
	res := h.Sesshin("version")
	if strings.Contains(res.Stdout, `"commit":null`) && strings.Contains(res.Stdout, `"version":"(devel)"`) {
		t.Skip("this build has no VCS information, so install refuses it")
	}
}

// seedSettings writes Claude Code's settings.json for the harness.
func seedSettings(t *testing.T, h *Harness, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(h.Loc.ClaudeSettings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.Loc.ClaudeSettings, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// apply runs a command of the output's apply list, through sh.
func apply(t *testing.T, h *Harness, command string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = h.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 || !strings.HasPrefix(command, "diff") {
			t.Fatalf("%s: %v\n%s", command, err, out)
		}
	}
	return string(out)
}

const seededSettings = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "/usr/local/bin/audit" }] }
    ]
  },
  "permissions": {
    "allow": [
      "Bash(koan:*)"
    ]
  }
}
`

// install proposes, applying it wires sesshin, uninstall proposes the way out,
// and another tool's entry survives both (cli-spec.md, install's Examples).
func TestInstallUninstall(t *testing.T) {
	h := New(t)
	requireIdentified(t, h)
	seedSettings(t, h, seededSettings)

	env, res := sesshinSetup(t, h, "install")
	if res.Exit != 0 || !env.OK || res.Stderr != "" {
		t.Fatalf("exit %d: %s %s", res.Exit, res.Stdout, res.Stderr)
	}
	hook, err := filepath.EvalSymlinks(filepath.Join(bin, "sesshin-hook"))
	if err != nil {
		t.Fatal(err)
	}
	r := env.Result
	if r.HookBinary != hook || r.SettingsPath != h.Loc.ClaudeSettings || r.DryRun || r.ProposalPath == nil || r.Apply == nil || len(*r.Apply) != 2 {
		t.Fatalf("%+v", r)
	}
	if *r.ProposalPath != filepath.Join(h.Loc.StateDir, "settings.proposed.json") || len(r.Changes) != 18 {
		t.Errorf("%+v", r)
	}
	for _, p := range []string{*r.ProposalPath, filepath.Join(h.Loc.StateDir, "install.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Error(err)
		}
	}
	// The self-test left nothing of its own in the state directory.
	if _, err := os.Stat(filepath.Join(h.Loc.StateDir, "sessions")); !os.IsNotExist(err) {
		t.Errorf("sessions: %v", err)
	}
	if got, _ := os.ReadFile(h.Loc.ClaudeSettings); string(got) != seededSettings {
		t.Errorf("install wrote settings.json:\n%s", got)
	}
	if d := apply(t, h, (*r.Apply)[0]); !strings.Contains(d, "+++") || !strings.Contains(d, "sesshin-hook") {
		t.Errorf("diff:\n%s", d)
	}

	apply(t, h, (*r.Apply)[1])
	env, res = sesshinSetup(t, h, "install", "--dry-run")
	if res.Exit != 0 || !env.OK || !env.Result.DryRun || env.Result.ProposalPath != nil || env.Result.Apply != nil || len(env.Result.Changes) != 18 {
		t.Fatalf("%s", res.Stdout)
	}
	for _, c := range env.Result.Changes {
		if c.Action != "unchanged" {
			t.Errorf("after applying: %+v", c)
		}
	}
	if last := env.Result.Changes[17]; last.What != "permissions.ask:Bash(sesshin prune:*)" {
		t.Errorf("the last item: %+v", last)
	}
	wired := settingsTree(t, h)
	allow := mustGet(t, mustObject(t, wired, "permissions"), "allow").([]any)
	if len(allow) != 3 || allow[0] != "Bash(koan:*)" || allow[1] != "Bash(sesshin:*)" || allow[2] != "Bash(jq:*)" {
		t.Errorf("permissions.allow %v", allow)
	}
	if cmd, _ := mustGet(t, mustObject(t, wired, "statusLine"), "command").(string); cmd != shQuote(hook)+" statusline" {
		t.Errorf("statusLine command %q", cmd)
	}

	// The hooks it wired run: the first of them records a session.
	if res := h.Hook("session-start", event("SessionStart", `"source":"startup"`)); res.Exit != 0 {
		t.Errorf("%+v", res)
	}

	env, res = sesshinSetup(t, h, "uninstall")
	if res.Exit != 0 || !env.OK || env.Result.SettingsPath != h.Loc.ClaudeSettings || env.Result.ProposalPath == nil || len(env.Result.Changes) != 17 {
		t.Fatalf("%s %s", res.Stdout, res.Stderr)
	}
	for _, c := range env.Result.Changes {
		if c.Action != "removed" {
			t.Errorf("%+v", c)
		}
	}
	apply(t, h, (*env.Result.Apply)[1])
	got, _ := os.ReadFile(h.Loc.ClaudeSettings)
	if strings.Contains(string(got), "sesshin-hook") || strings.Contains(string(got), "statusLine") || strings.Contains(string(got), "Bash(sesshin") {
		t.Errorf("sesshin remains:\n%s", got)
	}
	// Bash(jq:*) is shared, and install added it: uninstall leaves it, beside
	// the other tool's rule.
	wantSettings := strings.ReplaceAll(seededSettings, "\"Bash(koan:*)\"\n    ]", "\"Bash(koan:*)\",\n      \"Bash(jq:*)\"\n    ]")
	if string(got) != strings.ReplaceAll(wantSettings, `{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "/usr/local/bin/audit" }] }`,
		"{\n        \"matcher\": \"Bash\",\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"/usr/local/bin/audit\"\n          }\n        ]\n      }") {
		t.Errorf("the other tool's entry changed:\n%s", got)
	}
	env, _ = sesshinSetup(t, h, "uninstall", "--dry-run")
	if !env.OK || len(env.Result.Changes) != 0 {
		t.Errorf("%+v", env.Result)
	}
	// The state directory keeps what sesshin recorded.
	if _, err := os.Stat(filepath.Join(h.Loc.StateDir, "install.json")); err != nil {
		t.Error(err)
	}
}

func settingsTree(t *testing.T, h *Harness) *jsonio.Object {
	t.Helper()
	b, err := os.ReadFile(h.Loc.ClaudeSettings)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := jsonio.ParseValue(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonio.Object)
}

func mustGet(t *testing.T, o *jsonio.Object, key string) any {
	t.Helper()
	v, ok := o.Get(key)
	if !ok {
		t.Fatalf("no %s", key)
	}
	return v
}

func mustObject(t *testing.T, o *jsonio.Object, key string) *jsonio.Object {
	t.Helper()
	return mustGet(t, o, key).(*jsonio.Object)
}

// goBuild builds ./cmd/<name> into dir with extra flags.
func goBuild(t *testing.T, dir, name string, flags ...string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", append(append([]string{"build"}, flags...), "-o", out, "./cmd/"+name)...)
	cmd.Dir = ".."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", name, err, b)
	}
	return out
}

// selfTestFailure runs install with the harness's sesshin path and checks that it
// failed as self-test-failed, writing nothing, and returns the details.
func selfTestFailure(t *testing.T, h *Harness) map[string]any {
	t.Helper()
	env, res := sesshinSetup(t, h, "install")
	if res.Exit != 1 || env.OK || env.Error == nil || env.Error.Kind != "self-test-failed" || !strings.HasPrefix(res.Stderr, "sesshin: self-test-failed: ") {
		t.Fatalf("exit %d: %s %s", res.Exit, res.Stdout, res.Stderr)
	}
	if _, err := os.Stat(h.Loc.StateDir); !os.IsNotExist(err) {
		t.Errorf("a failed self-test wrote the state directory: %v", err)
	}
	return env.Error.Details
}

// sesshin copied alone to a directory has no sesshin-hook beside it.
func TestInstallNoHook(t *testing.T) {
	h := New(t)
	dir := t.TempDir()
	if err := copyFile(h.SesshinPath, filepath.Join(dir, "sesshin")); err != nil {
		t.Fatal(err)
	}
	h.SesshinPath = filepath.Join(dir, "sesshin")
	d := selfTestFailure(t, h)
	want, _ := filepath.EvalSymlinks(dir)
	if d["hook"] != nil || d["path"] != filepath.Join(want, "sesshin-hook") || !strings.Contains(d["detail"].(string), "not beside sesshin") {
		t.Errorf("%+v", d)
	}
}

// A sesshin-hook that isn't a Go binary, one built without version information,
// and one of another build than sesshin's are all refused before anything runs.
func TestInstallForeignHook(t *testing.T) {
	h := New(t)
	requireIdentified(t, h)
	dir := t.TempDir()
	sesshin := filepath.Join(dir, "sesshin")
	hook := filepath.Join(dir, "sesshin-hook")
	if err := copyFile(h.SesshinPath, sesshin); err != nil {
		t.Fatal(err)
	}
	h.SesshinPath = sesshin
	resolved, _ := filepath.EvalSymlinks(dir)

	check := func(name, detail string) {
		t.Helper()
		d := selfTestFailure(t, h)
		if d["hook"] != nil || d["path"] != filepath.Join(resolved, "sesshin-hook") || !strings.Contains(d["detail"].(string), detail) {
			t.Errorf("%s: %+v", name, d)
		}
	}

	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	check("a script", "cannot be identified")

	os.Remove(hook)
	built := goBuild(t, t.TempDir(), "sesshin-hook", "-buildvcs=false")
	if err := copyFile(built, hook); err != nil {
		t.Fatal(err)
	}
	check("no version information", "cannot be identified")

	// Another build: sesshin itself has no version information, and sesshin-hook
	// (the harness's) has.
	os.Remove(sesshin)
	if err := copyFile(goBuild(t, t.TempDir(), "sesshin", "-buildvcs=false"), sesshin); err != nil {
		t.Fatal(err)
	}
	os.Remove(hook)
	if err := copyFile(filepath.Join(bin, "sesshin-hook"), hook); err != nil {
		t.Fatal(err)
	}
	check("another build", "from another build")
}

// A sesshin-hook that does nothing, as the sesshintest build does when told to panic
// before every verb (it still exits 0, silently), fails the self-test, which
// finds no lifecycle.json.
func TestInstallBrokenHook(t *testing.T) {
	h := New(t)
	requireIdentified(t, h)
	h.UseSesshintest()
	h.Setenv("SESSHIN_TEST_PANIC", "dispatch")
	d := selfTestFailure(t, h)
	if d["hook"] != "session-start" || !strings.Contains(d["detail"].(string), "lifecycle.json was not written") {
		t.Errorf("%+v", d)
	}
}
