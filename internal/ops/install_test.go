package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

type installResult struct {
	DryRun       bool         `json:"dry_run"`
	HookBinary   string       `json:"hook_binary"`
	SettingsPath string       `json:"settings_path"`
	ProposalPath *string      `json:"proposal_path"`
	Apply        *[]string    `json:"apply"`
	Changes      []ChangeItem `json:"changes"`
}

func installOut(t *testing.T, f *fixture, dry bool) installResult {
	t.Helper()
	return result[installResult](t, Install(InstallInput{DryRun: dry}, f.s), "install-output")
}

// A first install records install.json, writes the proposal, and gives the
// two commands; applying the proposal makes every item unchanged, and a dry
// run then writes nothing.
func TestInstall(t *testing.T) {
	f := newFixture(t)
	out := installOut(t, f, false)
	if out.DryRun || out.HookBinary != fixtureHook || out.SettingsPath != f.settings() {
		t.Errorf("%+v", out)
	}
	proposal := filepath.Join(f.state(), "settings.proposed.json")
	if out.ProposalPath == nil || *out.ProposalPath != proposal {
		t.Fatalf("proposal_path %v", out.ProposalPath)
	}
	q := func(p string) string { return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'" }
	wantApply := []string{
		"diff -uN " + q(f.settings()) + " " + q(proposal),
		"cat " + q(proposal) + " > " + q(f.settings()),
	}
	if out.Apply == nil || strings.Join(*out.Apply, "\n") != strings.Join(wantApply, "\n") {
		t.Errorf("apply %v, want %v", out.Apply, wantApply)
	}
	if len(out.Changes) != 18 {
		t.Fatalf("%d changes", len(out.Changes))
	}
	for _, c := range out.Changes {
		if c.Action != "added" {
			t.Errorf("%+v", c)
		}
	}
	if out.Changes[0].What != "hooks.SessionStart:session-start" || out.Changes[12].What != "statusLine" {
		t.Errorf("order: %+v", out.Changes)
	}
	var rules []string
	for _, c := range out.Changes[13:] {
		rules = append(rules, c.What)
	}
	if want := "permissions.allow:Bash(sesshin:*) permissions.ask:Bash(sesshin install:*) permissions.ask:Bash(sesshin uninstall:*) permissions.ask:Bash(sesshin prune:*) permissions.ask:Bash(sesshin resume:*)"; strings.Join(rules, " ") != want {
		t.Errorf("rules: %q", rules)
	}

	in := installFile(t, filepath.Join(f.state(), "install.json"))
	if in.HookBinary != fixtureHook || in.Version != fixtureBuild || in.InstalledAt != "2026-10-03T19:30:05Z" ||
		in.Locations.ConfigDir != f.config() || in.Locations.StateDir != f.state() || in.Locations.ClaudeSettings != f.settings() {
		t.Errorf("%+v", in)
	}
	if len(f.selfTests) != 1 || f.selfTests[0] != fixtureHook {
		t.Errorf("self-test ran for %v", f.selfTests)
	}
	if _, err := os.Stat(f.settings()); !os.IsNotExist(err) {
		t.Errorf("settings.json: %v", err) // sesshin never writes it
	}

	// The apply command applies the proposal, through sh, whatever the paths
	// (the directory is Claude Code's, which exists once it has run).
	if err := os.MkdirAll(filepath.Dir(f.settings()), 0o700); err != nil {
		t.Fatal(err)
	}
	sh(t, (*out.Apply)[1])
	if f.read(f.settings()) != f.read(proposal) {
		t.Error("cat did not apply the proposal")
	}
	if d := sh(t, (*out.Apply)[0]); d != "" { // diff exits 0 and prints nothing when they match
		t.Errorf("diff after applying: %s", d)
	}
	before := snapshot(t, f.home)
	dry := installOut(t, f, true)
	if !dry.DryRun || dry.ProposalPath != nil || dry.Apply != nil {
		t.Errorf("dry run: %+v", dry)
	}
	for _, c := range dry.Changes {
		if c.Action != "unchanged" {
			t.Errorf("after applying: %+v", c)
		}
	}
	if after := snapshot(t, f.home); len(after) != len(before) {
		t.Errorf("dry run changed the files: %v", after)
	} else {
		for p, c := range before {
			if after[p] != c {
				t.Errorf("dry run changed %s", p)
			}
		}
	}
}

// The permission rules join the proposal beside the user's own: a rule that is
// there is unchanged, the rest are appended, and everything else under
// permissions stays, the user's Bash(jq:*) among it, unreported.
func TestInstallPermissionRules(t *testing.T) {
	f := newFixture(t)
	f.write(f.settings(), `{"permissions": {"allow": ["Bash(jq:*)", "Read(~/x/**)"], "deny": ["Bash(sesshin prune:*)"], "defaultMode": "plan"}}`)
	env := Install(InstallInput{}, f.s)
	out := result[installResult](t, env, "install-output")
	got := map[string]string{}
	for _, c := range out.Changes[13:] {
		got[c.What] = c.Action
	}
	want := map[string]string{
		"permissions.allow:Bash(sesshin:*)":         "added",
		"permissions.ask:Bash(sesshin install:*)":   "added",
		"permissions.ask:Bash(sesshin uninstall:*)": "added",
		"permissions.ask:Bash(sesshin prune:*)":     "added",
		"permissions.ask:Bash(sesshin resume:*)":    "added",
	}
	if len(got) != len(want) {
		t.Errorf("%+v", out.Changes)
	}
	for w, a := range want {
		if got[w] != a {
			t.Errorf("%s: %q, want %q", w, got[w], a)
		}
	}
	proposal := f.read(*out.ProposalPath)
	wantPerms := `"permissions": {
    "allow": [
      "Bash(jq:*)",
      "Read(~/x/**)",
      "Bash(sesshin:*)"
    ],
    "deny": [
      "Bash(sesshin prune:*)"
    ],
    "defaultMode": "plan",
    "ask": [
      "Bash(sesshin install:*)",
      "Bash(sesshin uninstall:*)",
      "Bash(sesshin prune:*)",
      "Bash(sesshin resume:*)"
    ]
  }`
	if !strings.Contains(proposal, wantPerms) {
		t.Errorf("proposal:\n%s\nwant it to contain\n%s", proposal, wantPerms)
	}
}

// A permissions of the wrong shape is a corrupt settings.json, as a bad hooks
// is.
func TestInstallPermissionsCorrupt(t *testing.T) {
	for _, in := range []string{`{"permissions": []}`, `{"permissions": {"allow": "Bash(sesshin:*)"}}`, `{"permissions": {"ask": {}}}`} {
		f := newFixture(t)
		f.write(f.settings(), in)
		e := failure(t, Install(InstallInput{}, f.s), KindCorrupt)
		if e.Details["path"] != f.settings() {
			t.Errorf("%s: %+v", in, e.Details)
		}
	}
}

// A dry run writes nothing, not even the state directory, but runs the
// checks and the self-test.
func TestInstallDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	out := installOut(t, f, true)
	if out.ProposalPath != nil || out.Apply != nil || len(out.Changes) != 18 || len(f.selfTests) != 1 {
		t.Errorf("%+v, self-tests %v", out, f.selfTests)
	}
	if _, err := os.Stat(f.state()); !os.IsNotExist(err) {
		t.Errorf("state directory: %v", err)
	}
	f.selfTest = selfTestFailed(fixtureHook, "stop", "x")
	failure(t, Install(InstallInput{DryRun: true}, f.s), KindSelfTest)
}

// Every error kind, in the order install checks them; none writes anything.
func TestInstallErrorPrecedence(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.config(), "config.toml"), "retain_days = \"x\"\n")
	f.write(filepath.Join(f.config(), "hooks.properties"), "hook_lock_wait_ms=abc\n")
	f.write(f.settings(), "[]")
	f.selfTest = selfTestFailed(fixtureHook, "stop", "boom")

	f.env["HOME"] = "relative"
	e := failure(t, Install(InstallInput{}, f.s), KindEnvironment)
	if e.Details["variable"] != "HOME" {
		t.Errorf("%+v", e)
	}
	f.env["HOME"] = f.home

	for _, step := range []struct {
		path, kind string
		fix        func()
	}{
		{filepath.Join(f.config(), "config.toml"), KindCorrupt, func() { os.Remove(filepath.Join(f.config(), "config.toml")) }},
		{filepath.Join(f.config(), "hooks.properties"), KindCorrupt, func() { os.Remove(filepath.Join(f.config(), "hooks.properties")) }},
		{f.settings(), KindCorrupt, func() { f.write(f.settings(), "{}") }},
		{fixtureHook, KindSelfTest, func() { f.selfTest = nil }},
	} {
		env := Install(InstallInput{}, f.s)
		e := failure(t, env, step.kind)
		if e.Details["path"] != step.path {
			t.Errorf("%s: details %+v", step.path, e.Details)
		}
		if _, err := os.Stat(f.state()); !os.IsNotExist(err) {
			t.Errorf("%s: wrote the state directory: %v", step.path, err)
		}
		step.fix()
	}
	installOut(t, f, false)

	// The corrupt settings.json's detail names the key.
	f.write(f.settings(), `{"hooks": {"Stop": {}}}`)
	e = failure(t, Install(InstallInput{}, f.s), KindCorrupt)
	if e.Details["detail"] != "hooks.Stop is not an array" {
		t.Errorf("%+v", e.Details)
	}
}

// settings.json is read through a symlink; a directory in its place, or one
// that can't be read, is io; absent is an empty object.
func TestInstallSettingsFile(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.home, "dotfiles", "settings.json")
	f.write(target, `{"model": "opus"}`)
	if err := os.MkdirAll(filepath.Dir(f.settings()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.settings()); err != nil {
		t.Fatal(err)
	}
	installOut(t, f, false)
	if p := f.read(filepath.Join(f.state(), "settings.proposed.json")); !strings.HasPrefix(p, "{\n  \"model\": \"opus\",") {
		t.Errorf("proposal does not start from the symlink's target:\n%s", p)
	}

	g := newFixture(t)
	if err := os.MkdirAll(g.settings(), 0o700); err != nil {
		t.Fatal(err)
	}
	e := failure(t, Install(InstallInput{}, g.s), KindIO)
	if e.Details["code"] != "EISDIR" || e.Details["path"] != g.settings() {
		t.Errorf("%+v", e)
	}
	if _, err := os.Stat(g.state()); !os.IsNotExist(err) {
		t.Errorf("wrote the state directory: %v", err)
	}

	// A claude directory that is a file: ENOTDIR, not absent.
	h := newFixture(t)
	h.write(filepath.Join(h.home, ".claude"), "x")
	if e := failure(t, Install(InstallInput{}, h.s), KindIO); e.Details["code"] != "ENOTDIR" {
		t.Errorf("%+v", e)
	}
}

// An error with no errno anywhere in an operation is internal, never an
// invented code; an OS error from the self-test's file reads is io.
func TestInstallExecutable(t *testing.T) {
	f := newFixture(t)
	f.s.Executable = func() (string, error) { return "", os.ErrInvalid }
	failure(t, Install(InstallInput{}, f.s), KindInternal)
	f.s.Executable = func() (string, error) {
		return "/x/sesshin", &os.PathError{Op: "lstat", Path: "/x", Err: syscall.EACCES}
	}
	if e := failure(t, Install(InstallInput{}, f.s), KindIO); e.Details["code"] != "EACCES" {
		t.Errorf("%+v", e)
	}
}

// The replaced statusLine is in the warning verbatim: its key order and
// number text as in settings.json.
func TestInstallStatusLineReplaced(t *testing.T) {
	f := newFixture(t)
	f.write(f.settings(), `{"statusLine": {"type": "command", "command": "ccstatusline", "padding": 1.50, "z": 1e2, "a": "é"}}`)
	env := Install(InstallInput{DryRun: true}, f.s)
	checkEnvelope(t, env, "install-output")
	if len(env.Warnings) != 1 || env.Warnings[0].Kind != "status-line-replaced" {
		t.Fatalf("%+v", env.Warnings)
	}
	line, err := jsonio.MarshalLine(env)
	if err != nil {
		t.Fatal(err)
	}
	want := `"details":{"settings_path":` + jsonString(f.settings()) + `,"status_line":{"type":"command","command":"ccstatusline","padding":1.50,"z":1e2,"a":"é"}}`
	if !strings.Contains(string(line), want) {
		t.Errorf("%s\nwant ...%s", line, want)
	}
	var out installResult
	b, _ := json.Marshal(env.Result)
	_ = json.Unmarshal(b, &out)
	if sl := out.Changes[12]; sl.What != "statusLine" || sl.Action != "replaced" {
		t.Errorf("%+v", sl)
	}

	// sesshin's own statusLine, and none, are not replaced.
	g := newFixture(t)
	installOut(t, g, false)
	if err := os.MkdirAll(filepath.Dir(g.settings()), 0o700); err != nil {
		t.Fatal(err)
	}
	sh(t, "cat "+shellQuote(filepath.Join(g.state(), "settings.proposed.json"))+" > "+shellQuote(g.settings()))
	if env := Install(InstallInput{DryRun: true}, g.s); len(env.Warnings) != 0 {
		t.Errorf("%+v", env.Warnings)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// install.json's hook_binary makes the entries of an earlier install sesshin's,
// when the path is not named sesshin-hook; a missing or unusable one doesn't.
func TestInstallRecordedHookBinary(t *testing.T) {
	settings := `{"hooks": {"Gone": [{"matcher": "", "hooks": [{"type": "command", "command": "/old/odd-name stop"}]}]}}`
	removed := func(f *fixture) bool {
		out := installOut(t, f, true)
		for _, c := range out.Changes {
			if c.What == "hooks.Gone:stop" {
				return c.Action == "removed"
			}
		}
		return false
	}

	f := newFixture(t)
	f.write(f.settings(), settings)
	if removed(f) {
		t.Error("removed with no install.json")
	}
	f.write(filepath.Join(f.state(), "install.json"), "{")
	if removed(f) {
		t.Error("removed with an unusable install.json")
	}
	good := `{"schema": 1, "hook_binary": "/old/odd-name", "version": "v1", "installed_at": "2026-01-01T00:00:00Z",
		"locations": {"config_dir": "/c", "state_dir": "/s", "claude_settings": "/c/settings.json"}}`
	f.write(filepath.Join(f.state(), "install.json"), good)
	if !removed(f) {
		t.Error("not removed with install.json recording the path")
	}
	// The new install.json replaces the old one, with the new locations.
	installOut(t, f, false)
	if in := installFile(t, filepath.Join(f.state(), "install.json")); in.HookBinary != fixtureHook || in.Locations.ClaudeSettings != f.settings() {
		t.Errorf("%+v", in)
	}
}

// An install.json that can't be read is io.
func TestInstallJSONUnreadable(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.state(), "install.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if e := failure(t, Install(InstallInput{}, f.s), KindIO); e.Details["code"] != "EISDIR" {
		t.Errorf("%+v", e)
	}
}

func TestDecodeInstallInput(t *testing.T) {
	in, e := DecodeInput([]byte(`{"dry_run": true}`), DecodeInstallInput)
	if e != nil || !in.DryRun {
		t.Errorf("%+v %+v", in, e)
	}
	if _, e := DecodeInput([]byte(`{"dry_run": 1}`), DecodeInstallInput); e == nil || e.Kind != KindInvalidInput {
		t.Errorf("%+v", e)
	}
	if _, e := DecodeInput([]byte(`{"x": 1}`), DecodeUninstallInput); e == nil || e.Kind != KindInvalidInput {
		t.Errorf("%+v", e)
	}
	if in, e := DecodeInput([]byte(`{}`), DecodeUninstallInput); e != nil || in.DryRun {
		t.Errorf("%+v %+v", in, e)
	}
}
