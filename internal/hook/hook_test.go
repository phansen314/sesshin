package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/hookconf"
	"github.com/phansen314/sesshin/internal/settings"
	"github.com/phansen314/sesshin/internal/statusline"
)

const session = "0b0d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3e"

var start = time.Date(2026, 10, 3, 18, 31, 51, 0, time.UTC)

// run runs the hook with stdin and the given HOME ("" leaves it unset), in a
// fresh environment of its own, and returns what it printed and the state
// directory it would use.
func run(t *testing.T, home, stdin string, args ...string) (stdout, state string) {
	t.Helper()
	var out bytes.Buffer
	env := Process{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		FS:     fsys.OS{},
		Getenv: func(k string) string {
			if k == "HOME" {
				return home
			}
			return ""
		},
		GOOS: "linux",
		Now:  func() time.Time { return start },
	}
	Run(args, env)
	return out.String(), filepath.Join(home, ".local", "state", "sesshin")
}

func readLog(t *testing.T, state string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(state, "hooks.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(b)
}

func payloadFor(id string) string { return `{"session_id":"` + id + `","cwd":"/tmp"}` }

// Hooks-spec, Log and Reading the payload; cli-spec, sesshin-hook: what each
// input logs, and what it prints.
func TestDispatch(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		stdin  string
		stdout string
		log    string // the whole of hooks.log; "" means none
	}{
		{"unknown verb", []string{"no-such"}, payloadFor(session), "",
			"2026-10-03T18:31:51Z no-such " + session + " unknown verb\n"},
		{"unknown verb with no session", []string{"--help"}, `{}`, "",
			"2026-10-03T18:31:51Z --help - unknown verb\n"},
		{"verb escaped in the log", []string{"a b\nc"}, `{}`, "",
			"2026-10-03T18:31:51Z a\\x20b\\nc - unknown verb\n"},
		{"extra arguments ignored", []string{"session-end", "extra", "--flag"}, payloadFor(session), "", ""},
		{"missing verb", nil, payloadFor(session), "",
			"2026-10-03T18:31:51Z - " + session + " no verb\n"},
		{"empty verb", []string{""}, payloadFor(session), "",
			"2026-10-03T18:31:51Z - " + session + " no verb\n"},
		{"empty stdin", []string{"stop"}, "", "", ""},
		{"blank stdin", []string{"stop"}, " \n", "", ""},
		{"empty stdin, unknown verb", []string{"no-such"}, "", "", ""},
		{"empty stdin, no verb", nil, "", "", ""},
		{"statusline, empty stdin", []string{"statusline"}, "", statusline.FallbackLine, ""},
		{"statusline, valid", []string{"statusline"}, payloadFor(session), "🧠 0% | 📁 tmp", ""},
		{"no session_id", []string{"stop"}, `{"cwd":"/tmp"}`, "",
			"2026-10-03T18:31:51Z stop - session_id is missing or not a UUID\n"},
		{"session_id not a UUID", []string{"stop"}, payloadFor("../../etc"), "",
			"2026-10-03T18:31:51Z stop - session_id is missing or not a UUID\n"},
		{"malformed payload", []string{"stop"}, `{"cwd":`, "",
			"2026-10-03T18:31:51Z stop - session_id is missing or not a UUID\n"},
		{"statusline, bad session_id", []string{"statusline"}, payloadFor("x"), statusline.FallbackLine,
			"2026-10-03T18:31:51Z statusline - session_id is missing or not a UUID\n"},
		{"statusline, malformed", []string{"statusline"}, `[`, statusline.FallbackLine,
			"2026-10-03T18:31:51Z statusline - session_id is missing or not a UUID\n"},
		{"malformed after session_id", []string{"stop"}, `{"session_id":"` + session + `","cwd":`, "",
			"2026-10-03T18:31:51Z stop " + session + " payload: unexpected EOF\n"},
		{"syntax error after session_id", []string{"stop"}, `{"session_id":"` + session + `" "cwd":"/tmp"}`, "",
			"2026-10-03T18:31:51Z stop " + session + " payload: invalid character '\"' after object key:value pair\n"},
		{"statusline, malformed after session_id", []string{"statusline"}, `{"session_id":"` + session + `",`, statusline.FallbackLine,
			"2026-10-03T18:31:51Z statusline " + session + " payload: unexpected EOF\n"},
		{"not an object", []string{"stop"}, `"` + session + `"`, "",
			"2026-10-03T18:31:51Z stop - session_id is missing or not a UUID\n"},
		{"uppercase UUID is lowercased", []string{"no-such"}, payloadFor(strings.ToUpper(session)), "",
			"2026-10-03T18:31:51Z no-such " + session + " unknown verb\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			stdout, state := run(t, home, tc.stdin, tc.args...)
			if stdout != tc.stdout {
				t.Errorf("stdout %q, want %q", stdout, tc.stdout)
			}
			if got := readLog(t, state); got != tc.log {
				t.Errorf("hooks.log %q, want %q", got, tc.log)
			}
			if tc.log == "" {
				if _, err := os.Stat(state); !os.IsNotExist(err) {
					t.Errorf("state directory created without a line to log: %v", err)
				}
			}
		})
	}
}

// With no usable HOME a hook logs nothing and does nothing (Recording an
// event, step 1); the statusline still prints its line.
func TestNoHome(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, home := range []string{"", "relative/home"} {
		for _, verb := range []string{"no-such", "stop", ""} {
			var args []string
			if verb != "" {
				args = []string{verb}
			}
			if stdout, _ := run(t, home, payloadFor("x"), args...); stdout != "" {
				t.Errorf("HOME %q, verb %q: stdout %q", home, verb, stdout)
			}
		}
		if stdout, _ := run(t, home, payloadFor(session), "statusline"); stdout != statusline.FallbackLine {
			t.Errorf("HOME %q: statusline stdout %q", home, stdout)
		}
	}
	entries, err := os.ReadDir(cwd)
	if err != nil || len(entries) != 0 {
		t.Errorf("files written to the working directory: %v, %v", entries, err)
	}
}

// Hooks-spec, Log: <state> is created mode 0700, with hooks.log in it and
// nothing else, even when its parents are missing.
func TestLogCreatesState(t *testing.T) {
	home := t.TempDir()
	_, state := run(t, home, `{}`, "no-such")
	fi, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("state mode %v, want 0700", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "hooks.log" {
		t.Errorf("state holds %v, %v; want hooks.log alone", entries, err)
	}
	if info, err := os.Stat(filepath.Join(state, "hooks.log")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("hooks.log: %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".config")); len(entries) != 0 {
		t.Errorf("config directory touched: %v", entries)
	}
	// A second line is appended to the same file.
	run(t, home, `{}`, "other")
	if n := strings.Count(readLog(t, state), "\n"); n != 2 {
		t.Errorf("%d lines, want 2", n)
	}
}

// Hooks-spec, Log: a bad hooks.properties is not logged, and the hook runs
// with the default.
func TestBadSettingsNotLogged(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "sesshin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.properties"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, state := run(t, home, payloadFor(session), "statusline")
	if stdout != "🧠 0% | 📁 tmp" {
		t.Errorf("stdout %q", stdout)
	}
	run(t, home, payloadFor(session), "session-end")
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Errorf("a bad hooks.properties logged or created the state directory: %v", err)
	}
}

// A verb's Call carries the decoded payload, the raw stdin, and the settings
// read from hooks.properties.
func TestCall(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "sesshin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.properties"), []byte("hook_lock_wait_ms=500\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := " " + payloadFor(strings.ToUpper(session)) + "\n"
	env := Process{
		Stdin:  strings.NewReader(stdin),
		Stdout: &bytes.Buffer{},
		FS:     fsys.OS{},
		Getenv: func(k string) string {
			if k == "HOME" {
				return home
			}
			return ""
		},
		GOOS: "linux",
		Now:  func() time.Time { return start },
	}
	c, ok := prepare(env, "stop", start, time.Now())
	if !ok {
		t.Fatal("prepare refused a valid hook")
	}
	if c.Verb != "stop" || c.Payload.SessionID != session || c.Payload.Cwd != "/tmp" || string(c.Stdin) != stdin ||
		c.Settings.LockWaitMS != 500 || !c.Now.Equal(start) || c.Loc.StateDir != filepath.Join(home, ".local", "state", "sesshin") {
		t.Errorf("call %+v", c)
	}
}

// Hooks-spec, H4: the lock deadline is twice hook_lock_wait_ms from when the
// hook starts, and session-end's is min(hook_lock_wait_ms, 1s).
func TestLockDeadline(t *testing.T) {
	began := time.Now()
	tests := []struct {
		verb   string
		waitMS int
		want   time.Duration
	}{
		{"stop", 2000, 4 * time.Second},
		{"session-start", 4000, 8 * time.Second},
		{"post-tool-use", 0, 0},
		{"session-end", 2000, time.Second},
		{"session-end", 500, 500 * time.Millisecond},
		{"session-end", 0, 0},
	}
	for _, tc := range tests {
		got := lockDeadline(tc.verb, began, hookconf.Settings{LockWaitMS: tc.waitMS})
		if got.Sub(began) != tc.want {
			t.Errorf("%s at %d ms: %v, want %v", tc.verb, tc.waitMS, got.Sub(began), tc.want)
		}
	}
}

// A verb's Call carries the lock deadline, set when the hook started, and
// hands record what it needs.
func TestCallDeadline(t *testing.T) {
	home := t.TempDir()
	env := Process{
		Stdin:  strings.NewReader(payloadFor(session)),
		Stdout: &bytes.Buffer{},
		FS:     fsys.OS{},
		Getenv: func(k string) string {
			if k == "HOME" {
				return home
			}
			return ""
		},
		GOOS: "linux",
		Now:  func() time.Time { return start },
	}
	began := time.Now()
	c, ok := prepare(env, "session-end", start, began)
	if !ok {
		t.Fatal("prepare refused a valid hook")
	}
	if got := c.Deadline.Sub(began); got != time.Second {
		t.Errorf("session-end deadline %v after start, want 1s", got)
	}
	e := c.RecordEnv()
	if e.SessionID != session || !e.Now.Equal(start) || e.LockWait != 2*time.Second || !e.Deadline.Equal(c.Deadline) ||
		e.Loc != c.Loc || e.Log == nil || e.Getenv == nil {
		t.Errorf("RecordEnv %+v", e)
	}
}

// Every verb the registrations write, and the statusLine verb, is one the
// dispatcher knows: a rename in one place can't leave the other behind.
func TestRegisteredVerbsResolve(t *testing.T) {
	for _, r := range settings.Registrations() {
		if verbFunc(r.Verb) == nil {
			t.Errorf("%s registers verb %q, which has no hook function", r.Event, r.Verb)
		}
	}
	if verbFunc(settings.StatusLineVerb) != nil {
		t.Errorf("statusLine verb %q is a lifecycle verb; it has its own frame", settings.StatusLineVerb)
	}
	if settings.StatusLineVerb != "statusline" {
		t.Errorf("statusLine verb is %q; the dispatcher handles %q", settings.StatusLineVerb, "statusline")
	}
}
