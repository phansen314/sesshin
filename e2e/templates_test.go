package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTemplate writes <config dir>/templates/<rel>.
func writeTemplate(t *testing.T, h *Harness, rel, content string) {
	t.Helper()
	p := filepath.Join(h.Loc.ConfigDir, "templates", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// sesshin templates lists the directory by name, with each one's content and
// the problem spawn would raise (operations.md, templates).
func TestTemplatesList(t *testing.T) {
	t.Parallel()
	h := New(t)
	if res := h.Sesshin("templates"); res.Exit != 0 || res.Stdout != `{"ok":true,"result":{"templates":[]},"warnings":[]}`+"\n" {
		t.Fatalf("no templates/: exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	writeTemplate(t, h, "review.json", `{"description":"Review a PR","args":["--model","opus"],"prompt_file":"prompts/review.md"}`)
	writeTemplate(t, h, "prompts/review.md", "Review the diff.")
	writeTemplate(t, h, "broken.json", `{"bogus":true}`)
	writeTemplate(t, h, "Upper.json", `{}`)

	res := h.Sesshin("templates")
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Templates []struct {
				Name        string          `json:"name"`
				Path        string          `json:"path"`
				Description *string         `json:"description"`
				Template    json.RawMessage `json:"template"`
				Problem     *struct {
					Kind string `json:"kind"`
					Path string `json:"path"`
				} `json:"problem"`
			} `json:"templates"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil || res.Exit != 0 || !env.OK || res.Stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q: %v", res.Exit, res.Stdout, res.Stderr, err)
	}
	ts := env.Result.Templates
	if len(ts) != 2 || ts[0].Name != "broken" || ts[1].Name != "review" {
		t.Fatalf("templates %+v", ts)
	}
	if ts[0].Problem == nil || ts[0].Problem.Kind != "corrupt" || string(ts[0].Template) != "null" || ts[0].Description != nil {
		t.Errorf("broken: %+v", ts[0])
	}
	if ts[1].Problem != nil || ts[1].Description == nil || *ts[1].Description != "Review a PR" ||
		ts[1].Path != filepath.Join(h.Loc.ConfigDir, "templates", "review.json") ||
		string(ts[1].Template) != `{"description":"Review a PR","args":["--model","opus"],"prompt_file":"prompts/review.md"}` {
		t.Errorf("review: %+v", ts[1])
	}
	// The same, with -i.
	in := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(in, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if again := h.Sesshin("templates", "-i", in); again.Stdout != res.Stdout {
		t.Errorf("-i: %q", again.Stdout)
	}
	if bad := h.Sesshin("templates", "extra"); bad.Exit != 2 {
		t.Errorf("an argument: exit %d, %q", bad.Exit, bad.Stdout)
	}
}

// sesshin spawn --template merges the flags over the template, reserves with
// its name, and the session that adopts the reservation records it; list and
// show report it, and it survives a resume (design-spec.md, Templates).
func TestSpawnTemplate(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	work := filepath.Join(h.Home, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(h.Home, "t.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTemplate(t, h, "review.json", `{"job":"rev","cwd":"~/work","type":"split","args":["--model","opus"],"prompt_file":"prompts/review.md","vars":{"A":"t"},"start_timeout_secs":0}`)
	writeTemplate(t, h, "prompts/review.md", "Review $(it)\n")

	env, res := sesshinSpawn(t, h, "--template", "review", "--var", "B=i", "--", "--effort", "high")
	if res.Exit != 0 || res.Stderr != "" || !env.OK || len(env.Warnings) != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	r, ok := readReservation(t, h, "rev")
	if !ok || r.Template == nil || *r.Template != "review" {
		t.Fatalf("reservation %+v", r)
	}
	want := []string{"@", "--to", "unix:/e2e/kitty", "launch", "--type=window", "--self", "--keep-focus", "--cwd=" + work,
		"--var=A=t", "--var=B=i",
		"--env=SESSHIN_JOB=rev", "--env=SESSHIN_TOKEN=" + r.Token,
		"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--name", "rev", "--model", "opus", "--effort", "high", "--", "Review $(it)\n"}
	if got := h.KittenCalls(); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Errorf("kitten calls %q\nwant %q", got, want)
	}

	// The session adopts the reservation and records the template.
	h.Setenv("SESSHIN_JOB", "rev")
	h.Setenv("SESSHIN_TOKEN", r.Token)
	h.KittyWindow(kittySocket, "7")
	start(t, h, "startup", `"cwd":"`+work+`"`, `"transcript_path":"`+transcript+`"`)
	got := readSesshin(t, h)
	if got.Template == nil || *got.Template != "review" || got.Job == nil || *got.Job != "rev" || got.Source != "spawn" {
		t.Fatalf("sesshin.json %+v", got)
	}
	if _, ok := readReservation(t, h, "rev"); ok {
		t.Error("the reservation was kept")
	}
	list := h.Sesshin("list", "--liveness", "all", "--fields", "template,job")
	if !strings.Contains(list.Stdout, `"template":"review"`) {
		t.Errorf("list: %s", list.Stdout)
	}
	show := h.Sesshin("show", "rev")
	if !strings.Contains(show.Stdout, `"template":"review"`) {
		t.Errorf("show: %s", show.Stdout)
	}
	// The template may change or go; the record stays.
	if err := os.Remove(filepath.Join(h.Loc.ConfigDir, "templates", "review.json")); err != nil {
		t.Fatal(err)
	}
	if list := h.Sesshin("list", "--liveness", "all", "--fields", "template"); !strings.Contains(list.Stdout, `"template":"review"`) {
		t.Errorf("list after removal: %s", list.Stdout)
	}
}

// The template's errors, from the built binary: not-found with templates,
// corrupt, a prompt file that is missing, nothing launched; and a file given
// to -i supplies no working directory.
func TestSpawnTemplateErrors(t *testing.T) {
	t.Parallel()
	h := kittySpawnHarness(t)
	writeTemplate(t, h, "corrupt.json", `{"bogus":1}`)
	writeTemplate(t, h, "noprompt.json", `{"prompt_file":"nope.md"}`)
	writeTemplate(t, h, "nocwd.json", `{"job":"j"}`)

	env, res := sesshinSpawn(t, h, "--template", "missing", "--cwd", h.Home)
	if res.Exit != 1 || env.OK || env.Error.Kind != "not-found" || !slices.Equal(anyStrings(env.Error.Details["templates"]), []string{"missing"}) {
		t.Errorf("missing: exit %d, stdout %q", res.Exit, res.Stdout)
	}
	env, res = sesshinSpawn(t, h, "--template", "corrupt", "--cwd", h.Home)
	if res.Exit != 1 || env.OK || env.Error.Kind != "corrupt" || env.Error.Details["path"] != filepath.Join(h.Loc.ConfigDir, "templates", "corrupt.json") {
		t.Errorf("corrupt: exit %d, stdout %q", res.Exit, res.Stdout)
	}
	env, res = sesshinSpawn(t, h, "--template", "noprompt", "--cwd", h.Home)
	if res.Exit != 1 || env.OK || env.Error.Kind != "not-found" ||
		!slices.Equal(anyStrings(env.Error.Details["paths"]), []string{filepath.Join(h.Loc.ConfigDir, "templates", "nope.md")}) {
		t.Errorf("noprompt: exit %d, stdout %q", res.Exit, res.Stdout)
	}
	env, res = sesshinSpawn(t, h, "--template", "BAD")
	if res.Exit != 1 || env.OK || env.Error.Kind != "invalid-input" {
		t.Errorf("bad name: exit %d, stdout %q", res.Exit, res.Stdout)
	}

	// -i takes cwd as given: a template without one is not enough.
	in := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(in, []byte(`{"template":"nocwd"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env, res = sesshinSpawn(t, h, "-i", in)
	if res.Exit != 1 || env.OK || env.Error.Kind != "invalid-input" {
		t.Errorf("-i without cwd: exit %d, stdout %q", res.Exit, res.Stdout)
	}
	// The same from flags works: the working directory is the last resort.
	env, res = sesshinSpawn(t, h, "--template", "nocwd", "--start-timeout-secs", "0")
	if res.Exit != 0 || !env.OK {
		t.Errorf("flags: exit %d, stdout %q", res.Exit, res.Stdout)
	}
	// And -i with cwd in the file.
	if err := os.WriteFile(in, []byte(`{"template":"nocwd","job":"j2","cwd":`+quote(h.Home)+`,"start_timeout_secs":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env, res = sesshinSpawn(t, h, "-i", in)
	if res.Exit != 0 || !env.OK {
		t.Errorf("-i with cwd: exit %d, stdout %q", res.Exit, res.Stdout)
	}
	if calls := h.KittenCalls(); len(calls) != 2 {
		t.Errorf("kitten calls %q", calls)
	}
}

func anyStrings(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, it := range items {
		s, _ := it.(string)
		out = append(out, s)
	}
	return out
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
