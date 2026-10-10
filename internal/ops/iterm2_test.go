package ops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/placement/iterm2"
)

// The ops over the iTerm2 backend itself, with a fake osascript: what the
// kitty-shaped fixtures can't show, namely the placement's UUID in either
// case, and the reasons the backend's errors map to.

const (
	itermCaller = "2E30574E-F9EE-4D62-BE94-56A54E66E0D5"
	itermNew    = "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"
)

// osa is a fake osascript: it records each script run and answers with reply.
type osa struct {
	scripts []string
	args    [][]string
	reply   func(script string, args []string) ([]byte, error)
}

func (o *osa) Run(script string, args []string, _ time.Duration) ([]byte, error) {
	o.scripts = append(o.scripts, script)
	o.args = append(o.args, args)
	return o.reply(script, args)
}

type osaTimeout struct{}

func (osaTimeout) Error() string  { return "osascript: limit passed" }
func (osaTimeout) TimedOut() bool { return true }

// itermSpawn is the spawn fixture run in an iTerm2 session.
func itermSpawn(t *testing.T, o *osa) *spawnFixture {
	t.Helper()
	f := newSpawnFixture(t)
	f.vars = map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t0p0:" + strings.ToLower(itermCaller), "SHELL": "/bin/zsh"}
	f.only = iterm2.Backend{
		GOOS:           "darwin",
		Runner:         o,
		Launches:       iterm2.Store{Dir: f.loc.LaunchesDir()},
		Executable:     func() (string, error) { return "/usr/local/bin/sesshin", nil },
		ControllingTTY: func(pid int64) (uint64, error) { return uint64(pid), nil },
		Rdev: func(tty string) (uint64, error) {
			if tty == "/dev/ttys011" {
				return 11, nil
			}
			return 99, nil
		},
	}
	return f
}

func (f *spawnFixture) launchFiles() []string {
	ents, _ := os.ReadDir(f.loc.LaunchesDir())
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// Without a job, spawn finds its session by the launched pane, whatever case
// the stored UUID has.
func TestItermSpawnWithoutJobMatchesAcrossCase(t *testing.T) {
	for name, stored := range map[string]string{"capitals": itermNew, "lowercase": strings.ToLower(itermNew)} {
		t.Run(name, func(t *testing.T) {
			o := &osa{reply: func(string, []string) ([]byte, error) { return []byte(strings.ToLower(itermNew) + "\n"), nil }}
			f := itermSpawn(t, o)
			f.running(uuidA, time.Minute, 11)
			f.sesshin(uuidA, 1, `{"terminal":"iterm2","session_id":"`+itermCaller+`"}`) // the caller's
			f.onSleep = func(n int) {
				if n == 2 {
					f.running(uuidB, 0, 21)
					f.sesshin(uuidB, 2, `{"terminal":"iterm2","session_id":"`+stored+`"}`)
				}
			}
			out, warnings := f.spawned(`"start_timeout_secs":5`)
			if len(warnings) != 0 || out.Session == nil || out.Session.SessionID != uuidB {
				t.Fatalf("%+v, warnings %+v", out, warnings)
			}
			if got := enc(t, out.Placement); got != `{"terminal":"iterm2","session_id":"`+itermNew+`"}` {
				t.Errorf("placement %s", got)
			}
			if len(o.args) != 1 || o.args[0][0] != itermCaller {
				t.Errorf("the script was asked about %v", o.args)
			}
		})
	}
}

func TestItermSpawnRefusedReleasesReservation(t *testing.T) {
	for name, reply := range map[string]string{"not running": "not-running\n", "caller gone": "not-found\n"} {
		t.Run(name, func(t *testing.T) {
			f := itermSpawn(t, &osa{reply: func(string, []string) ([]byte, error) { return []byte(reply), nil }})
			env := f.spawn(`"job":"api"`)
			wantKind(t, env, KindTerminal)
			if env.Error.Details["reason"] != "launch-failed" || env.Error.Details["terminal"] != "iterm2" {
				t.Errorf("%+v", env.Error.Details)
			}
			if f.reserved("api") || len(f.launchFiles()) != 0 {
				t.Errorf("reserved %v, launch files %v", f.reserved("api"), f.launchFiles())
			}
		})
	}
	t.Run("permission", func(t *testing.T) {
		f := itermSpawn(t, &osa{reply: func(string, []string) ([]byte, error) {
			return nil, errors.New("exit status 1: Not authorized to send Apple events to iTerm2. (-1743)")
		}})
		env := f.spawn(`"job":"api"`)
		wantKind(t, env, KindTerminal)
		if d, _ := env.Error.Details["detail"].(string); env.Error.Details["reason"] != "launch-failed" || !strings.Contains(d, "Privacy & Security → Automation") {
			t.Errorf("%+v", env.Error.Details)
		}
		if f.reserved("api") {
			t.Error("the job is not free")
		}
	})
}

func TestItermSpawnUnknownKeepsReservationAndFile(t *testing.T) {
	for name, reply := range map[string]func(string, []string) ([]byte, error){
		"timeout":        func(string, []string) ([]byte, error) { return nil, osaTimeout{} },
		"garbage":        func(string, []string) ([]byte, error) { return []byte("what\n"), nil },
		"created, no id": func(string, []string) ([]byte, error) { return []byte("created-unknown\n"), nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := itermSpawn(t, &osa{reply: reply})
			env := f.spawn(`"job":"api"`)
			wantKind(t, env, KindTerminal)
			if env.Error.Details["reason"] != "launch-unknown" {
				t.Errorf("%+v", env.Error.Details)
			}
			if !f.reserved("api") || len(f.launchFiles()) != 1 {
				t.Errorf("reserved %v, launch files %v", f.reserved("api"), f.launchFiles())
			}
		})
	}
}

// The launch file holds the arguments; only its nonce reaches the script.
func TestItermSpawnArgumentsStayOutOfTheScript(t *testing.T) {
	o := &osa{reply: func(string, []string) ([]byte, error) { return []byte(itermNew + "\n"), nil }}
	f := itermSpawn(t, o)
	f.spawned(`"prompt":"it's $(rm -rf ~)"`, `"start_timeout_secs":0`)
	if cmd := o.args[0][2]; !strings.HasPrefix(cmd, "'/usr/local/bin/sesshin' launch-exec ") || strings.Contains(cmd, "rm") {
		t.Errorf("command %q", cmd)
	}
	names := f.launchFiles()
	if len(names) != 1 {
		t.Fatalf("launch files %v", names)
	}
	b, _ := os.ReadFile(filepath.Join(f.loc.LaunchesDir(), names[0]))
	if !strings.Contains(string(b), "$(rm -rf ~)") || !strings.Contains(string(b), "SESSHIN_TOKEN") {
		t.Errorf("launch file %s", b)
	}
}

// send finds the pane by tty, and maps each failure to its reason.
func TestItermSendReasons(t *testing.T) {
	listing := itermCaller + "\t/dev/ttys011\n" + itermNew + "\t/dev/ttys012\n"
	cases := map[string]struct {
		reply  func(script string, args []string) ([]byte, error)
		reason string
	}{
		"sent":        {nil, ""},
		"not found":   {func(s string, a []string) ([]byte, error) { return nil, errors.New("x") }, "unreachable"},
		"tty gone":    {func(s string, a []string) ([]byte, error) { return []byte(itermNew + "\t/dev/ttys012\n"), nil }, "unreachable"},
		"not running": {func(s string, a []string) ([]byte, error) { return []byte("not-running\n"), nil }, "unreachable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			o := &osa{}
			o.reply = func(script string, args []string) ([]byte, error) {
				if tc.reply != nil {
					return tc.reply(script, args)
				}
				if strings.Contains(script, "tty of") {
					return []byte(listing), nil
				}
				return []byte("ok\n"), nil
			}
			f := newSendFixture(t)
			f.spawnFixture = itermSpawn(t, o)
			f.live(uuidA, 1, "", `{"terminal":"iterm2","session_id":"`+strings.ToLower(itermNew)+`"}`)
			env := f.send(uuidA)
			if tc.reason == "" {
				if !env.OK {
					t.Fatalf("%+v", env.Error)
				}
				if got := enc(t, env.Result.(SendOutput).Placement); got != `{"terminal":"iterm2","session_id":"`+itermCaller+`"}` {
					t.Errorf("placement %s", got)
				}
				return
			}
			wantKind(t, env, KindTerminal)
			if env.Error.Details["reason"] != tc.reason {
				t.Errorf("%+v", env.Error.Details)
			}
		})
	}
	t.Run("paste fails", func(t *testing.T) {
		o := &osa{reply: func(script string, args []string) ([]byte, error) {
			if strings.Contains(script, "tty of") {
				return []byte(listing), nil
			}
			return nil, osaTimeout{}
		}}
		f := newSendFixture(t)
		f.spawnFixture = itermSpawn(t, o)
		f.live(uuidA, 1, "", `{"terminal":"iterm2","session_id":"`+itermNew+`"}`)
		env := f.send(uuidA)
		wantKind(t, env, KindTerminal)
		if env.Error.Details["reason"] != "send-failed" {
			t.Errorf("%+v", env.Error.Details)
		}
	})
	t.Run("enter fails", func(t *testing.T) {
		o := &osa{reply: func(script string, args []string) ([]byte, error) {
			switch {
			case strings.Contains(script, "tty of"):
				return []byte(listing), nil
			case strings.Contains(script, "character id 13"):
				return nil, osaTimeout{}
			}
			return []byte("ok\n"), nil
		}}
		f := newSendFixture(t)
		f.spawnFixture = itermSpawn(t, o)
		f.live(uuidA, 1, "", `{"terminal":"iterm2","session_id":"`+itermNew+`"}`)
		env := f.send(uuidA)
		wantKind(t, env, KindTerminal)
		if env.Error.Details["reason"] != "submit-failed" {
			t.Errorf("%+v", env.Error.Details)
		}
	})
}

// focus falls back to the stored pane when none is found by pid, and reports
// a failed script as focus-failed.
func TestItermFocus(t *testing.T) {
	run := func(o *osa) Envelope {
		f := newFocusFixture(t)
		f.spawnFixture = itermSpawn(t, o)
		f.live(uuidA, 1, "", `{"terminal":"iterm2","session_id":"`+strings.ToLower(itermNew)+`"}`)
		return f.focus(uuidA)
	}
	t.Run("verified", func(t *testing.T) {
		o := &osa{reply: func(script string, args []string) ([]byte, error) {
			if strings.Contains(script, "tty of") {
				return []byte(itermCaller + "\t/dev/ttys011\n"), nil
			}
			return []byte("ok\n"), nil
		}}
		env := run(o)
		out, ok := env.Result.(FocusOutput)
		if !env.OK || !ok || !out.Verified || enc(t, out.Placement) != `{"terminal":"iterm2","session_id":"`+itermCaller+`"}` {
			t.Fatalf("%+v %+v", env, out)
		}
		if last := o.args[len(o.args)-1]; len(last) != 1 || last[0] != itermCaller {
			t.Errorf("focused %v", last)
		}
	})
	t.Run("stored pane when not found", func(t *testing.T) {
		o := &osa{reply: func(script string, args []string) ([]byte, error) {
			if strings.Contains(script, "tty of") {
				return []byte(itermNew + "\t/dev/ttys012\n"), nil
			}
			return []byte("ok\n"), nil
		}}
		out := run(o).Result.(FocusOutput)
		if out.Verified || enc(t, out.Placement) != `{"terminal":"iterm2","session_id":"`+itermNew+`"}` {
			t.Errorf("%+v", out)
		}
	})
	t.Run("focus fails", func(t *testing.T) {
		o := &osa{reply: func(script string, args []string) ([]byte, error) {
			if strings.Contains(script, "tty of") {
				return []byte(itermCaller + "\t/dev/ttys011\n"), nil
			}
			return []byte("not-found\n"), nil
		}}
		env := run(o)
		wantKind(t, env, KindTerminal)
		if env.Error.Details["reason"] != "focus-failed" {
			t.Errorf("%+v", env.Error.Details)
		}
	})
}
