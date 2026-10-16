package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

// templatesRaw runs templates on the JSON input as given.
func (f *pruneFixture) templatesRaw(in string) Envelope {
	f.t.Helper()
	ti, e := DecodeInput([]byte(in), DecodeTemplatesInput)
	if e != nil {
		return Failed(e)
	}
	env := Templates(ti, f.env())
	checkEnvelope(f.t, env, "templates-output")
	return env
}

func (f *pruneFixture) templates() TemplatesOutput {
	f.t.Helper()
	env := f.templatesRaw(`{}`)
	if !env.OK {
		f.t.Fatalf("templates failed: %+v", env.Error)
	}
	if len(env.Warnings) != 0 {
		f.t.Fatalf("warnings %+v", env.Warnings)
	}
	return env.Result.(TemplatesOutput)
}

// Operations.md, templates: sorted by name, with each one's description and
// content as parsed, and every problem spawn would raise in its entry.
func TestTemplates(t *testing.T) {
	f := newPruneFixture(t)
	f.template("review", `{"description":"Review a PR","cwd":"~/code","prompt_file":"prompts/review.md","args":["--model","opus"]}`)
	f.templateFile("prompts/review.md", "Review it.")
	f.template("opus", `{"args":["--model","opus"]}`)
	f.template("broken", `{"nope":1}`)
	f.template("lost", `{"description":"has a lost prompt","prompt_file":"prompts/none.md"}`)
	f.templateFile("Upper.json", `{}`)
	f.templateFile("notes.txt", `x`)

	out := f.templates()
	var names []string
	for _, e := range out.Templates {
		names = append(names, e.Name)
	}
	if want := []string{"broken", "lost", "opus", "review"}; !slices.Equal(names, want) {
		t.Fatalf("names %v, want %v", names, want)
	}
	byName := map[string]TemplateEntry{}
	for _, e := range out.Templates {
		byName[e.Name] = e
		if e.Path != filepath.Join(f.templatesDir(), e.Name+".json") {
			t.Errorf("%s: path %s", e.Name, e.Path)
		}
	}
	if e := byName["review"]; e.Problem != nil || e.Description == nil || *e.Description != "Review a PR" ||
		string(tmplJSON(t, e.Template)) != `{"description":"Review a PR","cwd":"~/code","prompt_file":"prompts/review.md","args":["--model","opus"]}` {
		t.Errorf("review: %+v, %s", e, tmplJSON(t, e.Template))
	}
	if e := byName["opus"]; e.Problem != nil || e.Description != nil || e.Template == nil {
		t.Errorf("opus: %+v", e)
	}
	if e := byName["broken"]; e.Problem == nil || e.Problem.Kind != "corrupt" || e.Problem.Path != e.Path || e.Description != nil || e.Template != nil {
		t.Errorf("broken: %+v", e)
	}
	if e := byName["lost"]; e.Problem == nil || e.Problem.Kind != "not-found" || e.Problem.Path != filepath.Join(f.templatesDir(), "prompts/none.md") ||
		e.Description == nil || *e.Description != "has a lost prompt" || e.Template == nil {
		t.Errorf("lost: %+v", e)
	}
}

func tmplJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A missing templates/, and a missing config directory, are an empty list;
// the state directory is never touched.
func TestTemplatesEmpty(t *testing.T) {
	f := newPruneFixture(t)
	if out := f.templates(); out.Templates == nil || len(out.Templates) != 0 {
		t.Errorf("no config directory: %+v", out)
	}
	if b := tmplJSON(t, f.templates()); string(b) != `{"templates":[]}` {
		t.Errorf("%s", b)
	}
	f.config("")
	if out := f.templates(); len(out.Templates) != 0 {
		t.Errorf("no templates/: %+v", out)
	}
	if err := os.MkdirAll(f.templatesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if out := f.templates(); len(out.Templates) != 0 {
		t.Errorf("empty templates/: %+v", out)
	}
	if _, err := os.Stat(f.loc.StateDir); !os.IsNotExist(err) {
		t.Errorf("the state directory: %v", err)
	}
}

// Takes no migration status: state.json is not read.
func TestTemplatesReadsNoState(t *testing.T) {
	f := newPruneFixture(t)
	f.template("a", `{}`)
	var touched []string
	f.hook = func(op fsys.Op) error {
		p := op.Path
		if op.Root != "" {
			p = filepath.Join(op.Root, op.Path)
		}
		if !isUnder(p, f.loc.ConfigDir) {
			touched = append(touched, p)
		}
		return nil
	}
	// A state.json that would raise a migration warning, were it read.
	if err := os.MkdirAll(f.loc.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.statePath(), []byte(`{"schema":2,"last_id":1,"migration":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.templates() // fails on any warning
	if len(touched) != 0 {
		t.Errorf("touched %v outside the config directory", touched)
	}
}

func isUnder(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !(len(rel) > 2 && rel[:3] == "../")
}

func TestTemplatesErrors(t *testing.T) {
	f := newPruneFixture(t)
	for _, in := range []string{`{"x":1}`, `[]`, `1`, `{"templates":[]}`} {
		wantKind(t, f.templatesRaw(in), KindInvalidInput)
	}
	home := f.home
	f.home = ""
	wantKind(t, f.templatesRaw(`{}`), KindEnvironment)
	f.home = home

	// templates/ that can't be listed is io; an entry that can't be read is
	// its problem, not an error.
	f.template("a", `{}`)
	f.template("b", `{}`)
	f.hook = fsys.ErrnoAt(fsys.OpReadFile, filepath.Join(f.templatesDir(), "a.json"), 1, syscall.EACCES)
	out := f.templates()
	if p := out.Templates[0].Problem; p == nil || p.Kind != "io" || p.Detail != "EACCES" {
		t.Errorf("a: %+v", out.Templates[0])
	}
	f.hook = nil
	if err := os.RemoveAll(f.templatesDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.templatesDir(), []byte("x"), 0o600); err != nil { // a file for the directory
		t.Fatal(err)
	}
	env := f.templatesRaw(`{}`)
	wantKind(t, env, KindIO)
	if env.Error.Details["path"] != f.templatesDir() {
		t.Errorf("%+v", env.Error.Details)
	}
}

func TestTemplatesInputAgreesWithSchema(t *testing.T) {
	agreeInput(t, "templates-input", `{}`, DecodeTemplatesInput, nil, `{"x": 1}`)
}

// Operations.md, Session view: template follows source, is the stored name,
// and is null without a usable sesshin.json or for a session with none; the
// projection may name it.
func TestViewTemplate(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.write(uuidA, "sesshin.json", []byte(`{"schema": 3,"id":7,"job":null,"source":"spawn","placement":null,"extra":{},"template":"review"}`))
	f.running(uuidB, time.Minute, 12)
	f.write(uuidB, "sesshin.json", []byte(`{"schema": 3,"id":8,"job":null,"source":"hook","placement":null,"extra":{},"template":null}`))
	f.running(uuidC, time.Minute, 13) // no sesshin.json

	_, l := f.list(`{"liveness":"all"}`)
	byID := map[string]int{}
	for i, u := range l.uuids() {
		byID[u] = i
	}
	templateOf := func(raw json.RawMessage) string {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return string(m["template"])
	}
	for id, want := range map[string]string{uuidA: `"review"`, uuidB: `null`, uuidC: `null`} {
		if got := templateOf(l.Raw[byID[id]]); got != want {
			t.Errorf("%s: template %s, want %s", id, got, want)
		}
	}
	if got := keys(t, l.Raw[byID[uuidA]]); slices.Index(got, "template") != slices.Index(got, "source")+1 {
		t.Errorf("keys %v", got)
	}
	_, l = f.list(`{"liveness":"all","fields":["template","job"]}`)
	if got := keys(t, l.Raw[byID[uuidA]]); !slices.Equal(got, []string{"id", "session_id", "job", "template"}) {
		t.Errorf("projection keys %v", got)
	}
	env := f.show("7", false)
	if !env.OK || env.Result.(ShowOutput).Session.Template == nil || *env.Result.(ShowOutput).Session.Template != "review" {
		t.Errorf("show: %+v", env)
	}
}

// 002-template through ops.Migrate: a state directory recorded at migration 1
// is brought to migration 2, its sesshin.json files converted, and the
// reservation beside them left alone.
func TestMigrateFixture002(t *testing.T) {
	f := newPruneFixture(t)
	copyTo(t, filepath.Join(fixture002, "before"), f.loc.StateDir)
	out, _ := f.migrated(false)
	if out.From != 1 || out.To != 2 || len(out.Applied) != 1 || out.Applied[0] != (AppliedStep{2, "template"}) || len(out.Converted) != 3 {
		t.Errorf("output %+v", out)
	}
	f.wantTree(afterTreeOf(t, fixture002))

	// Idempotent.
	out, _ = f.migrated(false)
	if out.From != 2 || len(out.Applied) != 0 || len(out.Converted) != 0 {
		t.Errorf("second run: %+v", out)
	}
	f.wantTree(afterTreeOf(t, fixture002))
}
