package ops

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/loc"
)

// selfTestRig is a SelfTester whose children are a function: it plays the
// hook, writing files under the HOME it was given.
type selfTestRig struct {
	t       *testing.T
	st      *SelfTester
	hook    string
	sesshin buildinfo.Info
	removed int
	calls   []string
	// child plays a verb; the default writes what a working sesshin-hook does.
	child func(r *selfTestRig, verb string, stdin []byte, env []string) ChildResult
}

const (
	lifecycleFixture  = "../model/testdata/lifecycle.json"
	statuslineFixture = "../model/testdata/statusline.json"
)

func newRig(t *testing.T) *selfTestRig {
	t.Helper()
	dir := t.TempDir()
	hook := filepath.Join(dir, "sesshin-hook")
	if err := os.WriteFile(hook, []byte("not run"), 0o700); err != nil {
		t.Fatal(err)
	}
	sesshin := buildinfo.Info{Version: fixtureBuild, Commit: "0a2ed27a1b2c", Go: "go1.26.8"}
	r := &selfTestRig{t: t, hook: hook, sesshin: sesshin, child: workingHook}
	r.st = &SelfTester{
		FS:   fsys.OS{},
		GOOS: "linux",
		Environ: func() []string {
			return []string{"PATH=/bin", "HOME=/real/home", "XDG_CONFIG_HOME=/x", "XDG_STATE_HOME=/y", "CLAUDE_CONFIG_DIR=/z",
				"CLAUDE_PID=1", "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "KITTY_LISTEN_ON=unix:/k", "KITTY_WINDOW_ID=3",
				"TMUX=/t", "STY=s", "LANG=C", "KITTY_PID=9"}
		},
		ReadBuild: func(string) (buildinfo.Info, error) { return sesshin, nil },
		TempDir: func() (string, func(), error) {
			d, err := os.MkdirTemp(t.TempDir(), "home-")
			return d, func() { r.removed++; os.RemoveAll(d) }, err
		},
		Child: func(hook, verb string, stdin []byte, env []string, limit time.Duration) ChildResult {
			if hook != r.hook || limit != 10*time.Second {
				t.Errorf("child %s %s, limit %v", hook, verb, limit)
			}
			r.calls = append(r.calls, verb)
			return r.child(r, verb, stdin, env)
		},
	}
	return r
}

func envValue(env []string, key string) (string, bool) {
	for _, kv := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// stateOf is the state directory a child with env would use.
func stateOf(t *testing.T, env []string) loc.Locations {
	t.Helper()
	l, err := loc.Resolve("linux", func(k string) string { v, _ := envValue(env, k); return v })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func fixtureBytes(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// workingHook writes what sesshin-hook does for each verb.
func workingHook(r *selfTestRig, verb string, stdin []byte, env []string) ChildResult {
	t := r.t
	dir := stateOf(t, env).SessionDir(selfTestSession)
	write := func(name, content string) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	switch verb {
	case "session-start":
		write("lifecycle.json", strings.ReplaceAll(fixtureBytes(t, lifecycleFixture), "3fa85f64-5717-4562-b3fc-2c963f66afa6", selfTestSession))
		write("sesshin.json", `{"schema": 1, "id": 1, "job": null, "source": "hook", "placement": null}`)
	case "statusline":
		write("statusline.json", fixtureBytes(t, statuslineFixture))
		return ChildResult{Stdout: []byte("#1 | 🧠 0%")}
	}
	return ChildResult{}
}

func (r *selfTestRig) run() *Error { return r.st.Test(r.hook, r.sesshin) }

func TestSelfTestPasses(t *testing.T) {
	r := newRig(t)
	var seen []string
	r.child = func(r *selfTestRig, verb string, stdin []byte, env []string) ChildResult {
		home, _ := envValue(env, "HOME")
		if home == "/real/home" || !strings.Contains(home, "home-") {
			t.Errorf("%s: HOME=%s", verb, home)
		}
		for _, name := range removedFromChild {
			if v, ok := envValue(env, name); ok {
				t.Errorf("%s: %s=%s", verb, name, v)
			}
		}
		for _, kept := range []string{"PATH", "LANG", "KITTY_PID"} {
			if _, ok := envValue(env, kept); !ok {
				t.Errorf("%s: %s dropped", verb, kept)
			}
		}
		// The payload: a JSON object with a fixed UUID and the temp dir.
		obj, _, err := jsonio.ParseObject(stdin)
		if err != nil {
			t.Fatalf("%s: %v: %s", verb, err, stdin)
		}
		if sid, _ := obj.Get("session_id"); sid != selfTestSession {
			t.Errorf("%s: session_id %v", verb, sid)
		}
		if cwd, _ := obj.Get("cwd"); cwd != home {
			t.Errorf("%s: cwd %v, HOME %s", verb, cwd, home)
		}
		if verb == "statusline" {
			for _, k := range []string{"model", "workspace", "cost", "context_window"} {
				if _, ok := obj.Get(k); !ok {
					t.Errorf("statusline payload lacks %s: %s", k, stdin)
				}
			}
		}
		seen = append(seen, string(stdin))
		return workingHook(r, verb, stdin, env)
	}
	if e := r.run(); e != nil {
		t.Fatalf("%+v", e)
	}
	if !slices.Equal(r.calls, []string{"session-start", "stop", "statusline"}) {
		t.Errorf("verbs %v", r.calls)
	}
	if r.removed != 1 {
		t.Errorf("temporary directory removed %d times", r.removed)
	}
}

func TestSelfTestFailures(t *testing.T) {
	exit := func(n int, stderr string) ChildResult { return ChildResult{Exit: n, Stderr: []byte(stderr)} }
	over := func(verb string, res ChildResult) func(*selfTestRig, string, []byte, []string) ChildResult {
		return func(r *selfTestRig, v string, stdin []byte, env []string) ChildResult {
			if v == verb {
				return res
			}
			return workingHook(r, v, stdin, env)
		}
	}
	// printing is a statusline that records its payload but prints out.
	printing := func(out string) func(*selfTestRig, string, []byte, []string) ChildResult {
		return func(r *selfTestRig, v string, stdin []byte, env []string) ChildResult {
			res := workingHook(r, v, stdin, env)
			if v == "statusline" {
				res.Stdout = []byte(out)
			}
			return res
		}
	}
	// without runs the working hook but leaves one file out, or breaks it.
	without := func(file, content string) func(*selfTestRig, string, []byte, []string) ChildResult {
		return func(r *selfTestRig, v string, stdin []byte, env []string) ChildResult {
			res := workingHook(r, v, stdin, env)
			p := filepath.Join(stateOf(r.t, env).SessionDir(selfTestSession), file)
			if content == "" {
				os.Remove(p)
			} else if _, err := os.Stat(p); err == nil {
				os.WriteFile(p, []byte(content), 0o600)
			}
			return res
		}
	}
	for _, tc := range []struct {
		name   string
		child  func(*selfTestRig, string, []byte, []string) ChildResult
		hook   string // the verb in the error
		detail string
		calls  int // verbs run
	}{
		{"exit status", over("stop", exit(3, "boom\n")), "stop", "exited with status 3 boom", 2},
		{"stderr", over("session-start", exit(0, "oops")), "session-start", "wrote to stderr: oops", 1},
		{"timeout", over("statusline", ChildResult{TimedOut: true}), "statusline", "did not exit within 10s, and was killed", 3},
		{"signal", over("stop", ChildResult{Exit: -1}), "stop", "was ended by a signal", 2},
		{"start", over("session-start", ChildResult{Err: errors.New("fork/exec: permission denied")}), "session-start", "could not be started: fork/exec: permission denied", 1},
		{"no lifecycle", without("lifecycle.json", ""), "session-start", "lifecycle.json was not written", 3},
		{"bad lifecycle", without("lifecycle.json", "{}"), "session-start", "lifecycle.json is not valid:", 3},
		{"no sesshin", without("sesshin.json", ""), "session-start", "sesshin.json was not written", 3},
		{"sesshin id 2", without("sesshin.json", `{"schema": 1, "id": 2, "job": null, "source": "hook", "placement": null}`), "session-start", "sesshin.json its id is not 1", 3},
		{"sesshin null id", without("sesshin.json", `{"schema": 1, "id": null, "job": null, "source": "hook", "placement": null}`), "session-start", "sesshin.json its id is not 1", 3},
		{"no statusline.json", without("statusline.json", ""), "statusline", "statusline.json was not written", 3},
		{"bad statusline.json", without("statusline.json", `{"schema": 1}`), "statusline", "statusline.json is not valid:", 3},
		{"no line", printing(""), "statusline", "it printed no line", 3},
		{"newline", printing("x\n"), "statusline", "its line ends in a newline", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.child = tc.child
			e := r.run()
			if e == nil || e.Kind != KindSelfTest {
				t.Fatalf("%+v", e)
			}
			checkEnvelope(t, Failed(e), "")
			if e.Details["hook"] != tc.hook || e.Details["path"] != r.hook || !strings.HasPrefix(fmt.Sprint(e.Details["detail"]), tc.detail) {
				t.Errorf("%+v, want %s: %s", e.Details, tc.hook, tc.detail)
			}
			if len(r.calls) != tc.calls || r.removed != 1 {
				t.Errorf("verbs %v, removed %d", r.calls, r.removed)
			}
		})
	}
}

// sesshin-hook missing, not identifiable, or from another build fails before any
// child runs, with hook null.
func TestSelfTestHookFile(t *testing.T) {
	devel := buildinfo.Info{Version: buildinfo.Devel}
	for _, tc := range []struct {
		name   string
		setup  func(r *selfTestRig)
		detail string
	}{
		{"missing", func(r *selfTestRig) { os.Remove(r.hook) }, "sesshin-hook is not beside sesshin"},
		{"directory", func(r *selfTestRig) { os.Remove(r.hook); os.Mkdir(r.hook, 0o700) }, "sesshin-hook is not a regular file"},
		{"not a go binary", func(r *selfTestRig) {
			r.st.ReadBuild = func(string) (buildinfo.Info, error) { return buildinfo.Info{}, errors.New("unrecognized file format") }
		}, "sesshin-hook cannot be identified: unrecognized file format"},
		{"unidentifiable", func(r *selfTestRig) {
			r.st.ReadBuild = func(string) (buildinfo.Info, error) { return devel, nil }
		}, "sesshin-hook cannot be identified: it has no commit and its version is (devel)"},
		{"other commit", func(r *selfTestRig) {
			r.st.ReadBuild = func(string) (buildinfo.Info, error) {
				return buildinfo.Info{Version: fixtureBuild, Commit: "ffffffffffff"}, nil
			}
		}, "sesshin-hook is from another build: " + fixtureBuild + " (commit ffffffffffff), sesshin is " + fixtureBuild + " (commit 0a2ed27a1b2c)"},
		{"other modified", func(r *selfTestRig) {
			r.st.ReadBuild = func(string) (buildinfo.Info, error) {
				return buildinfo.Info{Version: fixtureBuild, Commit: "0a2ed27a1b2c", Modified: true}, nil
			}
		}, "sesshin-hook is from another build: " + fixtureBuild + " (commit 0a2ed27a1b2c, modified), sesshin is "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.setup(r)
			e := r.run()
			if e == nil || e.Kind != KindSelfTest || e.Details["hook"] != nil || e.Details["path"] != r.hook ||
				!strings.HasPrefix(fmt.Sprint(e.Details["detail"]), tc.detail) {
				t.Fatalf("%+v", e)
			}
			checkEnvelope(t, Failed(e), "")
			if len(r.calls) != 0 || r.removed != 0 {
				t.Errorf("verbs %v, removed %d", r.calls, r.removed)
			}
		})
	}

	// A build that is only a module version is identifiable; a hook that
	// can't be read is io.
	r := newRig(t)
	r.sesshin = buildinfo.Info{Version: "v1.2.3"}
	r.st.ReadBuild = func(string) (buildinfo.Info, error) { return r.sesshin, nil }
	if e := r.run(); e != nil {
		t.Errorf("%+v", e)
	}
	r.st.ReadBuild = func(p string) (buildinfo.Info, error) {
		return buildinfo.Info{}, &os.PathError{Op: "open", Path: p, Err: syscall.EACCES}
	}
	if e := r.run(); e == nil || e.Kind != KindIO || e.Details["code"] != "EACCES" {
		t.Errorf("%+v", e)
	}
}

// The real child runner: exit status, stderr, stdin, a limit that kills.
func TestOSChild(t *testing.T) {
	dir := t.TempDir()
	script := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := exec.Command(script("probe", "exit 0")).Run(); err != nil {
		t.Skipf("cannot run scripts here: %v", err)
	}
	res := osChild(script("ok", `cat; echo "$1:$HOME" >&2; exit 4`), "verb", []byte("in"), []string{"HOME=/h"}, 5*time.Second)
	if res.Err != nil || res.TimedOut || res.Exit != 4 || string(res.Stdout) != "in" || string(res.Stderr) != "verb:/h\n" {
		t.Errorf("%+v", res)
	}
	// A child that holds the pipes past the limit is killed, and the limit holds.
	start := time.Now()
	res = osChild(script("hang", `sleep 3 & sleep 3`), "verb", nil, nil, 200*time.Millisecond)
	if !res.TimedOut || time.Since(start) > 5*time.Second {
		t.Errorf("%+v after %v", res, time.Since(start))
	}
	res = osChild(filepath.Join(dir, "missing"), "verb", nil, nil, time.Second)
	if res.Err == nil {
		t.Errorf("%+v", res)
	}
	if r := osChild(script("sig", `kill -9 $$`), "verb", nil, nil, time.Second); r.Exit != -1 {
		t.Errorf("%+v", r)
	}
}
