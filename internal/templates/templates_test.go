package templates

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
)

const home = "/home/me"

// tdir is a templates directory in a temporary config directory.
func tdir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "templates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func load(dir, name string) (*Template, error) { return Load(fsys.OS{}, dir, home, name) }

func str(s string) *string { return &s }

// Every row of the design-spec error table.
func TestLoadErrors(t *testing.T) {
	dir := tdir(t)
	put(t, filepath.Join(dir, "ok.json"), `{"job":"j","cwd":"~/code","args":["--model","opus"],"description":"d"}`)
	put(t, filepath.Join(dir, "bad.json"), `{"nope":1}`)
	put(t, filepath.Join(dir, "good-prompt.json"), `{"prompt_file":"prompts/p.md"}`)
	put(t, filepath.Join(dir, "prompts", "p.md"), "hello\n")
	if err := os.Mkdir(filepath.Join(dir, "adir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "dangling.json")); err != nil {
		t.Fatal(err)
	}

	tmpl, err := load(dir, "ok")
	if err != nil || *tmpl.Job != "j" || *tmpl.Cwd != "/home/me/code" || !reflect.DeepEqual(tmpl.Args, []string{"--model", "opus"}) || *tmpl.Description != "d" {
		t.Errorf("ok: %+v, %v", tmpl, err)
	}
	tmpl, err = load(dir, "good-prompt")
	if err != nil || *tmpl.Prompt != "hello\n" || *tmpl.PromptFile != "prompts/p.md" {
		t.Errorf("good-prompt: %+v, %v", tmpl, err)
	}

	var nf *NotFound
	for _, name := range []string{"missing", "adir", "dangling"} {
		if _, err := load(dir, name); !errors.As(err, &nf) || nf.Path != Path(dir, name) {
			t.Errorf("%s: %v, want not-found", name, err)
		}
	}
	var c *Corrupt
	if _, err := load(dir, "bad"); !errors.As(err, &c) || c.Path != Path(dir, "bad") || !strings.Contains(c.Detail, "/nope") {
		t.Errorf("bad: %v", err)
	}
	// An unreadable file is io, not corrupt.
	var ioe *IOError
	f := fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, Path(dir, "ok"), 1, syscall.EACCES)}
	if _, err := Load(f, dir, home, "ok"); !errors.As(err, &ioe) || !errors.Is(err, syscall.EACCES) {
		t.Errorf("unreadable: %v", err)
	}
}

// The template file alone, one problem each: all corrupt, with the path of the
// template.
func TestLoadCorrupt(t *testing.T) {
	dir := tdir(t)
	for name, content := range map[string]string{
		"not-json":       `{`,
		"two-objects":    `{} {}`,
		"array":          `[]`,
		"repeated":       `{"job":"a","job":"b"}`,
		"unknown":        `{"template":"x"}`,
		"extra":          `{"extra":{}}`,
		"bad-type":       `{"type":"window"}`,
		"type-number":    `{"type":1}`,
		"job-digits":     `{"job":"12"}`,
		"job-underscore": `{"job":"a_b"}`,
		"cwd-relative":   `{"cwd":"code"}`,
		"cwd-tilde-user": `{"cwd":"~me/code"}`,
		"cwd-nul":        `{"cwd":"/a\u0000b"}`,
		"name-empty":     `{"name":""}`,
		"vars-key":       `{"vars":{"1a":"x"}}`,
		"vars-value":     `{"vars":{"A":1}}`,
		"args-item":      `{"args":["a",1]}`,
		"args-nul":       `{"args":["a\u0000"]}`,
		"timeout-high":   `{"start_timeout_secs":121}`,
		"timeout-float":  `{"start_timeout_secs":1.5}`,
		"both-prompts":   `{"prompt":"a","prompt_file":"p.md"}`,
		"pf-absolute":    `{"prompt_file":"/etc/passwd"}`,
		"pf-dotdot":      `{"prompt_file":"../p.md"}`,
		"pf-dotdot-mid":  `{"prompt_file":"a/../p.md"}`,
		"pf-empty":       `{"prompt_file":""}`,
		"description":    `{"description":1}`,
		"bom":            "\xef\xbb\xbf{}",
	} {
		put(t, Path(dir, name), content)
		_, err := load(dir, name)
		var c *Corrupt
		if !errors.As(err, &c) || c.Path != Path(dir, name) || c.Detail == "" {
			t.Errorf("%s: %v, want corrupt", name, err)
		}
	}
	// Everything wrong at once is one error, with every problem.
	put(t, Path(dir, "many"), `{"type":"x","cwd":"y","job":"1"}`)
	_, err := load(dir, "many")
	for _, f := range []string{"/type", "/cwd", "/job"} {
		if err == nil || !strings.Contains(err.Error(), f) {
			t.Errorf("many: %v lacks %s", err, f)
		}
	}
	// Every valid value of each kind.
	put(t, Path(dir, "all"), `{"job":"Api","cwd":"/w","type":"os-window","name":"n","prompt":"p","args":[],"vars":{"_A1":""},"start_timeout_secs":0,"description":"d"}`)
	if _, err := load(dir, "all"); err != nil {
		t.Errorf("all: %v", err)
	}
	put(t, Path(dir, "empty"), `{}`)
	if tm, err := load(dir, "empty"); err != nil || tm.Job != nil || tm.Args != nil || tm.Vars != nil || tm.StartTimeoutSecs != nil {
		t.Errorf("empty: %+v, %v", tm, err)
	}
}

// Symlinks are followed for a template and its prompt file, wherever they
// lead.
func TestLoadFollowsSymlinks(t *testing.T) {
	dir := tdir(t)
	outside := t.TempDir()
	put(t, filepath.Join(outside, "t.json"), `{"prompt_file":"linked.md","name":"n"}`)
	put(t, filepath.Join(outside, "p.md"), "from outside")
	if err := os.Symlink(filepath.Join(outside, "t.json"), Path(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "p.md"), filepath.Join(dir, "linked.md")); err != nil {
		t.Fatal(err)
	}
	tm, err := load(dir, "linked")
	if err != nil || *tm.Prompt != "from outside" || *tm.Name != "n" {
		t.Errorf("%+v, %v", tm, err)
	}
	// A link to a directory is no prompt file.
	put(t, Path(dir, "dirprompt"), `{"prompt_file":"sub"}`)
	if err := os.Symlink(outside, filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	var nf *NotFound
	if _, err := load(dir, "dirprompt"); !errors.As(err, &nf) || nf.Path != filepath.Join(dir, "sub") {
		t.Errorf("%v", err)
	}
}

func TestLoadPromptFile(t *testing.T) {
	dir := tdir(t)
	put(t, filepath.Join(dir, "p", "ok.md"), "line one\n\nline three")
	put(t, filepath.Join(dir, "p", "bad-utf8.md"), "a\xffb")
	put(t, filepath.Join(dir, "p", "nul.md"), "a\x00b")
	if err := os.Mkdir(filepath.Join(dir, "p", "adir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, pf := range map[string]string{"missing": "p/none.md", "dir": "p/adir", "bad-utf8": "p/bad-utf8.md", "nul": "p/nul.md"} {
		put(t, Path(dir, name), `{"description":"keep me","prompt_file":"`+pf+`"}`)
	}
	put(t, Path(dir, "ok"), `{"prompt_file":"p/ok.md"}`)

	if tm, err := load(dir, "ok"); err != nil || *tm.Prompt != "line one\n\nline three" {
		t.Errorf("ok: %+v, %v", tm, err)
	}
	var nf *NotFound
	for name, pf := range map[string]string{"missing": "p/none.md", "dir": "p/adir"} {
		tm, err := load(dir, name)
		if !errors.As(err, &nf) || nf.Path != filepath.Join(dir, pf) {
			t.Errorf("%s: %v", name, err)
		}
		if tm == nil || *tm.Description != "keep me" {
			t.Errorf("%s: the template is not returned with a prompt file's error", name)
		}
	}
	for name, pf := range map[string]string{"bad-utf8": "p/bad-utf8.md", "nul": "p/nul.md"} {
		var c *Corrupt
		if _, err := load(dir, name); !errors.As(err, &c) || c.Path != filepath.Join(dir, pf) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A bad prompt file is an error even when the input's prompt would replace
	// it: Load has no input.
	var ioe *IOError
	f := fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, filepath.Join(dir, "p/ok.md"), 1, syscall.EIO)}
	if _, err := Load(f, dir, home, "ok"); !errors.As(err, &ioe) || ioe.Path != filepath.Join(dir, "p/ok.md") {
		t.Errorf("unreadable prompt file: %v", err)
	}
}

func TestLoadTilde(t *testing.T) {
	dir := tdir(t)
	put(t, Path(dir, "t"), `{"cwd":"~/code/api"}`)
	put(t, Path(dir, "root"), `{"cwd":"~/"}`)
	put(t, Path(dir, "abs"), `{"cwd":"/srv"}`)
	for name, want := range map[string]string{"t": "/home/me/code/api", "root": "/home/me/", "abs": "/srv"} {
		if tm, err := load(dir, name); err != nil || *tm.Cwd != want {
			t.Errorf("%s: %+v, %v", name, tm, err)
		}
	}
	if tm, err := Load(fsys.OS{}, dir, "/home/me/", "t"); err != nil || *tm.Cwd != "/home/me/code/api" {
		t.Errorf("HOME with a trailing slash: %+v, %v", tm, err)
	}
}

// Every merge row.
func TestMerge(t *testing.T) {
	zero, five := int64(0), int64(5)
	tm := &Template{
		Job: str("tjob"), Cwd: str("/t"), Type: str("split"), Name: str("tname"), Prompt: str("tprompt"),
		Args:             []string{"--t1", "--t2"},
		Vars:             []placement.Var{{Name: "A", Value: "t"}, {Name: "B", Value: "t"}},
		StartTimeoutSecs: &five,
	}
	noCwd := func() (string, error) { t.Error("the working directory was asked for"); return "", nil }
	for _, tc := range []struct {
		name string
		in   model.SpawnInput
		t    *Template
		want Merged
	}{
		{"nothing", model.SpawnInput{Cwd: "/i"}, nil,
			Merged{Cwd: "/i", Type: "tab", Args: []string{}, StartTimeoutSecs: 15}},
		{"the template alone", model.SpawnInput{}, tm,
			Merged{Job: "tjob", Cwd: "/t", Type: "split", Name: "tname", Prompt: "tprompt", Args: []string{"--t1", "--t2"},
				Vars: []placement.Var{{Name: "A", Value: "t"}, {Name: "B", Value: "t"}}, StartTimeoutSecs: 5}},
		{"the input wins", model.SpawnInput{Template: "x", Job: "ijob", Cwd: "/i", Type: "os-window", Name: "iname", Prompt: str("iprompt"), StartTimeoutSecs: &zero}, tm,
			Merged{Template: "x", Job: "ijob", Cwd: "/i", Type: "os-window", Name: "iname", Prompt: "iprompt", Args: []string{"--t1", "--t2"},
				Vars: []placement.Var{{Name: "A", Value: "t"}, {Name: "B", Value: "t"}}, StartTimeoutSecs: 0}},
		{"an empty prompt replaces the template's, leaving none", model.SpawnInput{Cwd: "/i", Prompt: str("")}, tm,
			Merged{Job: "tjob", Cwd: "/i", Type: "split", Name: "tname", Args: []string{"--t1", "--t2"},
				Vars: []placement.Var{{Name: "A", Value: "t"}, {Name: "B", Value: "t"}}, StartTimeoutSecs: 5}},
		{"args are concatenated, template first", model.SpawnInput{Cwd: "/i", Args: []string{"--i1"}}, tm,
			Merged{Job: "tjob", Cwd: "/i", Type: "split", Name: "tname", Prompt: "tprompt", Args: []string{"--t1", "--t2", "--i1"},
				Vars: []placement.Var{{Name: "A", Value: "t"}, {Name: "B", Value: "t"}}, StartTimeoutSecs: 5}},
		{"vars merge, the input winning per key", model.SpawnInput{Cwd: "/i", Vars: []placement.Var{{Name: "B", Value: "i"}, {Name: "C", Value: "i"}}}, tm,
			Merged{Job: "tjob", Cwd: "/i", Type: "split", Name: "tname", Prompt: "tprompt", Args: []string{"--t1", "--t2"},
				Vars: []placement.Var{{Name: "A", Value: "t"}, {Name: "B", Value: "i"}, {Name: "C", Value: "i"}}, StartTimeoutSecs: 5}},
		{"the name defaults to the merged job", model.SpawnInput{Cwd: "/i", Job: "ijob"}, &Template{},
			Merged{Job: "ijob", Cwd: "/i", Type: "tab", Name: "ijob", Args: []string{}, StartTimeoutSecs: 15}},
		{"the name defaults to the template's job", model.SpawnInput{Cwd: "/i"}, &Template{Job: str("tjob")},
			Merged{Job: "tjob", Cwd: "/i", Type: "tab", Name: "tjob", Args: []string{}, StartTimeoutSecs: 15}},
		{"a template's value wins over a default", model.SpawnInput{Cwd: "/i"}, &Template{Type: str("tab"), StartTimeoutSecs: &zero},
			Merged{Cwd: "/i", Type: "tab", Args: []string{}, StartTimeoutSecs: 0}},
		{"extra is the input's alone", model.SpawnInput{Cwd: "/i"}, &Template{Description: str("d")},
			Merged{Cwd: "/i", Type: "tab", Args: []string{}, StartTimeoutSecs: 15}},
	} {
		got, err := Merge(tc.in, tc.t, noCwd)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v, %v\nwant %+v", tc.name, got, err, tc.want)
		}
	}
	// The template is not changed by a merge.
	if !reflect.DeepEqual(tm.Args, []string{"--t1", "--t2"}) || len(tm.Vars) != 2 || tm.Vars[1].Value != "t" {
		t.Errorf("template changed: %+v", tm)
	}
}

// cwd is the input's, then the template's, then the caller's, called only when
// needed.
func TestMergeCwd(t *testing.T) {
	asked := 0
	caller := func() (string, error) { asked++; return "/wd", nil }
	for _, tc := range []struct {
		name   string
		in     model.SpawnInput
		t      *Template
		caller func() (string, error)
		want   string
		asked  int
	}{
		{"input", model.SpawnInput{Cwd: "/i"}, &Template{Cwd: str("/t")}, caller, "/i", 0},
		{"template", model.SpawnInput{}, &Template{Cwd: str("/t")}, caller, "/t", 0},
		{"caller", model.SpawnInput{}, &Template{}, caller, "/wd", 1},
		{"caller, no template", model.SpawnInput{}, nil, caller, "/wd", 1},
	} {
		asked = 0
		got, err := Merge(tc.in, tc.t, tc.caller)
		if err != nil || got.Cwd != tc.want || asked != tc.asked {
			t.Errorf("%s: %q, %v, asked %d", tc.name, got.Cwd, err, asked)
		}
	}
	// -i supplies none: no cwd is an error, however the rest is.
	for _, tc := range []struct {
		name string
		t    *Template
	}{{"no template", nil}, {"a template without cwd", &Template{Job: str("j")}}} {
		if _, err := Merge(model.SpawnInput{}, tc.t, nil); !errors.Is(err, ErrNoCwd) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// A working directory that can't be determined is its own error, only
	// when needed.
	boom := func() (string, error) { return "", syscall.ENOENT }
	var wd *WorkingDirError
	if _, err := Merge(model.SpawnInput{}, &Template{}, boom); !errors.As(err, &wd) || !errors.Is(err, syscall.ENOENT) {
		t.Errorf("%v", err)
	}
	if _, err := Merge(model.SpawnInput{Cwd: "/i"}, nil, boom); err != nil {
		t.Errorf("%v", err)
	}
}

func TestList(t *testing.T) {
	dir := tdir(t)
	outside := t.TempDir()
	put(t, Path(dir, "zeta"), `{"description":"last"}`)
	put(t, Path(dir, "alpha"), `{"description":"first","cwd":"~/code","prompt_file":"prompts/a.md"}`)
	put(t, filepath.Join(dir, "prompts", "a.md"), "text")
	put(t, Path(dir, "a-b"), `{}`)
	put(t, Path(dir, "broken"), `{"bogus":true}`)
	put(t, Path(dir, "noprompt"), `{"description":"d","prompt_file":"nowhere.md"}`)
	put(t, filepath.Join(outside, "l.json"), `{"description":"linked"}`)
	if err := os.Symlink(filepath.Join(outside, "l.json"), Path(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "nothing"), Path(dir, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, Path(dir, "dirlink")); err != nil {
		t.Fatal(err)
	}
	// Ignored: a capital, a hidden entry, a subdirectory, another extension.
	put(t, filepath.Join(dir, "Review.json"), `{}`)
	put(t, filepath.Join(dir, ".x.json"), `{}`)
	put(t, filepath.Join(dir, "notes.txt"), `x`)
	put(t, filepath.Join(dir, "12.json"), `{}`) // all digits is no name
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A top-level .json prompt file is simply a corrupt template.
	put(t, filepath.Join(dir, "p.json"), `"just text"`)

	list, err := List(fsys.OS{}, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]Entry{}
	for _, e := range list {
		names = append(names, e.Name)
		byName[e.Name] = e
		if e.Path != Path(dir, e.Name) {
			t.Errorf("%s: path %s", e.Name, e.Path)
		}
	}
	// Sorted by name, bytewise: "a-b" before "alpha".
	if want := []string{"a-b", "alpha", "broken", "dangling", "dirlink", "linked", "noprompt", "p", "zeta"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names %v, want %v", names, want)
	}
	for _, name := range []string{"a-b", "alpha", "linked", "zeta"} {
		if e := byName[name]; e.Problem != nil || e.Template == nil {
			t.Errorf("%s: %+v", name, e)
		}
	}
	if d := byName["linked"].Template.Description; d == nil || *d != "linked" {
		t.Errorf("linked: %v", d)
	}
	if o := byName["alpha"].Template.Object; o == nil || o.Len() != 3 {
		t.Errorf("alpha's object: %+v", o)
	} else if v, _ := o.Get("cwd"); v != "~/code" {
		t.Errorf("alpha's cwd is not as written: %v", v)
	}
	for name, kind := range map[string]string{"broken": "corrupt", "p": "corrupt", "dangling": "not-found", "dirlink": "not-found"} {
		e := byName[name]
		if e.Problem == nil || e.Problem.Kind != kind || e.Problem.Path != Path(dir, name) || e.Template != nil {
			t.Errorf("%s: %+v", name, e)
		}
	}
	// A bad prompt file leaves the template and its description.
	e := byName["noprompt"]
	if e.Problem == nil || e.Problem.Kind != "not-found" || e.Problem.Path != filepath.Join(dir, "nowhere.md") ||
		e.Template == nil || *e.Template.Description != "d" {
		t.Errorf("noprompt: %+v", e)
	}
}

func TestListIO(t *testing.T) {
	dir := tdir(t)
	put(t, Path(dir, "a"), `{}`)
	put(t, Path(dir, "b"), `{}`)
	f := fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, Path(dir, "a"), 1, syscall.EACCES)}
	list, err := List(f, dir, home)
	if err != nil || len(list) != 2 {
		t.Fatalf("%v, %v", list, err)
	}
	if p := list[0].Problem; p == nil || p.Kind != "io" || p.Detail != "EACCES" || p.Path != Path(dir, "a") || list[0].Template != nil {
		t.Errorf("a: %+v", list[0])
	}
	if list[1].Problem != nil {
		t.Errorf("b: %+v", list[1])
	}
	// A directory that can't be listed is io; a missing one is none.
	if list, err := List(fsys.OS{}, filepath.Join(dir, "none"), home); err != nil || len(list) != 0 {
		t.Errorf("missing: %v, %v", list, err)
	}
	if list, err := List(fsys.OS{}, filepath.Join(t.TempDir(), "no", "config"), home); err != nil || len(list) != 0 {
		t.Errorf("no config directory: %v, %v", list, err)
	}
	file := filepath.Join(t.TempDir(), "templates")
	put(t, file, "x")
	var ioe *IOError
	if _, err := List(fsys.OS{}, file, home); !errors.As(err, &ioe) || ioe.Path != file {
		t.Errorf("a file for a directory: %v", err)
	}
}
