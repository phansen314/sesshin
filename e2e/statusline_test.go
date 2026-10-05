package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// statuslineSession is the session the model's lifecycle.json fixture is of.
const statuslineSession = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

// A statusline tick records its payload when the session has a usable
// lifecycle.json, and prints its line (hooks-spec.md, statusline).
func TestStatuslineRecords(t *testing.T) {
	t.Parallel()
	h := New(t)
	dir := filepath.Join(h.Loc.StateDir, "sessions", statuslineSession)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lc, err := os.ReadFile("../internal/model/testdata/lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lifecycle.json"), lc, 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"session_id":"` + statuslineSession + `","cwd":"` + h.Home + `","cost":{"total_cost_usd":1.50}}`
	res := h.Hook("statusline", body)
	// The fixture's title; the sesshin ID is hidden: there is no sesshin.json.
	if want := "⬢ api review | 🧠 0% | 📁 " + filepath.Base(h.Home) + " | 💰 $1.50"; res.Exit != 0 || res.Stdout != want || res.Stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want %q", res.Exit, res.Stdout, res.Stderr, want)
	}
	data, err := os.ReadFile(filepath.Join(dir, "statusline.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The fake claude is the process the hook ran under.
	for _, want := range []string{`"pid": ` + strconv.Itoa(res.ClaudePID) + `,`, `"total_cost_usd": 1.50`, `"git_branch": null`, `"usd": 1.5`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("statusline.json lacks %s:\n%s", want, data)
		}
	}
}

// Without a usable lifecycle.json the tick renders and writes nothing: the
// statusline does not adopt a session.
func TestStatuslineNoLifecycle(t *testing.T) {
	t.Parallel()
	h := New(t)
	dir := filepath.Join(h.Loc.StateDir, "sessions", statuslineSession)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	res := h.Hook("statusline", `{"session_id":"`+statuslineSession+`","cwd":"/tmp"}`)
	if res.Exit != 0 || res.Stdout != "🧠 0% | 📁 tmp" || res.Stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	es, err := os.ReadDir(dir)
	if err != nil || len(es) != 0 {
		t.Errorf("session directory holds %v, %v", es, err)
	}
}

// One tick through the built binary, rendered whole: the sesshin ID from
// sesshin.json, the title from lifecycle.json, times in the zone TZ names, no
// trailing newline. The sesshin ID is read for display only: it is not in
// statusline.json.
func TestStatuslineRenders(t *testing.T) {
	t.Parallel()
	h := New(t)
	if _, err := time.LoadLocation("America/Denver"); err != nil {
		t.Skipf("no zone database: %v", err)
	}
	h.Setenv("TZ", "America/Denver")
	dir := filepath.Join(h.Loc.StateDir, "sessions", statuslineSession)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lc, err := os.ReadFile("../internal/model/testdata/lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"lifecycle.json": string(lc),
		"sesshin.json":   `{"schema":1,"id":12,"job":null,"source":"hook","placement":null}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(h.Home, "sesshin")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"session_id":"` + statuslineSession + `","cwd":"` + repo + `","model":{"id":"claude-opus-5-5"},` +
		`"cost":{"total_cost_usd":1.2,"total_api_duration_ms":780000},` +
		`"context_window":{"total_input_tokens":351000,"context_window_size":1000000,"used_percentage":34.6},` +
		`"rate_limits":{"five_hour":{"used_percentage":22,"resets_at":1791061200},"seven_day":{"used_percentage":41,"resets_at":1791298800}}}`
	res := h.Hook("statusline", body)
	want := "#12 | ⬢ api review | 🧠 35% 351k/1M | 📁 sesshin | 🌿 main | 🤖 Opus 5.5 | 💰 $1.20 | ⌛ 13m API\n" +
		"⏱️ 5h 22% resets 3:00PM | 7d 41% resets 10/6 9:00AM"
	if res.Exit != 0 || res.Stdout != want || res.Stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want %q", res.Exit, res.Stdout, res.Stderr, want)
	}
	data, err := os.ReadFile(filepath.Join(dir, "statusline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"id": 12`) || strings.Contains(string(data), "placement") {
		t.Errorf("statusline.json depends on sesshin.json:\n%s", data)
	}
}
