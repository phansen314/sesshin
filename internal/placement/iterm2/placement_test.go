package iterm2

import (
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/placement"
)

const (
	uuidUpper = "2E30574E-F9EE-4D62-BE94-56A54E66E0D5"
	uuidLower = "2e30574e-f9ee-4d62-be94-56a54e66e0d5"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func enc(t *testing.T, p *jsonio.Object) string {
	t.Helper()
	if p == nil {
		return "null"
	}
	b, err := jsonio.MarshalLine(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func placed(t *testing.T, id string) *jsonio.Object {
	t.Helper()
	return &jsonio.Object{Members: []jsonio.Member{{Key: "terminal", Value: "iterm2"}, {Key: "session_id", Value: id}}}
}

func TestRecognize(t *testing.T) {
	good := map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w1t0p0:" + uuidUpper}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range good {
			m[a] = b
		}
		m[k] = v
		return m
	}
	want := `{"terminal":"iterm2","session_id":"` + uuidUpper + `"}`
	for name, tc := range map[string]struct {
		goos string
		env  map[string]string
		want string
	}{
		"capitals":          {"darwin", good, want},
		"lowercase kept up": {"darwin", with("ITERM_SESSION_ID", "w0t0p0:"+uuidLower), want},
		"last colon":        {"darwin", with("ITERM_SESSION_ID", "a:b:"+uuidUpper), want},
		"linux":             {"linux", good, "null"},
		"other terminal":    {"darwin", with("TERM_PROGRAM", "vscode"), "null"},
		"no terminal":       {"darwin", with("TERM_PROGRAM", ""), "null"},
		"no prefix":         {"darwin", with("ITERM_SESSION_ID", uuidUpper), "null"},
		"not a uuid":        {"darwin", with("ITERM_SESSION_ID", "w0t0p0:nope"), "null"},
		"empty":             {"darwin", with("ITERM_SESSION_ID", ""), "null"},
		"trailing colon":    {"darwin", with("ITERM_SESSION_ID", "w0t0p0:"+uuidUpper+":"), "null"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := enc(t, Recognize(tc.goos, env(tc.env))); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRecognizeUsesRuntimeGOOS(t *testing.T) {
	// An empty GOOS is the system's own.
	got := Backend{}.Recognize(env(map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t0p0:" + uuidUpper}))
	if (got != nil) != (Backend{}.goos() == "darwin") {
		t.Errorf("recognized %v on %s", got, Backend{}.goos())
	}
}

// The backend's contract: validation in either case, an Address that is
// canonical across case, and nothing kept by Replace.
func TestBackendConformance(t *testing.T) {
	var b placement.Backend = Backend{GOOS: "darwin"}
	if b.Tag() != "iterm2" {
		t.Errorf("tag %q", b.Tag())
	}
	p := b.Recognize(env(map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t0p0:" + uuidLower}))
	if !b.Valid(p) {
		t.Fatalf("%s is not valid", enc(t, p))
	}
	upper, lower := placed(t, uuidUpper), placed(t, uuidLower)
	if !b.Valid(upper) || !b.Valid(lower) {
		t.Error("a UUID in either case is valid")
	}
	if enc(t, b.Address(lower)) != enc(t, b.Address(upper)) || enc(t, b.Address(lower)) != enc(t, p) {
		t.Errorf("Address %s, %s; Recognize %s", enc(t, b.Address(lower)), enc(t, b.Address(upper)), enc(t, p))
	}
	extra := &jsonio.Object{Members: append(append([]jsonio.Member(nil), upper.Members...), jsonio.Member{Key: "tab_title", Value: "x"})}
	if !b.Valid(extra) || enc(t, b.Address(extra)) != enc(t, upper) {
		t.Errorf("unknown keys: %s", enc(t, b.Address(extra)))
	}
	for name, bad := range map[string]*jsonio.Object{
		"nil":         nil,
		"kitty":       {Members: []jsonio.Member{{Key: "terminal", Value: "kitty"}, {Key: "session_id", Value: uuidUpper}}},
		"no id":       {Members: []jsonio.Member{{Key: "terminal", Value: "iterm2"}}},
		"number":      {Members: []jsonio.Member{{Key: "terminal", Value: "iterm2"}, {Key: "session_id", Value: float64(1)}}},
		"not a uuid":  placed(t, "w0t0p0"),
		"with prefix": placed(t, "w0t0p0:"+uuidUpper),
	} {
		if b.Valid(bad) || b.Address(bad) != nil {
			t.Errorf("%s is valid", name)
		}
		if _, _, ok := b.Stored(bad); ok {
			t.Errorf("%s has stored keys", name)
		}
	}
	if title, vars, ok := b.Stored(upper); !ok || title != "" || vars != nil {
		t.Errorf("Stored: %q, %v, %v", title, vars, ok)
	}
	if got := b.Replace(upper, lower, true); got != upper {
		t.Error("Replace changes next")
	}
	if b.Replace(nil, upper, false) != nil {
		t.Error("Replace of nil")
	}
	if _, ok := b.(placement.Syncer); ok {
		t.Error("iTerm2 has no sync")
	}
	if !b.(placement.Launcher).UserVars() {
		t.Error("iTerm2 sets user variables")
	}
	if b.(placement.Hinter).Hint() == "" {
		t.Error("no hint")
	}
}

// Variables names every variable Recognize reads, recognized or not.
func TestVariables(t *testing.T) {
	b := Backend{GOOS: "darwin"}
	names := b.Variables()
	for _, vars := range []map[string]string{
		{"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t0p0:" + uuidUpper},
		{"TERM_PROGRAM": "iTerm.app"},
		{},
	} {
		b.Recognize(func(name string) string {
			if !slices.Contains(names, name) {
				t.Errorf("Recognize reads %s, which Variables lacks", name)
			}
			return vars[name]
		})
	}
}
