package ops

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// templatesDir is the config directory's templates/.
func (f *pruneFixture) templatesDir() string { return filepath.Join(f.loc.ConfigDir, "templates") }

// template writes templates/<name>.json.
func (f *pruneFixture) template(name, content string) {
	f.t.Helper()
	f.templateFile(name+".json", content)
}

// templateFile writes a file under templates/.
func (f *pruneFixture) templateFile(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.templatesDir(), rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// jsonText is v as JSON.
func jsonText(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// spawnedRaw is a spawn of the JSON input as given that must have succeeded.
func (f *spawnFixture) spawnedRaw(in string) (SpawnOutput, []Warning) {
	f.t.Helper()
	env := f.spawnRaw(in)
	if !env.OK {
		f.t.Fatalf("spawn failed: %+v", env.Error)
	}
	return env.Result.(SpawnOutput), env.Warnings
}

// Operations.md, spawn, Merging a template: the template's job, cwd, type,
// name, args, vars, and prompt reach the launch and the reservation, the
// input winning where it gives one.
func TestSpawnTemplateMerged(t *testing.T) {
	f := newSpawnFixture(t)
	f.template("review", `{"job":"rev","cwd":`+jsonText(t, f.cwd)+`,"type":"split","name":"Reviewer","prompt":"template prompt",`+
		`"args":["--model","opus"],"vars":{"A":"t","B":"t"},"start_timeout_secs":0,"description":"d"}`)

	// The input without a cwd: the template's.
	out, warnings := f.spawnedRaw(`{"template":"review","args":["--effort","high"],"vars":{"B":"i","C":"i"}}`)
	if len(warnings) != 0 || out.Session != nil || out.Job == nil || *out.Job != "rev" {
		t.Errorf("%+v, warnings %+v", out, warnings)
	}
	want := placement.LaunchSpec{
		Caller: kitty.PlacementOf("unix:/kitty", 3),
		Type:   "split",
		Cwd:    f.cwd,
		Title:  "Reviewer",
		Vars:   []placement.Var{{Name: "A", Value: "t"}, {Name: "B", Value: "i"}, {Name: "C", Value: "i"}},
		Env:    []placement.Var{{Name: "SESSHIN_JOB", Value: "rev"}, {Name: "SESSHIN_TOKEN", Value: token(1)}},
		Argv: []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--name", "Reviewer",
			"--model", "opus", "--effort", "high", "--", "template prompt"},
	}
	if len(f.launches) != 1 || !reflect.DeepEqual(f.launches[0], want) {
		t.Errorf("launched %+v\nwant     %+v", f.launches, want)
	}
	r, ok := f.reservation("rev")
	if !ok || r.Template == nil || *r.Template != "review" || r.Job == nil || *r.Job != "rev" || enc(t, r.Extra) != "{}" {
		t.Errorf("reservation %+v", r)
	}

	// The input wins: job, cwd, type, name, prompt, timeout.
	other := t.TempDir()
	f.launches = nil
	out, _ = f.spawnedRaw(`{"template":"review","job":"mine","cwd":` + jsonText(t, other) + `,"type":"os-window","name":"Mine","prompt":"my prompt","start_timeout_secs":0,"extra":{"k":1}}`)
	want = placement.LaunchSpec{
		Caller: kitty.PlacementOf("unix:/kitty", 3),
		Type:   "os-window",
		Cwd:    other,
		Title:  "Mine",
		Vars:   []placement.Var{{Name: "A", Value: "t"}, {Name: "B", Value: "t"}},
		Env:    []placement.Var{{Name: "SESSHIN_JOB", Value: "mine"}, {Name: "SESSHIN_TOKEN", Value: token(2)}},
		Argv:   []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--name", "Mine", "--model", "opus", "--", "my prompt"},
	}
	if len(f.launches) != 1 || !reflect.DeepEqual(f.launches[0], want) {
		t.Errorf("launched %+v\nwant     %+v", f.launches, want)
	}
	r, ok = f.reservation("mine")
	if !ok || r.Template == nil || *r.Template != "review" || enc(t, r.Extra) != `{"k":1}` {
		t.Errorf("reservation %+v", r)
	}
}

// Defaults are applied after the merge: the name is the merged job, from the
// template when the input gives none; a template's start_timeout_secs of 0
// beats the default of 15.
func TestSpawnTemplateDefaults(t *testing.T) {
	f := newSpawnFixture(t)
	f.template("bare", `{"job":"tjob","cwd":`+jsonText(t, f.cwd)+`,"start_timeout_secs":0}`)
	f.spawnedRaw(`{"template":"bare"}`)
	if len(f.launches) != 1 || f.launches[0].Title != "tjob" || f.launches[0].Type != "tab" ||
		!reflect.DeepEqual(f.launches[0].Argv[len(f.launches[0].Argv)-2:], []string{"--name", "tjob"}) {
		t.Errorf("launched %+v", f.launches)
	}
	if len(f.sleeps) != 0 {
		t.Errorf("waited %d pauses with the template's start_timeout_secs 0", len(f.sleeps))
	}
}

// A spawn with no template reserves template null.
func TestSpawnWithoutTemplateReservesNull(t *testing.T) {
	f := newSpawnFixture(t)
	f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
	r, ok := f.reservation("api")
	if !ok || r.Template != nil {
		t.Errorf("reservation %+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(f.loc.ReservationsDir(), "api_"+token(1)+".json"))
	if !bytesHasSuffix(b, "\"extra\": {},\n  \"template\": null\n}\n") {
		t.Errorf("file %q", b)
	}
}

func bytesHasSuffix(b []byte, s string) bool {
	return len(b) >= len(s) && string(b[len(b)-len(s):]) == s
}

// The template's cwd may start with ~/, expanded against HOME.
func TestSpawnTemplateTildeCwd(t *testing.T) {
	f := newSpawnFixture(t)
	if err := os.Mkdir(filepath.Join(f.home, "proj"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.template("t", `{"cwd":"~/proj"}`)
	f.spawnedRaw(`{"template":"t","start_timeout_secs":0}`)
	if len(f.launches) != 1 || f.launches[0].Cwd != filepath.Join(f.home, "proj") {
		t.Errorf("launched %+v", f.launches)
	}
}

// The caller's working directory is the last place cwd can come from: called
// only when nothing else gives one, and never for input from -i (nil).
func TestSpawnTemplateCallerCwd(t *testing.T) {
	f := newSpawnFixture(t)
	f.template("nocwd", `{"job":"j"}`)
	f.template("withcwd", `{"cwd":`+jsonText(t, f.cwd)+`}`)
	asked := 0
	f.callerCwd = func() (string, error) { asked++; return f.cwd, nil }

	f.spawnedRaw(`{"template":"nocwd","start_timeout_secs":0}`)
	if asked != 1 || f.launches[0].Cwd != f.cwd {
		t.Errorf("asked %d, launched %+v", asked, f.launches)
	}
	asked = 0
	f.spawnedRaw(`{"template":"withcwd","start_timeout_secs":0}`)
	f.spawnedRaw(`{"cwd":` + jsonText(t, f.cwd) + `,"template":"nocwd","job":"j2","start_timeout_secs":0}`)
	if asked != 0 {
		t.Errorf("asked %d times though something else gave cwd", asked)
	}

	// Nothing gives cwd, and nothing is asked for: invalid-input at /cwd.
	f.callerCwd = nil
	for _, in := range []string{`{"template":"nocwd"}`, `{}`} {
		env := f.spawnRaw(in)
		wantKind(t, env, KindInvalidInput)
		if ps := env.Error.Details["problems"].([]model.Problem); len(ps) != 1 || ps[0].Field != "/cwd" {
			t.Errorf("%s: %+v", in, env.Error)
		}
	}
	// A working directory that can't be determined is io, when needed.
	f.callerCwd = func() (string, error) { return "", &os.PathError{Op: "getwd", Path: ".", Err: syscall.ENOENT} }
	env := f.spawnRaw(`{"template":"nocwd"}`)
	wantKind(t, env, KindIO)
	if env.Error.Details["code"] != "ENOENT" {
		t.Errorf("%+v", env.Error)
	}
	if env := f.spawnRaw(`{"template":"withcwd","start_timeout_secs":0}`); !env.OK {
		t.Errorf("not needed, yet failed: %+v", env.Error)
	}
}

// Every error of a template, with its details, raised before anything is
// reserved or launched.
func TestSpawnTemplateErrors(t *testing.T) {
	f := newSpawnFixture(t)
	f.template("corrupt", `{"bogus":1}`)
	f.template("badjob", `{"job":"12"}`)
	f.template("noprompt", `{"prompt_file":"prompts/none.md"}`)
	f.template("dirprompt", `{"prompt_file":"prompts"}`)
	f.templateFile("prompts/bad.md", "a\xffb")
	f.templateFile("prompts/nul.md", "a\x00b")
	f.template("badprompt", `{"prompt_file":"prompts/bad.md"}`)
	f.template("nulprompt", `{"prompt_file":"prompts/nul.md"}`)
	f.template("escape", `{"prompt_file":"../x.md"}`)
	if err := os.Mkdir(filepath.Join(f.templatesDir(), "adir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.home, "nowhere"), filepath.Join(f.templatesDir(), "dangling.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop.json", filepath.Join(f.templatesDir(), "loop.json")); err != nil {
		t.Fatal(err)
	}
	f.template("loopprompt", `{"prompt_file":"prompts/loop.md"}`)
	if err := os.Symlink("loop.md", filepath.Join(f.templatesDir(), "prompts", "loop.md")); err != nil {
		t.Fatal(err)
	}
	f.template("throughprompt", `{"prompt_file":"prompts/bad.md/x"}`)
	tpath := func(name string) string { return filepath.Join(f.templatesDir(), name+".json") }

	for _, tc := range []struct {
		name    string
		kind    string
		details map[string]any
	}{
		{"missing", KindNotFound, map[string]any{"selectors": []string{}, "paths": []string{}, "templates": []string{"missing"}}},
		{"adir", KindNotFound, map[string]any{"selectors": []string{}, "paths": []string{}, "templates": []string{"adir"}}},
		{"dangling", KindNotFound, map[string]any{"selectors": []string{}, "paths": []string{}, "templates": []string{"dangling"}}},
		{"loop", KindNotFound, map[string]any{"selectors": []string{}, "paths": []string{}, "templates": []string{"loop"}}},
		{"loopprompt", KindNotFound, map[string]any{"selectors": []string{}, "paths": []string{filepath.Join(f.templatesDir(), "prompts/loop.md")}, "templates": []string{}}},
		{"throughprompt", KindNotFound, map[string]any{"selectors": []string{}, "paths": []string{filepath.Join(f.templatesDir(), "prompts/bad.md/x")}, "templates": []string{}}},
		{"corrupt", KindCorrupt, map[string]any{"path": tpath("corrupt")}},
		{"badjob", KindCorrupt, map[string]any{"path": tpath("badjob")}},
		{"escape", KindCorrupt, map[string]any{"path": tpath("escape")}},
		{"noprompt", KindNotFound, map[string]any{"selectors": []string{}, "paths": []string{filepath.Join(f.templatesDir(), "prompts/none.md")}, "templates": []string{}}},
		{"dirprompt", KindNotFound, map[string]any{"selectors": []string{}, "paths": []string{filepath.Join(f.templatesDir(), "prompts")}, "templates": []string{}}},
		{"badprompt", KindCorrupt, map[string]any{"path": filepath.Join(f.templatesDir(), "prompts/bad.md")}},
		{"nulprompt", KindCorrupt, map[string]any{"path": filepath.Join(f.templatesDir(), "prompts/nul.md")}},
	} {
		// Even when the input's prompt would replace the template's.
		env := f.spawn(`"template":"`+tc.name+`"`, `"prompt":"mine"`)
		wantKind(t, env, tc.kind)
		for k, v := range tc.details {
			if !reflect.DeepEqual(env.Error.Details[k], v) {
				t.Errorf("%s: details[%s] = %v, want %v", tc.name, k, env.Error.Details[k], v)
			}
		}
	}
	if len(f.launches) != 0 || len(f.reservationNames("")) != 0 {
		t.Error("something was launched or reserved")
	}

	// An unreadable template is io; so is an unreadable prompt file.
	f.template("ok", `{"prompt_file":"prompts/ok.md"}`)
	f.templateFile("prompts/ok.md", "p")
	for _, path := range []string{tpath("ok"), filepath.Join(f.templatesDir(), "prompts/ok.md")} {
		f.hook = fsys.ErrnoAt(fsys.OpReadReg, path, 1, syscall.EACCES)
		env := f.spawn(`"template":"ok"`)
		wantKind(t, env, KindIO)
		if env.Error.Details["path"] != path || env.Error.Details["code"] != "EACCES" {
			t.Errorf("%s: %+v", path, env.Error.Details)
		}
	}
}

// The template's errors come before every error that needs more than the
// input and the template: busy, the merged cwd, the terminal; and after
// config.toml.
func TestSpawnTemplateErrorOrder(t *testing.T) {
	f := newSpawnFixture(t)
	f.template("corrupt", `{`)
	f.template("nocwd", `{}`)
	f.cwd = filepath.Join(f.cwd, "missing")
	f.vars["KITTY_LISTEN_ON"] = ""
	root, err := fsys.OS{}.OpenRoot(f.loc.SessionsDir())
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(f.loc.SessionsDir(), 0o700); err == nil {
			root, err = fsys.OS{}.OpenRoot(f.loc.SessionsDir())
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	// config.toml is read before the template.
	f.config("retain_days = -1")
	wantKind(t, f.spawn(`"template":"corrupt"`), KindCorrupt)
	if env := f.spawn(`"template":"corrupt"`); env.Error.Details["path"] == filepath.Join(f.templatesDir(), "corrupt.json") {
		t.Error("the template was read before config.toml")
	}
	f.config("")
	// The template, before the merged cwd, the terminal, and the lock.
	env := f.spawn(`"template":"corrupt"`)
	wantKind(t, env, KindCorrupt)
	if env.Error.Details["path"] != filepath.Join(f.templatesDir(), "corrupt.json") {
		t.Errorf("%+v", env.Error.Details)
	}
	wantKind(t, f.spawn(`"template":"missing"`), KindNotFound)
	// The merged cwd, after the template's own errors, before the terminal.
	wantKind(t, f.spawnRaw(`{"template":"nocwd"}`), KindInvalidInput)
	wantKind(t, f.spawn(`"template":"nocwd"`), KindNotFound) // cwd given, but missing
	f.cwd = t.TempDir()
	wantKind(t, f.spawn(`"template":"nocwd"`), KindTerminal)
	f.vars["KITTY_LISTEN_ON"] = "unix:/kitty"
	wantKind(t, f.spawn(`"template":"nocwd"`), KindBusy)
}

// A template's vars on a backend that sets none are refused before anything
// is reserved, as the input's are.
func TestSpawnTemplateVarsUnsupported(t *testing.T) {
	f := newSpawnFixture(t)
	f.only = launching{bare: bare{f.kit}}
	f.template("v", `{"vars":{"A":"1"}}`)
	env := f.spawn(`"template":"v"`)
	wantKind(t, env, KindTerminal)
	if env.Error.Details["reason"] != "unsupported" {
		t.Errorf("%+v", env.Error.Details)
	}
	noReservations(t, f)
	if len(f.launches) != 0 {
		t.Error("launched")
	}
}
