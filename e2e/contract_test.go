package e2e

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/hookconf"
	"github.com/phansen314/sesshin/internal/model"
)

// The hooks contract, H1–H7, on the built binary under every failure the
// specs name (hooks-spec.md, The contract; implementation-spec.md, Contract
// tests). TestHookContract covers payload variety, an unknown verb, and no
// verb; these cover the environment, the files, the locks, and the injected
// panics and fatal errors of the sesshintest build.

// sid2 is a second session, for the tests that count sesshin IDs.
const sid2 = "5c1a2b3d-1111-4222-8333-444455556666"

// healthyLine is the statusline's line for tickPayload in an established
// session: the sesshin ID, the context, the directory.
const (
	tickPayload = `{"session_id":"` + sid + `","cwd":"/tmp"}`
	healthyLine = "#1 | 🧠 0% | 📁 tmp"
)

// recording are the verbs that record an event through record.Record.
var recording = []string{"session-start", "user-prompt", "post-tool-use", "stop", "notification", "compact", "session-end"}

// contractPayload is a payload that makes the verb do its work in an
// established session.
func contractPayload(verb string) string {
	switch verb {
	case "session-start":
		return event("SessionStart", `"source":"resume"`, `"cwd":"/work"`)
	case "user-prompt", "terminal-sync":
		return event("UserPromptSubmit", `"prompt_id":"p1"`)
	case "post-tool-use":
		return event("PostToolUse", `"prompt_id":"p1"`)
	case "stop":
		return event("Stop", `"prompt_id":"p1"`)
	case "notification":
		return event("Notification", `"notification_type":"permission_prompt"`)
	case "compact":
		return event("PreCompact", `"trigger":"auto"`)
	case "cwd-changed":
		return `{"session_id":"` + sid + `","new_cwd":"/elsewhere"}`
	case "session-end":
		return event("SessionEnd", `"reason":"other"`)
	}
	return tickPayload
}

// wantStdout is what verb prints: nothing, but for the statusline, which
// prints line.
func wantStdout(verb string, line string) string {
	if verb == "statusline" {
		return line
	}
	return ""
}

// established makes a session as a running one is: a session-start in kitty
// window 7, whose sesshin.json has id 1 and a placement, and one statusline tick,
// which has made statusline.json. It leaves hooks.log absent.
func established(t *testing.T, h *Harness) {
	t.Helper()
	h.KittyWindow("unix:/x", "7")
	h.Kitten(kittenLS)
	start(t, h, "startup", `"cwd":"/work"`)
	res := h.Hook("statusline", tickPayload)
	if res.Exit != 0 || res.Stdout != healthyLine || res.Stderr != "" {
		t.Fatalf("the established session's tick: exit %d, stdout %q, stderr %q; want %q", res.Exit, res.Stdout, res.Stderr, healthyLine)
	}
	if got := hooksLog(h); got != "" {
		t.Fatalf("hooks.log after setup: %s", got)
	}
}

// hookIn runs the hook with dir as its working directory.
func hookIn(h *Harness, dir, verb, payload string) Result {
	h.t.Helper()
	return h.Run("cd "+shQuote(dir)+" && "+shQuote(h.HookPath)+" "+verb, payload)
}

// snapshot is every file and directory under root, by relative path, with a
// file's content; "<dir>" stands for a directory.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case rel == ".":
		case d.IsDir():
			snap[rel] = "<dir>"
		default:
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snap[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func sameSnapshot(a, b map[string]string) bool { return maps.Equal(a, b) }

// followUp requires that the next lifecycle hook records normally: it exits
// 0 in silence, writes lifecycle.json, and leaves sesshin.json with an ID.
func followUp(t *testing.T, h *Harness) {
	t.Helper()
	path := filepath.Join(sessionDir(h), "lifecycle.json")
	before, _ := os.ReadFile(path)
	res := h.Hook("post-tool-use", event("PostToolUse"))
	quiet(t, res)
	if after, _ := os.ReadFile(path); string(after) == string(before) {
		t.Errorf("the next hook wrote nothing to lifecycle.json")
	}
	if l := readLifecycle(t, h); l.LastEventType != "tool" {
		t.Errorf("the next hook recorded %q, want tool", l.LastEventType)
	}
	if sesshin := readSesshin(t, h); sesshin.ID == nil {
		t.Errorf("sesshin.json has no id after the next hook")
	}
}

// logLines are the lines of hooks.log written by verb for the session.
func logLines(h *Harness, verb string) []string {
	var out []string
	for _, line := range strings.Split(hooksLog(h), "\n") {
		if strings.Contains(line, " "+verb+" "+sid+" ") {
			out = append(out, line)
		}
	}
	return out
}

func logged(h *Harness, verb, what string) bool {
	return slices.ContainsFunc(logLines(h, verb), func(l string) bool { return strings.Contains(l, what) })
}

// H1, H3, H7 with no usable HOME: the hook can't find the state directory,
// so it exits 0 without writing or logging anything (hooks-spec.md,
// Recording an event, step 1). The working directory is a temp directory
// that a relative HOME would resolve in.
func TestContractMissingHome(t *testing.T) {
	t.Parallel()
	homes := []struct {
		name string
		set  func(h *Harness)
	}{
		{"unset", func(h *Harness) { h.Unsetenv("HOME") }},
		{"empty", func(h *Harness) { h.Setenv("HOME", "") }},
		{"dot", func(h *Harness) { h.Setenv("HOME", ".") }},
		{"relative path", func(h *Harness) { h.Setenv("HOME", "rel/home") }},
	}
	for _, home := range homes {
		for _, verb := range verbs {
			t.Run(home.name+"/"+verb, func(t *testing.T) {
				t.Parallel()
				h := New(t)
				home.set(h)
				h.KittyWindow("unix:/x", "7")
				h.Kitten(kittenLS)
				dir := t.TempDir()
				res := hookIn(h, dir, verb, contractPayload(verb))
				if want := wantStdout(verb, fallback); res.Exit != 0 || res.Stdout != want || res.Stderr != "" {
					t.Errorf("exit %d, stdout %q, stderr %q; want 0, %q, and no stderr", res.Exit, res.Stdout, res.Stderr, want)
				}
				if snap := snapshot(t, dir); len(snap) != 0 {
					t.Errorf("wrote %v", snap)
				}
				if snap := snapshot(t, h.Home); len(snap) != 0 {
					t.Errorf("wrote under the unused HOME: %v", snap)
				}
			})
		}
	}
}

// The files a hook reads, and the three ways one is unusable.
var (
	contractFiles = []string{"lifecycle.json", "sesshin.json", "state.json", "statusline.json", "hooks.properties"}
	contractKinds = []string{"garbage", "other format", "directory"}
	schemaRE      = regexp.MustCompile(`"schema":\s*\d+`)
)

func contractPath(h *Harness, file string) string {
	switch file {
	case "state.json":
		return filepath.Join(h.Loc.StateDir, file)
	case "hooks.properties":
		return filepath.Join(h.Loc.ConfigDir, file)
	}
	return filepath.Join(sessionDir(h), file)
}

// spoil makes file unusable in the way kind says.
func spoil(t *testing.T, h *Harness, file, kind string) {
	t.Helper()
	path := contractPath(h, file)
	old, _ := os.ReadFile(path)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var err error
	switch {
	case kind == "directory":
		err = os.Mkdir(path, 0o700)
	case kind == "garbage":
		err = os.WriteFile(path, []byte("{not json \x00\xff"), 0o600)
	case file == "hooks.properties": // JSON is another format here
		err = os.WriteFile(path, []byte(`{"schema": 99}`), 0o600)
	default:
		if !schemaRE.Match(old) {
			t.Fatalf("%s has no schema member: %s", file, old)
		}
		err = os.WriteFile(path, schemaRE.ReplaceAll(old, []byte(`"schema": 99`)), 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// usable reports whether the file's content is one sesshin reads.
func usable(file string, b []byte) bool {
	switch file {
	case "lifecycle.json":
		_, r := model.ReadLifecycle(b, sid)
		return r.Usable
	case "sesshin.json":
		h, r := model.ReadSesshin(b)
		return r.Usable && h.ID != nil
	case "state.json":
		_, r := model.ReadState(b)
		return r.Usable
	case "statusline.json":
		_, r := model.ReadStatusline(b)
		return r.Usable
	}
	_, problems := hookconf.Parse(b)
	return len(problems) == 0
}

// unusableOutcome is what the verb's spec says it does with an unusable
// file: whether it logs it, and whether it replaces it. A file in another
// format (kind "other format") is never replaced, and logged by session-start
// alone, but for hooks.properties, which has no format versions. The state.json cases
// start with no sesshin.json, since only a hook that issues an ID reads
// state.json.
func unusableOutcome(verb, file, kind string) (logs, replaces bool) {
	records := slices.Contains(recording, verb)
	if kind == "other format" && file != "hooks.properties" {
		switch file {
		case "statusline.json":
			return false, false
		case "lifecycle.json":
			return verb == "session-start", false
		}
		return records && verb == "session-start", false
	}
	switch file {
	case "lifecycle.json":
		switch {
		case records && verb != "session-end":
			return true, true // starts a new one, as if missing
		case verb != "terminal-sync":
			return true, false // session-end, cwd-changed, the statusline: left as it is
		}
	case "sesshin.json":
		switch {
		case records:
			return true, true // created afresh, with a new ID
		case verb == "terminal-sync":
			return true, false
		}
	case "state.json":
		return records, records
	case "statusline.json":
		return verb == "statusline", verb == "statusline"
	}
	return false, false // hooks.properties, and a file the verb never reads
}

// For every verb and every file it may find unusable (garbage, another
// format, a directory in its place): exit 0 in silence (the statusline
// prints its line), and what the verb's spec says it does with the file, and
// the next lifecycle hook records normally.
func TestContractUnusableFiles(t *testing.T) {
	t.Parallel()
	for _, file := range contractFiles {
		for _, kind := range contractKinds {
			for _, verb := range verbs {
				t.Run(file+"/"+kind+"/"+verb, func(t *testing.T) {
					t.Parallel()
					h := New(t)
					established(t, h)
					if file == "state.json" {
						if err := os.Remove(contractPath(h, "sesshin.json")); err != nil {
							t.Fatal(err)
						}
					}
					path := contractPath(h, file)
					orig, _ := os.ReadFile(path)
					spoil(t, h, file, kind)
					spoiled, _ := os.ReadFile(path)

					res := h.Hook(verb, contractPayload(verb))
					line := healthyLine
					if file == "sesshin.json" || file == "state.json" {
						line = strings.TrimPrefix(healthyLine, "#1 | ") // the ID segment is hidden
					}
					if want := wantStdout(verb, line); res.Exit != 0 || res.Stdout != want || res.Stderr != "" {
						t.Fatalf("exit %d, stdout %q, stderr %q; want 0, %q, and no stderr", res.Exit, res.Stdout, res.Stderr, want)
					}

					logs, replaces := unusableOutcome(verb, file, kind)
					if got := logged(h, verb, file); got != logs {
						t.Errorf("logged %v, want %v; hooks.log:\n%s", got, logs, hooksLog(h))
					}
					if file == "hooks.properties" && hooksLog(h) != "" {
						t.Errorf("hooks.properties is never logged; hooks.log:\n%s", hooksLog(h))
					}
					after, err := os.ReadFile(path)
					switch {
					case kind == "directory":
						if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
							t.Errorf("the directory in %s's place is gone: %v", file, err)
						}
					case replaces:
						if err != nil || !usable(file, after) {
							t.Errorf("%s not replaced: %q, %v", file, after, err)
						}
					case string(after) != string(spoiled):
						t.Errorf("%s changed: %q, was %q", file, after, spoiled)
					}

					// What a person would do about a directory.
					if kind == "directory" && file != "hooks.properties" {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
					// What a person would do about a file in another
					// format: migrate it, here by putting it back.
					if kind == "other format" && file != "hooks.properties" && file != "statusline.json" {
						if err := os.WriteFile(path, orig, 0o600); err != nil {
							t.Fatal(err)
						}
					}
					followUp(t, h)
				})
			}
		}
	}
}

// holdLock locks dir for the rest of the test, until the returned func is
// called.
func holdLock(t *testing.T, dir string) (release func()) {
	t.Helper()
	root, err := fsys.OS{}.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := root.Lock(0)
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	done := false
	release = func() {
		if !done {
			done = true
			lock.Unlock()
			root.Close()
		}
	}
	t.Cleanup(release)
	return release
}

// withinWait requires a lock wait of 100 ms: given up on after it, and well
// inside the deadline.
func withinWait(t *testing.T, res Result) {
	t.Helper()
	if res.Duration < 90*time.Millisecond || res.Duration > time.Second {
		t.Errorf("took %v, with a 100 ms wait", res.Duration)
	}
}

// H4 with the session lock held: every verb that takes it exits 0 in silence
// within its deadline, logs, and writes nothing. The statusline takes none.
func TestContractSessionLockHeld(t *testing.T) {
	t.Parallel()
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			established(t, h)
			hookConfig(t, h, "hook_lock_wait_ms=100\n")
			before := snapshot(t, sessionDir(h))
			release := holdLock(t, sessionDir(h))
			res := h.Hook(verb, contractPayload(verb))
			after := snapshot(t, sessionDir(h))
			if verb == "statusline" {
				if res.Exit != 0 || res.Stdout != healthyLine || res.Stderr != "" || hooksLog(h) != "" {
					t.Errorf("exit %d, stdout %q, stderr %q, hooks.log %q; want an ordinary tick", res.Exit, res.Stdout, res.Stderr, hooksLog(h))
				}
				if sameSnapshot(before, after) {
					t.Errorf("the statusline wrote nothing")
				}
				return
			}
			quiet(t, res)
			withinWait(t, res)
			if !logged(h, verb, "session lock") {
				t.Errorf("hooks.log lacks the session lock: %q", hooksLog(h))
			}
			if !sameSnapshot(before, after) {
				t.Errorf("wrote under a held lock: %v then %v", before, after)
			}
			release()
			followUp(t, h)
		})
	}
}

// H4 with the state lock held: a hook that must issue an ID gives up on it
// within its deadline. It has recorded the event, written sesshin.json with a
// null id, and logged; the next hook completes it. No other verb takes the
// lock.
func TestContractStateLockHeld(t *testing.T) {
	t.Parallel()
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			established(t, h)
			hookConfig(t, h, "hook_lock_wait_ms=100\n")
			if err := os.Remove(contractPath(h, "sesshin.json")); err != nil {
				t.Fatal(err)
			}
			state := contractPath(h, "state.json")
			stateBefore, _ := os.ReadFile(state)
			lifeBefore, _ := os.ReadFile(contractPath(h, "lifecycle.json"))
			release := holdLock(t, filepath.Join(h.Loc.StateDir, "sessions"))
			res := h.Hook(verb, contractPayload(verb))
			if got, _ := os.ReadFile(state); string(got) != string(stateBefore) {
				t.Errorf("state.json written under a held lock: %s", got)
			}
			if !slices.Contains(recording, verb) {
				wantLine := strings.TrimPrefix(healthyLine, "#1 | ") // no sesshin.json to read
				if res.Exit != 0 || res.Stdout != wantStdout(verb, wantLine) || res.Stderr != "" || strings.Contains(hooksLog(h), "lock") {
					t.Errorf("exit %d, stdout %q, stderr %q, hooks.log %q; want it not to wait", res.Exit, res.Stdout, res.Stderr, hooksLog(h))
				}
				return
			}
			quiet(t, res)
			withinWait(t, res)
			if !logged(h, verb, "state lock") {
				t.Errorf("hooks.log lacks the state lock: %q", hooksLog(h))
			}
			if got, _ := os.ReadFile(contractPath(h, "lifecycle.json")); string(got) == string(lifeBefore) {
				t.Errorf("the event was lost with the state lock")
			}
			if b, err := os.ReadFile(contractPath(h, "sesshin.json")); err != nil {
				t.Errorf("sesshin.json: %v", err)
			} else if sesshin, r := model.ReadSesshin(b); !r.Usable || sesshin.ID != nil {
				t.Errorf("sesshin.json %s, want usable with a null id", b)
			}
			// The placement is kept without an ID: this window's, for
			// session-start, which replaces it, and for the verbs that
			// create the file.
			if got := readSesshinPlacement(t, h); !strings.Contains(got, `"window_id":7`) {
				t.Errorf("placement %s, want kitty's window 7 without an id", got)
			}
			release()
			followUp(t, h)
			if got := readSesshinPlacement(t, h); !strings.Contains(got, `"window_id":7`) {
				t.Errorf("placement %s lost when the id was issued", got)
			}
		})
	}
}

// injected runs verb with SESSHIN_TEST_PANIC=value, on the sesshintest build, and
// clears the variable again.
func injected(h *Harness, value, verb string) Result {
	h.t.Helper()
	h.UseSesshintest()
	h.Setenv("SESSHIN_TEST_PANIC", value)
	defer h.Unsetenv("SESSHIN_TEST_PANIC")
	return h.Hook(verb, contractPayload(verb))
}

// control reruns verb with no injection, to show the injected run left
// something undone that an ordinary one does, and then requires that the
// next hook records normally.
func control(t *testing.T, h *Harness, verb string) {
	t.Helper()
	before := snapshot(t, h.Home)
	res := h.Hook(verb, contractPayload(verb))
	if want := wantStdout(verb, healthyLine); res.Exit != 0 || res.Stdout != want || res.Stderr != "" {
		t.Errorf("control: exit %d, stdout %q, stderr %q; want 0, %q, and no stderr", res.Exit, res.Stdout, res.Stderr, want)
	}
	if sameSnapshot(before, snapshot(t, h.Home)) {
		t.Errorf("control: an ordinary %s changed nothing, so the injected run proved nothing", verb)
	}
	followUp(t, h)
}

// H1, H2, H3: a panic at start (main's recover is in place), at dispatch
// (after stdin is decoded), and at each verb's entry exits 0 in silence and
// writes nothing. The statusline prints its fallback line at each: its own
// frame covers dispatch and its entry, and main's recover covers start.
func TestContractPanicBeforeVerb(t *testing.T) {
	t.Parallel()
	points := []struct {
		point func(verb string) string
		line  func(verb string) string
	}{
		{func(string) string { return "start" }, func(v string) string { return wantStdout(v, fallback) }},
		{func(string) string { return "dispatch" }, func(v string) string { return wantStdout(v, fallback) }},
		{func(v string) string { return "verb:" + v }, func(v string) string { return wantStdout(v, fallback) }},
	}
	for _, p := range points {
		for _, verb := range verbs {
			t.Run(p.point(verb)+"/"+verb, func(t *testing.T) {
				t.Parallel()
				h := New(t)
				res := injected(h, p.point(verb), verb)
				if want := p.line(verb); res.Exit != 0 || res.Stdout != want || res.Stderr != "" {
					t.Errorf("exit %d, stdout %q, stderr %q; want 0, %q, and no stderr", res.Exit, res.Stdout, res.Stderr, want)
				}
				if snap := snapshot(t, h.Home); len(snap) != 0 {
					t.Errorf("wrote %v", snap)
				}
				established(t, h)
				control(t, h, verb)
			})
		}
	}
}

// Injected points where a verb holds the session lock, with the event not yet
// written: record:locked.
func TestContractPanicLocked(t *testing.T) {
	t.Parallel()
	for _, verb := range slices.Concat(recording, []string{"cwd-changed", "terminal-sync"}) {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			established(t, h)
			before := snapshot(t, h.Home)
			res := injected(h, "record:locked", verb)
			quiet(t, res)
			if !sameSnapshot(before, snapshot(t, h.Home)) {
				t.Errorf("wrote under the lock before the panic: %v then %v", before, snapshot(t, h.Home))
			}
			control(t, h, verb) // takes the lock again
		})
	}
}

// withoutSesshin is an established session whose sesshin.json is gone, so that the
// next lifecycle hook creates it with a new ID.
func withoutSesshin(t *testing.T, h *Harness) {
	t.Helper()
	established(t, h)
	if err := os.Remove(contractPath(h, "sesshin.json")); err != nil {
		t.Fatal(err)
	}
}

// record:written: lifecycle.json is written and usable, sesshin.json is not yet,
// and the next hook completes it.
func TestContractPanicWritten(t *testing.T) {
	t.Parallel()
	for _, verb := range recording {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			withoutSesshin(t, h)
			life, state := contractPath(h, "lifecycle.json"), contractPath(h, "state.json")
			lifeBefore, _ := os.ReadFile(life)
			stateBefore, _ := os.ReadFile(state)
			res := injected(h, "record:written", verb)
			quiet(t, res)
			if b, err := os.ReadFile(life); err != nil || string(b) == string(lifeBefore) || !usable("lifecycle.json", b) {
				t.Errorf("lifecycle.json after the panic: %q, %v", b, err)
			}
			if _, err := os.Stat(contractPath(h, "sesshin.json")); !os.IsNotExist(err) {
				t.Errorf("sesshin.json written before the panic: %v", err)
			}
			if b, _ := os.ReadFile(state); string(b) != string(stateBefore) {
				t.Errorf("state.json written before the panic: %s", b)
			}
			control(t, h, verb)
		})
	}
}

// record:state-locked: the state lock is released, state.json is intact, and
// the next hooks issue IDs that are all different.
func TestContractPanicStateLocked(t *testing.T) {
	t.Parallel()
	for _, verb := range recording {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			withoutSesshin(t, h)
			state := contractPath(h, "state.json")
			stateBefore, _ := os.ReadFile(state)
			res := injected(h, "record:state-locked", verb)
			quiet(t, res)
			if b, _ := os.ReadFile(state); string(b) != string(stateBefore) {
				t.Errorf("state.json written before the panic: %s", b)
			}
			if _, err := os.Stat(contractPath(h, "sesshin.json")); !os.IsNotExist(err) {
				t.Errorf("sesshin.json written before the ID: %v", err)
			}
			control(t, h, verb)
			// A second session takes the next ID, not one already issued.
			quiet(t, h.Hook("post-tool-use", strings.ReplaceAll(event("PostToolUse"), sid, sid2)))
			ids := map[int64]bool{}
			for _, s := range []string{sid, sid2} {
				b, err := os.ReadFile(filepath.Join(h.Loc.StateDir, "sessions", s, "sesshin.json"))
				sesshin, r := model.ReadSesshin(b)
				if err != nil || !r.Usable || sesshin.ID == nil {
					t.Fatalf("sesshin.json of %s: %q, %v", s, b, err)
				}
				ids[*sesshin.ID] = true
			}
			b, _ := os.ReadFile(state)
			if st, r := model.ReadState(b); !r.Usable || len(ids) != 2 || !ids[st.LastID] {
				t.Errorf("ids %v, state.json %s: want two, the larger being last_id", ids, b)
			}
		})
	}
}

// statuslineSteps are the names the log gives each step.
var statuslineSteps = []string{"previous statusline.json", "process lookup", "git branch", "burn rate", "render", "write"}

// statusline:1 … statusline:6: the step is skipped and logged, and the line
// still prints. A panic in step 5 prints the fallback line; one in step 6
// writes nothing and leaves no temp file.
func TestContractPanicStatusline(t *testing.T) {
	t.Parallel()
	for n := 1; n <= 6; n++ {
		t.Run("statusline:"+strconv.Itoa(n), func(t *testing.T) {
			t.Parallel()
			h := New(t)
			established(t, h)
			file := contractPath(h, "statusline.json")
			before, _ := os.ReadFile(file)
			res := injected(h, "statusline:"+strconv.Itoa(n), "statusline")
			want := healthyLine
			if n == 5 {
				want = fallback // the sesshin ID is read after the point
			}
			if res.Exit != 0 || res.Stdout != want || res.Stderr != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want 0, %q, and no stderr", res.Exit, res.Stdout, res.Stderr, want)
			}
			step := "statusline step " + strconv.Itoa(n) + " (" + statuslineSteps[n-1] + "): panic: sesshintest: statusline:" + strconv.Itoa(n)
			if !logged(h, "statusline", step) {
				t.Errorf("hooks.log lacks %q:\n%s", step, hooksLog(h))
			}
			after, _ := os.ReadFile(file)
			if n == 6 && string(after) != string(before) {
				t.Errorf("statusline.json written after a panic in step 6")
			}
			if n != 6 && (string(after) == string(before) || !usable("statusline.json", after)) {
				t.Errorf("statusline.json not rewritten: %q", after)
			}
			for name := range snapshot(t, sessionDir(h)) {
				if strings.HasPrefix(name, ".sesshin-tmp-") {
					t.Errorf("temp file left: %s", name)
				}
			}
			followUp(t, h)
		})
	}
}

// sync:returned: terminal-sync, with kitten back and the lock not yet taken,
// exits 0 in silence and writes nothing; the next sync writes.
func TestContractPanicSync(t *testing.T) {
	t.Parallel()
	h := New(t)
	established(t, h)
	before := snapshot(t, h.Home)
	res := injected(h, "sync:returned", "terminal-sync")
	quiet(t, res)
	if !sameSnapshot(before, snapshot(t, h.Home)) {
		t.Errorf("wrote after the panic: %v then %v", before, snapshot(t, h.Home))
	}
	control(t, h, "terminal-sync")
	if got := readSesshinPlacement(t, h); !strings.Contains(got, `"tab_title":"api review"`) {
		t.Errorf("placement %s, want the synced tab", got)
	}
}

// shellAbort is a sh's own report of a child's SIGABRT: dash, which doesn't
// exec its last command, prints it; bash execs, and prints nothing.
var shellAbort = regexp.MustCompile(`^(Aborted( \(core dumped\))?\n)?$`)

// aborted requires the end of a fatal error: SIGABRT or the 134 sh makes of
// it, never 2, and no output but sh's report of the signal.
func aborted(t *testing.T, res Result) {
	t.Helper()
	if !(res.Exit == 134 || res.Signal == syscall.SIGABRT) || res.Stdout != "" || !shellAbort.MatchString(res.Stderr) {
		t.Fatalf("exit %d, signal %v, stdout %q, stderr %q; want SIGABRT (134) and no output", res.Exit, res.Signal, res.Stdout, res.Stderr)
	}
}

// A fatal error no recover can catch, at dispatch and while holding the
// session lock, ends the hook by SIGABRT, never exit 2, with no output
// (The hook binary). The process's death releases the lock: the next hook
// records normally.
func TestContractFatal(t *testing.T) {
	t.Parallel()
	for _, verb := range verbs {
		t.Run("dispatch/"+verb, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			aborted(t, injected(h, "fatal:dispatch", verb))
			if snap := snapshot(t, h.Home); len(snap) != 0 {
				t.Errorf("wrote %v", snap)
			}
			followUp(t, h)
		})
	}
	for _, verb := range slices.Concat(recording, []string{"cwd-changed", "terminal-sync"}) {
		t.Run("record:locked/"+verb, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			established(t, h)
			before := snapshot(t, h.Home)
			aborted(t, injected(h, "fatal:record:locked", verb))
			if !sameSnapshot(before, snapshot(t, h.Home)) {
				t.Errorf("wrote under the lock before the fatal error: %v", snapshot(t, h.Home))
			}
			start := time.Now()
			followUp(t, h)
			if took := time.Since(start); took > time.Second {
				t.Errorf("the next hook took %v: the lock was not released", took)
			}
		})
	}
}

// No environment variable makes a shipped hook fail (Configuration): with
// SESSHIN_TEST_PANIC set, the shipped binary records normally. So does the
// sesshintest build with it unset.
func TestContractShippedIgnoresTestPanic(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"verb:post-tool-use", "dispatch", "start", "fatal:dispatch", "fatal:record:locked"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			h := New(t)
			h.Setenv("SESSHIN_TEST_PANIC", value)
			followUp(t, h)
			res := h.Hook("statusline", tickPayload)
			if res.Exit != 0 || res.Stdout != healthyLine || res.Stderr != "" {
				t.Errorf("statusline: exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
			}
			if got := hooksLog(h); got != "" {
				t.Errorf("hooks.log: %s", got)
			}
		})
	}
	t.Run("sesshintest build, variable unset", func(t *testing.T) {
		h := New(t)
		h.UseSesshintest()
		followUp(t, h)
		if got := hooksLog(h); got != "" {
			t.Errorf("hooks.log: %s", got)
		}
	})
}
