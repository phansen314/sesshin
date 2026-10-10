package iterm2

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/placement"
)

// call is one run of a script.
type call struct {
	script string
	args   []string
	limit  time.Duration
	file   string // the content of the file named by args[1], for the paste
}

// fakeRunner answers each run by calling reply, and records it.
type fakeRunner struct {
	calls []call
	reply func(c call) ([]byte, error)
}

func (f *fakeRunner) Run(script string, args []string, limit time.Duration) ([]byte, error) {
	c := call{script: script, args: args, limit: limit}
	if script == pasteScript {
		b, err := os.ReadFile(args[1])
		if err != nil {
			return nil, err
		}
		c.file = string(b)
	}
	f.calls = append(f.calls, c)
	return f.reply(c)
}

func reply(out string) func(call) ([]byte, error) {
	return func(call) ([]byte, error) { return []byte(out), nil }
}

func failing(err error) func(call) ([]byte, error) {
	return func(call) ([]byte, error) { return nil, err }
}

// timeout is a Runner error of the limit passing.
var timeout = &Error{Timeout: true, Err: errors.New("osascript: limit passed")}

// permission is osascript's own message for a refused permission.
var permission = errors.New("exit status 1: 5:30: execution error: Not authorized to send Apple events to iTerm2. (-1743)")

const otherUUID = "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"

func newBackend(r *fakeRunner) Backend { return Backend{GOOS: "darwin", Runner: r} }

func TestScriptsFollowTheRules(t *testing.T) {
	for name, s := range map[string]string{"list": listScript, "tty": ttyScript, "launch": launchScript, "paste": pasteScript, "enter": enterScript, "focus": focusScript} {
		t.Run(name, func(t *testing.T) {
			lines := strings.Split(s, "\n")
			first := lines[0]
			if first == "on run argv" {
				first = lines[1]
			}
			if !strings.HasPrefix(first, `if application id "com.googlecode.iterm2" is not running then return`) {
				t.Errorf("does not begin by returning when iTerm2 isn't running: %q", first)
			}
			if strings.Contains(s, `application "iTerm2"`) || strings.Contains(s, "delay") {
				t.Error("names iTerm2 by name or delays")
			}
			for _, w := range []string{"& tab", "tab &", " kind", "before "} {
				if strings.Contains(s, w) {
					t.Errorf("uses %q", w)
				}
			}
		})
	}
}

func TestExist(t *testing.T) {
	p, q := placed(t, uuidUpper), placed(t, otherUUID)
	lower := placed(t, uuidLower)
	for name, tc := range map[string]struct {
		r    func(call) ([]byte, error)
		want []placement.Existence
	}{
		"one present":     {reply(uuidUpper + "\n"), []placement.Existence{placement.Present, placement.Gone, placement.Present}},
		"none listed":     {reply(""), []placement.Existence{placement.Gone, placement.Gone, placement.Gone}},
		"lowercase reply": {reply(strings.ToLower(uuidUpper) + "\n"), []placement.Existence{placement.Present, placement.Gone, placement.Present}},
		"not running":     {reply("not-running\n"), []placement.Existence{0, 0, 0}},
		"garbage":         {reply("what\n" + uuidUpper + "\n"), []placement.Existence{0, 0, 0}},
		"timeout":         {failing(timeout), []placement.Existence{0, 0, 0}},
		"refused":         {failing(permission), []placement.Existence{0, 0, 0}},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeRunner{reply: tc.r}
			got := newBackend(f).Exist([]*jsonio.Object{p, q, lower})
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("answers %v, want %v", got, tc.want)
					break
				}
			}
			if len(f.calls) != 1 || f.calls[0].script != listScript || f.calls[0].limit != time.Second || len(f.calls[0].args) != 0 {
				t.Errorf("calls %+v", f.calls)
			}
		})
	}
	t.Run("nothing valid asks nothing", func(t *testing.T) {
		f := &fakeRunner{reply: reply("")}
		got := newBackend(f).Exist([]*jsonio.Object{nil, placed(t, "x")})
		if len(f.calls) != 0 || got[0] != placement.Unknown || got[1] != placement.Unknown {
			t.Errorf("%v, %v", got, f.calls)
		}
	})
}

func TestLocate(t *testing.T) {
	const tty1, tty2 = "/dev/ttys001", "/dev/ttys002"
	rdevs := map[string]uint64{tty1: 100, tty2: 200}
	listing := uuidUpper + "\t" + tty1 + "\n" + otherUUID + "\t" + tty2 + "\n"
	locate := func(r func(call) ([]byte, error), dev uint64, devErr error) (*jsonio.Object, error, *fakeRunner) {
		f := &fakeRunner{reply: r}
		b := newBackend(f)
		b.ControllingTTY = func(pid int64) (uint64, error) {
			if pid != 4242 {
				t.Errorf("pid %d", pid)
			}
			return dev, devErr
		}
		b.Rdev = func(path string) (uint64, error) {
			if d, ok := rdevs[path]; ok {
				return d, nil
			}
			return 0, errors.New("no such tty")
		}
		got, err := b.Locate(placed(t, "11111111-2222-4333-8444-555555555555"), 4242, env(nil))
		return got, err, f
	}
	t.Run("second session", func(t *testing.T) {
		got, err, f := locate(reply(strings.ToLower(listing)), 200, nil)
		if err != nil || enc(t, got) != enc(t, placed(t, otherUUID)) {
			t.Errorf("got %s, %v", enc(t, got), err)
		}
		if len(f.calls) != 1 || f.calls[0].script != ttyScript || f.calls[0].limit != 5*time.Second {
			t.Errorf("calls %+v", f.calls)
		}
	})
	for name, tc := range map[string]struct {
		r    func(call) ([]byte, error)
		dev  uint64
		derr error
		want string
	}{
		"no tty matches":    {reply(listing), 300, nil, "no iTerm2 session"},
		"no controlling":    {reply(listing), 0, errors.New("no controlling terminal"), "controlling terminal of pid 4242 is unknown"},
		"not running":       {reply("not-running\n"), 100, nil, "not running"},
		"garbage":           {reply("nonsense\n"), 100, nil, "not a session"},
		"missing tty":       {reply(uuidUpper + "\n"), 100, nil, "not a session"},
		"timeout":           {failing(timeout), 100, nil, "limit passed"},
		"permission":        {failing(permission), 100, nil, "System Settings → Privacy & Security → Automation"},
		"unstatable tty":    {reply(uuidUpper + "\t/dev/gone\n"), 100, nil, "no iTerm2 session"},
		"empty listing":     {reply(""), 100, nil, "no iTerm2 session"},
		"stored plays none": {reply(listing), 999, nil, "no iTerm2 session"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err, _ := locate(tc.r, tc.dev, tc.derr)
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, %v; want an error containing %q", got, err, tc.want)
			}
		})
	}
	t.Run("timeout is typed", func(t *testing.T) {
		_, err, _ := locate(failing(timeout), 100, nil)
		if !placement.IsTimeout(err) {
			t.Errorf("%v is not a timeout", err)
		}
	})
}

func TestSend(t *testing.T) {
	p := placed(t, uuidLower)
	t.Run("paste then enter", func(t *testing.T) {
		f := &fakeRunner{reply: reply("ok\n")}
		if err := newBackend(f).Send(p, "hello\nworld é", true); err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != 2 {
			t.Fatalf("calls %+v", f.calls)
		}
		c := f.calls[0]
		if c.script != pasteScript || c.limit != 5*time.Second || c.args[0] != uuidUpper || c.file != "\x1b[200~hello\nworld é\x1b[201~" {
			t.Errorf("paste %+v", c)
		}
		if e := f.calls[1]; e.script != enterScript || e.limit != 5*time.Second || len(e.args) != 1 || e.args[0] != uuidUpper {
			t.Errorf("enter %+v", e)
		}
		if _, err := os.Stat(c.args[1]); !os.IsNotExist(err) {
			t.Errorf("the paste file is still there: %v", err)
		}
	})
	t.Run("file is private and in the given directory", func(t *testing.T) {
		dir := t.TempDir()
		var mode os.FileMode
		f := &fakeRunner{}
		f.reply = func(c call) ([]byte, error) {
			st, err := os.Stat(c.args[1])
			if err != nil {
				return nil, err
			}
			mode = st.Mode().Perm()
			if filepath.Dir(c.args[1]) != dir {
				t.Errorf("file in %s", filepath.Dir(c.args[1]))
			}
			return []byte("ok"), nil
		}
		b := newBackend(f)
		b.TempDir = dir
		if err := b.Send(p, "x", false); err != nil {
			t.Fatal(err)
		}
		if mode != 0o600 || len(f.calls) != 1 {
			t.Errorf("mode %o, calls %d", mode, len(f.calls))
		}
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Errorf("left %v", left)
		}
	})
	for name, tc := range map[string]struct {
		r      func(call) ([]byte, error)
		submit bool // the error is the Enter's
		calls  int
		want   string
	}{
		"not running": {reply("not-running"), false, 1, "not running"},
		"not found":   {reply("not-found"), false, 1, "no such session"},
		"garbage":     {reply("???"), false, 1, "not ok"},
		"timeout":     {failing(timeout), false, 1, "limit passed"},
		"permission":  {failing(permission), false, 1, "System Settings → Privacy & Security → Automation"},
		"enter fails": {func(c call) ([]byte, error) {
			if c.script == enterScript {
				return nil, timeout
			}
			return []byte("ok"), nil
		}, true, 2, "limit passed"},
		"enter not seen": {func(c call) ([]byte, error) {
			return []byte(map[bool]string{true: "not-found", false: "ok"}[c.script == enterScript]), nil
		}, true, 2, "no such session"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeRunner{reply: tc.r}
			err := newBackend(f).Send(p, "x", true)
			if err == nil || !strings.Contains(err.Error(), tc.want) || placement.IsSubmit(err) != tc.submit || len(f.calls) != tc.calls {
				t.Errorf("err %v (submit %v), %d calls", err, placement.IsSubmit(err), len(f.calls))
			}
			var se *placement.SendError
			if !errors.As(err, &se) {
				t.Errorf("%T is not a *SendError", err)
			}
		})
	}
	if err := newBackend(&fakeRunner{reply: reply("ok")}).Send(nil, "x", true); err == nil {
		t.Error("sent to no placement")
	}
}

func TestFocus(t *testing.T) {
	p := placed(t, uuidLower)
	f := &fakeRunner{reply: reply("ok\n")}
	if err := newBackend(f).Focus(p); err != nil {
		t.Fatal(err)
	}
	if c := f.calls[0]; len(f.calls) != 1 || c.script != focusScript || c.limit != 5*time.Second || len(c.args) != 1 || c.args[0] != uuidUpper {
		t.Errorf("calls %+v", f.calls)
	}
	for name, tc := range map[string]struct {
		r    func(call) ([]byte, error)
		want string
	}{
		"not running": {reply("not-running"), "not running"},
		"not found":   {reply("not-found"), "no such session"},
		"garbage":     {reply("x"), "not ok"},
		"timeout":     {failing(timeout), "limit passed"},
		"permission":  {failing(permission), "Automation"},
	} {
		t.Run(name, func(t *testing.T) {
			err := newBackend(&fakeRunner{reply: tc.r}).Focus(p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
		})
	}
	if newBackend(f).Focus(nil) == nil {
		t.Error("focused no placement")
	}
}
