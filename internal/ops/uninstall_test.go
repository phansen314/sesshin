package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type uninstallResult struct {
	DryRun       bool         `json:"dry_run"`
	SettingsPath string       `json:"settings_path"`
	ProposalPath *string      `json:"proposal_path"`
	Apply        *[]string    `json:"apply"`
	Changes      []ChangeItem `json:"changes"`
}

func uninstallOut(t *testing.T, f *fixture, dry bool) uninstallResult {
	t.Helper()
	return result[uninstallResult](t, Uninstall(UninstallInput{DryRun: dry}, f.s), "uninstall-output")
}

const otherTool = `"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/bin/audit"}]}]`

// Install, apply, uninstall, apply: sesshin's entries are gone, another tool's
// survive both.
func TestUninstallRoundTrip(t *testing.T) {
	f := newFixture(t)
	f.write(f.settings(), `{"hooks": {`+otherTool+`}, "model": "opus"}`)
	out := installOut(t, f, false)
	sh(t, (*out.Apply)[1])
	if !strings.Contains(f.read(f.settings()), "sesshin-hook") {
		t.Fatal("not applied")
	}

	un := uninstallOut(t, f, false)
	proposal := filepath.Join(f.state(), "settings.proposed.json")
	if un.SettingsPath != f.settings() || un.ProposalPath == nil || *un.ProposalPath != proposal || un.Apply == nil || len(*un.Apply) != 2 {
		t.Errorf("%+v", un)
	}
	if len(un.Changes) != 17 || un.Changes[12].What != "statusLine" || un.Changes[13].What != "permissions.allow:Bash(sesshin:*)" {
		t.Errorf("%+v", un.Changes)
	}
	for _, c := range un.Changes {
		if c.Action != "removed" {
			t.Errorf("%+v", c)
		}
	}
	sh(t, (*un.Apply)[1])
	got := f.read(f.settings())
	if strings.Contains(got, "sesshin-hook") || strings.Contains(got, "Bash(sesshin") || !strings.Contains(got, "/usr/bin/audit") || !strings.Contains(got, `"model": "opus"`) {
		t.Errorf("settings.json:\n%s", got)
	}
	// Bash(jq:*) is shared, so it stays.
	if !strings.Contains(got, `"allow": [
      "Bash(jq:*)"
    ]`) {
		t.Errorf("settings.json:\n%s", got)
	}
	// Apart from the proposal, the state directory is left alone.
	if _, err := os.Stat(filepath.Join(f.state(), "install.json")); err != nil {
		t.Error(err)
	}
	if again := uninstallOut(t, f, true); len(again.Changes) != 0 {
		t.Errorf("%+v", again.Changes)
	}
}

// Uninstall removes sesshin's four rules and leaves Bash(jq:*) and the user's
// others, reporting one removed item per rule after the statusLine.
func TestUninstallPermissionRules(t *testing.T) {
	f := newFixture(t)
	f.write(f.settings(), `{"permissions": {"allow": ["Bash(sesshin:*)", "Bash(jq:*)", "Bash(sesshin:*)", "Read(x)"], "ask": ["Bash(sesshin install:*)", "Bash(other:*)"], "defaultMode": "plan"}}`)
	un := uninstallOut(t, f, false)
	var got []string
	for _, c := range un.Changes {
		got = append(got, c.Action+" "+c.What)
	}
	want := []string{"removed permissions.allow:Bash(sesshin:*)", "removed permissions.ask:Bash(sesshin install:*)"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%q, want %q", got, want)
	}
	wantFile := `{
  "permissions": {
    "allow": [
      "Bash(jq:*)",
      "Read(x)"
    ],
    "ask": [
      "Bash(other:*)"
    ],
    "defaultMode": "plan"
  }
}
`
	if got := f.read(*un.ProposalPath); got != wantFile {
		t.Errorf("proposal:\n%s\nwant\n%s", got, wantFile)
	}
}

// A permissions of the wrong shape is a corrupt settings.json.
func TestUninstallPermissionsCorrupt(t *testing.T) {
	f := newFixture(t)
	f.write(f.settings(), `{"permissions": {"allow": {}}}`)
	env := Uninstall(UninstallInput{}, f.s)
	checkEnvelope(t, env, "uninstall-output")
	if env.OK || env.Error.Kind != KindCorrupt {
		t.Errorf("%+v", env)
	}
}

// With no settings.json the proposal is {} and nothing changes: no special
// case. A dry run writes nothing.
func TestUninstallNoSettings(t *testing.T) {
	f := newFixture(t)
	dry := uninstallOut(t, f, true)
	if !dry.DryRun || dry.ProposalPath != nil || dry.Apply != nil || dry.Changes == nil || len(dry.Changes) != 0 {
		t.Errorf("%+v", dry)
	}
	if _, err := os.Stat(f.state()); !os.IsNotExist(err) {
		t.Errorf("state directory: %v", err)
	}
	out := uninstallOut(t, f, false)
	if f.read(*out.ProposalPath) != "{}\n" || len(out.Changes) != 0 {
		t.Errorf("%q %+v", f.read(*out.ProposalPath), out.Changes)
	}
}

// uninstall proposes against the settings.json install.json records, and
// removes the sesshin-hook it recorded; with install.json missing or unusable it
// falls back to what it resolves.
func TestUninstallRecorded(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(f.home, "elsewhere", "settings.json")
	f.write(other, `{"hooks": {"Gone": [{"matcher": "", "hooks": [{"type": "command", "command": "/old/odd-name stop"}]}]}}`)
	f.write(f.settings(), `{"hooks": {"Gone": [{"matcher": "", "hooks": [{"type": "command", "command": "/old/odd-name stop"}]}]}, "model": "x"}`)
	install := `{"schema": 1, "hook_binary": "/old/odd-name", "version": "v1", "installed_at": "2026-01-01T00:00:00Z",
		"locations": {"config_dir": "/c", "state_dir": "/s", "claude_settings": ` + jsonString(other) + `}}`
	f.write(filepath.Join(f.state(), "install.json"), install)

	out := uninstallOut(t, f, false)
	if out.SettingsPath != other || len(out.Changes) != 1 || out.Changes[0] != (ChangeItem{"hooks.Gone:stop", "removed"}) {
		t.Errorf("%+v", out)
	}
	if got := f.read(*out.ProposalPath); got != "{}\n" {
		t.Errorf("proposal %q", got)
	}
	if !strings.Contains(strings.Join(*out.Apply, "\n"), shellQuote(other)) {
		t.Errorf("apply %v", *out.Apply)
	}

	for name, content := range map[string]string{"missing": "", "unusable": "{", "wrong schema": strings.Replace(install, `"schema": 1`, `"schema": 2`, 1)} {
		os.Remove(filepath.Join(f.state(), "install.json"))
		if content != "" {
			f.write(filepath.Join(f.state(), "install.json"), content)
		}
		out := uninstallOut(t, f, true)
		if out.SettingsPath != f.settings() || len(out.Changes) != 0 {
			t.Errorf("%s: %+v", name, out)
		}
	}
}

// uninstall reads neither config.toml nor hooks.properties, and runs no
// self-test; HOME and settings.json are its only errors.
func TestUninstallErrors(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.config(), "config.toml"), "retain_days = \"x\"\n")
	f.write(filepath.Join(f.config(), "hooks.properties"), "hook_lock_wait_ms=abc\n")
	f.selfTest = selfTestFailed(fixtureHook, "stop", "boom")
	uninstallOut(t, f, false)
	if len(f.selfTests) != 0 {
		t.Errorf("self-test ran: %v", f.selfTests)
	}

	f.write(f.settings(), `{"statusLine": 3}`)
	e := failure(t, Uninstall(UninstallInput{}, f.s), KindCorrupt)
	if e.Details["path"] != f.settings() || e.Details["detail"] != "statusLine is not an object" {
		t.Errorf("%+v", e.Details)
	}
	f.env["HOME"] = ""
	failure(t, Uninstall(UninstallInput{}, f.s), KindEnvironment)
	f.env["HOME"] = f.home

	os.Remove(f.settings())
	if err := os.Mkdir(f.settings(), 0o700); err != nil {
		t.Fatal(err)
	}
	if e := failure(t, Uninstall(UninstallInput{}, f.s), KindIO); e.Details["code"] != "EISDIR" {
		t.Errorf("%+v", e)
	}
}

// A statusLine set since install is left alone, and reported unchanged.
func TestUninstallForeignStatusLine(t *testing.T) {
	f := newFixture(t)
	f.write(f.settings(), `{"statusLine": {"type": "command", "command": "ccstatusline"}}`)
	out := uninstallOut(t, f, true)
	if len(out.Changes) != 1 || out.Changes[0] != (ChangeItem{"statusLine", "unchanged"}) {
		t.Errorf("%+v", out.Changes)
	}
}
