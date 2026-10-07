package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const migrateFixtureDir = "../internal/migrate/testdata/001-extra/before"

const migrateSess1 = "11111111-1111-4111-8111-111111111111"

type migrateResult struct {
	OK     bool `json:"ok"`
	Result struct {
		DryRun  bool `json:"dry_run"`
		From    int  `json:"from"`
		To      int  `json:"to"`
		Applied []struct {
			Step int    `json:"step"`
			Name string `json:"name"`
		} `json:"applied"`
		Changed []struct {
			SessionID string   `json:"session_id"`
			ID        *int64   `json:"id"`
			Files     []string `json:"files"`
		} `json:"changed"`
		Unconverted []struct {
			Path   string `json:"path"`
			Detail string `json:"detail"`
		} `json:"unconverted"`
	} `json:"result"`
	Warnings []struct {
		Kind    string         `json:"kind"`
		Details map[string]any `json:"details"`
	} `json:"warnings"`
}

func sesshinMigrate(t *testing.T, h *Harness, args ...string) migrateResult {
	t.Helper()
	res := h.Sesshin(append([]string{"migrate"}, args...)...)
	if res.Exit != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	var out migrateResult
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil || !out.OK {
		t.Fatalf("%v: %s", err, res.Stdout)
	}
	return out
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The built hooks over a schema 1 state directory leave its files alone and
// issue no ID; sesshin migrate --dry-run reports and changes nothing;
// sesshin migrate converts; then a lifecycle hook completes the pending ID.
func TestMigrate(t *testing.T) {
	t.Parallel()
	h := New(t)
	state := h.Loc.StateDir
	if err := filepath.WalkDir(migrateFixtureDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(migrateFixtureDir, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(state, rel), 0o700)
		}
		return os.WriteFile(filepath.Join(state, rel), []byte(readFileT(t, p)), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	stateJSON := filepath.Join(state, "state.json")
	oldState := readFileT(t, stateJSON)
	oldSess1 := readFileT(t, filepath.Join(state, "sessions", migrateSess1, "sesshin.json"))

	// A session still at schema 1: its sesshin.json is left alone, and logged.
	quiet(t, h.Hook("session-start", eventFor(migrateSess1, "SessionStart", `"source":"startup"`)))
	if got := readFileT(t, filepath.Join(state, "sessions", migrateSess1, "sesshin.json")); got != oldSess1 {
		t.Errorf("sesshin.json changed:\n%s", got)
	}
	if !strings.Contains(hooksLog(h), "sesshin.json in format 1, not 2; left alone") {
		t.Errorf("hooks.log: %s", hooksLog(h))
	}
	// A new session gets a pending ID while state.json is at schema 1.
	quiet(t, h.Hook("user-prompt", event("UserPromptSubmit", `"prompt_id":"p1"`)))
	if readFileT(t, stateJSON) != oldState {
		t.Error("state.json changed")
	}
	if readSesshin(t, h).ID != nil {
		t.Error("a schema 1 state.json issued an ID")
	}

	// Dry run: the output says what would change, and the disk does not.
	before := readFileT(t, filepath.Join(state, "sessions", "22222222-2222-4222-8222-222222222222", "sesshin.json"))
	dry := sesshinMigrate(t, h, "--dry-run")
	if !dry.Result.DryRun || dry.Result.From != 0 || dry.Result.To != 1 || len(dry.Result.Changed) != 3 ||
		len(dry.Result.Unconverted) != 1 || len(dry.Warnings) != 2 {
		t.Errorf("dry run: %+v", dry)
	}
	if readFileT(t, stateJSON) != oldState ||
		readFileT(t, filepath.Join(state, "sessions", "22222222-2222-4222-8222-222222222222", "sesshin.json")) != before {
		t.Error("the dry run wrote")
	}

	out := sesshinMigrate(t, h)
	r := out.Result
	if r.DryRun || r.From != 0 || r.To != 1 || len(r.Applied) != 1 || r.Applied[0].Name != "extra" ||
		len(r.Changed) != 3 || r.Changed[0].SessionID != migrateSess1 || r.Changed[0].ID == nil || *r.Changed[0].ID != 12 {
		t.Errorf("migrate: %+v", out)
	}
	if got := readFileT(t, stateJSON); got != "{\n  \"schema\": 2,\n  \"last_id\": 41,\n  \"migration\": 1\n}\n" {
		t.Errorf("state.json %q", got)
	}
	if !strings.Contains(readFileT(t, filepath.Join(state, "sessions", migrateSess1, "sesshin.json")), `"extra": {}`) {
		t.Error("sesshin.json not converted")
	}
	if again := sesshinMigrate(t, h); again.Result.From != 1 || len(again.Result.Changed) != 0 || len(again.Warnings) != 0 {
		t.Errorf("second run: %+v", again)
	}

	// The next lifecycle hook completes the pending ID.
	quiet(t, h.Hook("post-tool-use", event("PostToolUse", `"prompt_id":"p1"`)))
	if id := readSesshin(t, h).ID; id == nil || *id != 42 {
		t.Errorf("id %v, want 42", id)
	}
}
