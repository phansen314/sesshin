package loc

import (
	"errors"
	"os"
	"testing"
)

const home = "/home/u"

// value is one way a variable can be set: set false is unset.
type value struct {
	name string
	set  bool
	v    string
}

// values are the ways each optional variable is tried. Only absolute values
// are used, cleaned.
var values = []value{
	{"unset", false, ""},
	{"empty", true, ""},
	{"relative", true, "rel/dir"},
	{"absolute", true, "/x/dir"},
	{"trailing slash", true, "/x/dir/"},
	{"unclean", true, "/x//y/../dir/."},
}

func used(v value) bool { return v.v != "" && v.v[0] == '/' }

func env(m map[string]value) func(string) string {
	return func(k string) string { return m[k].v }
}

// defaults are the locations with only HOME set.
var defaults = map[string]Locations{
	"linux": {
		ConfigDir:      "/home/u/.config/sesshin",
		StateDir:       "/home/u/.local/state/sesshin",
		ClaudeSettings: "/home/u/.claude/settings.json",
	},
	"darwin": {
		ConfigDir:      "/home/u/Library/Application Support/sesshin",
		StateDir:       "/home/u/Library/Application Support/sesshin/state",
		ClaudeSettings: "/home/u/.claude/settings.json",
	},
}

func TestResolveVariables(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		for _, variable := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "CLAUDE_CONFIG_DIR"} {
			for _, v := range values {
				t.Run(goos+"/"+variable+"/"+v.name, func(t *testing.T) {
					e := map[string]value{"HOME": {"absolute", true, home}}
					if v.set {
						e[variable] = v
					}
					want := defaults[goos]
					if used(v) {
						switch {
						case variable == "CLAUDE_CONFIG_DIR":
							want.ClaudeSettings = "/x/dir/settings.json"
						case goos == "darwin":
							// XDG is Linux only.
						case variable == "XDG_CONFIG_HOME":
							want.ConfigDir = "/x/dir/sesshin"
						case variable == "XDG_STATE_HOME":
							want.StateDir = "/x/dir/sesshin"
						}
					}
					got, err := Resolve(goos, env(e))
					if err != nil {
						t.Fatal(err)
					}
					if got != want {
						t.Errorf("got %+v\nwant %+v", got, want)
					}
				})
			}
		}
	}
}

func TestResolveAllVariables(t *testing.T) {
	e := env(map[string]value{
		"HOME":              {"", true, home},
		"XDG_CONFIG_HOME":   {"", true, "/c/"},
		"XDG_STATE_HOME":    {"", true, "/s/"},
		"CLAUDE_CONFIG_DIR": {"", true, "/k/"},
	})
	want := map[string]Locations{
		"linux":  {ConfigDir: "/c/sesshin", StateDir: "/s/sesshin", ClaudeSettings: "/k/settings.json"},
		"darwin": {ConfigDir: defaults["darwin"].ConfigDir, StateDir: defaults["darwin"].StateDir, ClaudeSettings: "/k/settings.json"},
	}
	for goos, w := range want {
		got, err := Resolve(goos, e)
		if err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("%s: got %+v\nwant %+v", goos, got, w)
		}
	}
}

func TestResolveHome(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		v    string
		want string // HOME as cleaned; "" for an EnvironmentError
	}{
		{"unset", false, "", ""},
		{"empty", true, "", ""},
		{"relative", true, "home/u", ""},
		{"dot", true, ".", ""},
		{"tilde", true, "~", ""},
		{"absolute", true, "/home/u", "/home/u"},
		{"trailing slash", true, "/home/u/", "/home/u"},
		{"unclean", true, "//home/./x/../u", "/home/u"},
		{"root", true, "/", "/"},
	}
	for _, goos := range []string{"linux", "darwin"} {
		for _, c := range cases {
			t.Run(goos+"/"+c.name, func(t *testing.T) {
				// Every other variable absolute: HOME is required anyway.
				e := map[string]value{
					"XDG_CONFIG_HOME":   {"", true, "/c"},
					"XDG_STATE_HOME":    {"", true, "/s"},
					"CLAUDE_CONFIG_DIR": {"", true, "/k"},
				}
				if c.set {
					e["HOME"] = value{"", true, c.v}
				}
				got, err := Resolve(goos, env(e))
				if c.want == "" {
					var ee *EnvironmentError
					if !errors.As(err, &ee) {
						t.Fatalf("got %+v, %v; want an EnvironmentError", got, err)
					}
					if ee.Variable != "HOME" || ee.Value != c.v {
						t.Errorf("got %+v", ee)
					}
					if got != (Locations{}) {
						t.Errorf("locations %+v with an error", got)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got.ClaudeSettings != "/k/settings.json" {
					t.Errorf("got %+v", got)
				}
				if goos == "linux" && (got.ConfigDir != "/c/sesshin" || got.StateDir != "/s/sesshin") {
					t.Errorf("got %+v", got)
				}
				if goos == "darwin" {
					w := c.want + "/Library/Application Support/sesshin"
					if c.want == "/" {
						w = "/Library/Application Support/sesshin"
					}
					if got.ConfigDir != w || got.StateDir != w+"/state" {
						t.Errorf("got %+v", got)
					}
				}
			})
		}
	}
}

func TestEnvironmentErrorMessage(t *testing.T) {
	for _, c := range []struct {
		value, want string
	}{
		{"", "HOME is not set"},
		{"rel", `HOME is not an absolute path: "rel"`},
	} {
		if got := (&EnvironmentError{Variable: "HOME", Value: c.value}).Error(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// Resolve reads no variable but these, so nothing else can change a location.
func TestResolveReads(t *testing.T) {
	allowed := map[string]bool{"HOME": true, "XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true, "CLAUDE_CONFIG_DIR": true}
	for _, goos := range []string{"linux", "darwin"} {
		_, _ = Resolve(goos, func(k string) string {
			if !allowed[k] {
				t.Errorf("%s: reads %s", goos, k)
			}
			return home
		})
	}
}

// Resolution creates nothing, and leaves symlinks unresolved.
func TestResolveTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/nonexistent", dir+"/link"); err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"linux", "darwin"} {
		got, err := Resolve(goos, env(map[string]value{"HOME": {"", true, dir + "/link"}}))
		if err != nil {
			t.Fatal(err)
		}
		if got.ClaudeSettings != dir+"/link/.claude/settings.json" {
			t.Errorf("%s: got %+v", goos, got)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("%d entries in HOME, want only the symlink", len(entries))
	}
}

func TestSessionDirs(t *testing.T) {
	l := Locations{StateDir: "/s/sesshin"}
	if got := l.SessionsDir(); got != "/s/sesshin/sessions" {
		t.Errorf("SessionsDir %q", got)
	}
	if got := l.SessionDir("0b0d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3e"); got != "/s/sesshin/sessions/0b0d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3e" {
		t.Errorf("SessionDir %q", got)
	}
}
