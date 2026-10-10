package iterm2

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/placement"
)

const nonce = "0123456789abcdef0123456789abcdef"

var launchNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// launcher is a Backend over a fake runner, a launch-file store in a temp
// directory with a fixed nonce, and a binary at exe.
func launcher(t *testing.T, r *fakeRunner, exe string) (Backend, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "launches")
	b := newBackend(r)
	b.Launches = Store{Dir: dir, Now: func() time.Time { return launchNow }, Nonce: func() string { return nonce }}
	b.Executable = func() (string, error) { return exe, nil }
	return b, dir
}

func spec(t *testing.T, typ string) placement.LaunchSpec {
	return placement.LaunchSpec{
		Caller: placed(t, uuidLower), Type: typ, Cwd: "/work dir", Title: "api",
		Vars: []placement.Var{{Name: "project", Value: "a b"}, {Name: "x", Value: ""}},
		Env:  []placement.Var{{Name: "SESSHIN_JOB", Value: "api"}, {Name: "SESSHIN_TOKEN", Value: "tok"}},
		Argv: []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--", "it's $(x)"},
	}
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func TestLaunch(t *testing.T) {
	f := &fakeRunner{reply: reply(strings.ToLower(otherUUID) + "\n")}
	b, dir := launcher(t, f, "/Users/me/my bin/sesshin")
	got, err := b.Launch(spec(t, "split"))
	if err != nil || enc(t, got) != enc(t, placed(t, otherUUID)) {
		t.Fatalf("got %s, %v", enc(t, got), err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls %+v", f.calls)
	}
	c := f.calls[0]
	want := []string{uuidUpper, "split", "'/Users/me/my bin/sesshin' launch-exec " + nonce, "api", "project", "a b", "x", ""}
	if c.script != launchScript || c.limit != 10*time.Second || strings.Join(c.args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("call %+v, want args %q", c, want)
	}

	// Only the nonce is in the command; the arguments wait in the file.
	if strings.Contains(strings.Join(c.args, " "), "$(x)") {
		t.Error("the arguments reached the script")
	}
	if names := files(t, dir); len(names) != 1 || names[0] != nonce+".json" {
		t.Fatalf("files %v", names)
	}
	st, _ := os.Stat(filepath.Join(dir, nonce+".json"))
	dst, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 {
		t.Errorf("modes %o, %o", st.Mode().Perm(), dst.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(dir, nonce+".json"))
	var lf LaunchFile
	if err := json.Unmarshal(data, &lf); err != nil {
		t.Fatal(err)
	}
	if lf.CreatedAt != "2026-10-09T12:00:00Z" || lf.Cwd != "/work dir" || lf.Env["SESSHIN_TOKEN"] != "tok" || lf.Env["SESSHIN_JOB"] != "api" ||
		len(lf.Argv) != 9 || lf.Argv[8] != "it's $(x)" {
		t.Errorf("launch file %s", data)
	}
	if _, ok := parseLaunch(data); !ok {
		t.Errorf("launch file does not read back: %s", data)
	}
}

func TestLaunchTypes(t *testing.T) {
	for _, typ := range []string{"tab", "split", "os-window"} {
		f := &fakeRunner{reply: reply(uuidUpper)}
		b, _ := launcher(t, f, "/bin/sesshin")
		s := spec(t, typ)
		if typ == "split" {
			s.Title = ""
		}
		if _, err := b.Launch(s); err != nil || f.calls[0].args[1] != typ || (typ == "split") != (f.calls[0].args[3] == "") {
			t.Errorf("%s: %v %q", typ, err, f.calls[0].args)
		}
	}
}

func TestLaunchFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		r       func(call) ([]byte, error)
		unknown bool
		kept    bool // the launch file is left for prune
		want    string
	}{
		"not running":  {reply("not-running\n"), false, false, "not running"},
		"no caller":    {reply("not-found\n"), false, false, "no session"},
		"script fails": {failing(errors.New("exit status 1: execution error: Can't get window (-1728)")), false, false, "-1728"},
		"permission":   {failing(permission), false, false, "System Settings → Privacy & Security → Automation"},
		"timeout":      {failing(timeout), true, true, "limit passed"},
		"garbage":      {reply("hello\n"), true, true, "not a session"},
		"two uuids":    {reply(uuidUpper + "\n" + otherUUID + "\n"), true, true, "not a session"},
		"empty":        {reply(""), true, true, "not a session"},
	} {
		t.Run(name, func(t *testing.T) {
			b, dir := launcher(t, &fakeRunner{reply: tc.r}, "/bin/sesshin")
			got, err := b.Launch(spec(t, "tab"))
			var le *placement.LaunchError
			if got != nil || !errors.As(err, &le) || le.Unknown != tc.unknown || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, %v; want unknown %v with %q", got, err, tc.unknown, tc.want)
			}
			if kept := len(files(t, dir)) == 1; kept != tc.kept {
				t.Errorf("launch file kept %v: %v", kept, files(t, dir))
			}
		})
	}
}

func TestLaunchRefusals(t *testing.T) {
	t.Run("caller not iTerm2", func(t *testing.T) {
		f := &fakeRunner{reply: reply(uuidUpper)}
		b, dir := launcher(t, f, "/bin/sesshin")
		s := spec(t, "tab")
		s.Caller = nil
		if _, err := b.Launch(s); err == nil || placement.IsUnknown(err) || len(f.calls) != 0 || len(files(t, dir)) != 0 {
			t.Errorf("%v, %d calls", err, len(f.calls))
		}
	})
	for _, exe := range []string{"/it's/sesshin", `/back\slash/sesshin`, "/new\nline/sesshin", "/tab\t/sesshin", "/del\x7f/sesshin"} {
		t.Run(exe, func(t *testing.T) {
			f := &fakeRunner{reply: reply(uuidUpper)}
			b, dir := launcher(t, f, exe)
			if _, err := b.Launch(spec(t, "tab")); err == nil || placement.IsUnknown(err) || len(f.calls) != 0 {
				t.Errorf("%v, %d calls", err, len(f.calls))
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("something was written: %v", err)
			}
		})
	}
	t.Run("no binary", func(t *testing.T) {
		b, _ := launcher(t, &fakeRunner{reply: reply(uuidUpper)}, "")
		b.Executable = func() (string, error) { return "", errors.New("no") }
		if _, err := b.Launch(spec(t, "tab")); err == nil || placement.IsUnknown(err) {
			t.Errorf("%v", err)
		}
	})
}

func TestQuotable(t *testing.T) {
	for s, want := range map[string]bool{"/a b/c": true, "/é/x": true, "/a'b": false, `/a\b`: false, "/a\x01b": false, "": true} {
		if quotable(s) != want {
			t.Errorf("quotable(%q) = %v", s, !want)
		}
	}
}
