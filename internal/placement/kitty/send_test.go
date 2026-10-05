package kitty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A claude window runs claude as its one foreground process, and the shell's
// own pid is the window's pid; a window at a prompt lists the shell.
const sendLS = `[
 {"id":1,"tabs":[
  {"id":1,"title":"a","windows":[
   {"id":1,"pid":100,"foreground_processes":[{"pid":100,"cmdline":["zsh"]}]},
   {"id":2,"pid":200,"foreground_processes":[{"pid":4242,"cmdline":["claude"]},{"pid":4243,"cmdline":["sleep"]}]}]},
  {"id":2,"title":"b","windows":[
   {"id":3,"pid":4242,"foreground_processes":[{"pid":300,"cmdline":["vim"]}]}]}]},
 {"id":2,"tabs":[{"id":3,"title":"c","windows":[
   {"id":9,"pid":900,"foreground_processes":[{"pid":5555,"cmdline":["claude"]}]}]}]}
]`

func TestParseWindowForPID(t *testing.T) {
	for _, tc := range []struct {
		name string
		ls   string
		pid  int64
		want int64 // 0: no window
	}{
		{"a foreground process of the first window", sendLS, 100, 1},
		{"one of several foreground processes", sendLS, 4243, 2},
		{"the pid among them, not the window's pid", sendLS, 4242, 2},
		{"a second OS window", sendLS, 5555, 9},
		{"only the window's own pid is no match", sendLS, 900, 0},
		{"no such pid", sendLS, 1, 0},
		{"no windows", `[]`, 4242, 0},
		{"no foreground processes", `[{"tabs":[{"windows":[{"id":1,"pid":4242}]}]}]`, 4242, 0},
		{"a pid that only starts with it", sendLS, 424, 0},
	} {
		got, err := ParseWindowForPID([]byte(tc.ls), tc.pid)
		if tc.want == 0 {
			var e *Error
			if err == nil || !errors.As(err, &e) {
				t.Errorf("%s: got %d, %v; want an *Error", tc.name, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %d, %v; want %d", tc.name, got, err, tc.want)
		}
	}
}

func TestParseWindowForPIDMalformed(t *testing.T) {
	for name, ls := range map[string]string{
		"not json":        `hello`,
		"not a list":      `{"tabs":[]}`,
		"no window id":    `[{"tabs":[{"windows":[{"foreground_processes":[{"pid":7}]}]}]}]`,
		"a bad window id": `[{"tabs":[{"windows":[{"id":0,"foreground_processes":[{"pid":7}]}]}]}]`,
		"a fractional id": `[{"tabs":[{"windows":[{"id":1.5,"foreground_processes":[{"pid":7}]}]}]}]`,
		"a negative id":   `[{"tabs":[{"windows":[{"id":-2,"foreground_processes":[{"pid":7}]}]}]}]`,
		"a string pid id": `[{"tabs":[{"windows":[{"id":"3","foreground_processes":[{"pid":7}]}]}]}]`,
	} {
		if got, err := ParseWindowForPID([]byte(ls), 7); err == nil {
			t.Errorf("%s: got %d", name, got)
		}
	}
}

// shortSendLimit makes a hanging kitten cost little.
func shortSendLimit(t *testing.T) {
	t.Helper()
	old := sendLimit
	sendLimit = 200 * time.Millisecond
	t.Cleanup(func() { sendLimit = old })
}

func TestWindowForPID(t *testing.T) {
	log := recordingKitten(t, catPath(t)+` <<'EOF'
`+sendLS+`
EOF`)
	got, err := WindowForPID("unix:/x", 4242)
	if err != nil || got != 2 {
		t.Fatalf("got %d, %v", got, err)
	}
	if want := []string{"@", "--to", "unix:/x", "ls"}; !slices.Equal(recorded(t, log), want) {
		t.Errorf("kitten got %q, want %q", recorded(t, log), want)
	}
}

func TestWindowForPIDFailures(t *testing.T) {
	for name, script := range map[string]string{
		"nonzero exit": `exit 1`,
		"not ls":       `echo hello`,
		"no window":    `echo '[]'`,
	} {
		t.Run(name, func(t *testing.T) {
			fakeKitten(t, script)
			if got, err := WindowForPID("unix:/x", 4242); err == nil {
				t.Errorf("got %d", got)
			}
		})
	}
}

func TestWindowForPIDHang(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	fakeKitten(t, "exec "+sleep+" 5")
	shortSendLimit(t)
	start := time.Now()
	_, err = WindowForPID("unix:/x", 4242)
	var e *Error
	if !errors.As(err, &e) || !e.Timeout {
		t.Errorf("got %v, want a timeout", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

// catPath is cat's path: the fake kitten's PATH holds only itself.
func catPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("cat")
	if err != nil {
		t.Skip(err)
	}
	return p
}

// stdinKitten is a kitten that records its arguments, one per line, and the
// stdin of a call given --stdin, then runs script.
func stdinKitten(t *testing.T, script string) (args, stdin string) {
	t.Helper()
	stdin = filepath.Join(t.TempDir(), "stdin")
	args = recordingKitten(t, `case " $* " in *" --stdin "*) `+catPath(t)+` > `+stdin+` ;; esac
`+script)
	return args, stdin
}

func TestSendText(t *testing.T) {
	args, stdin := stdinKitten(t, ``)
	text := "line one\nline\ttwo\r\n 日本語 \\r $(x)"
	if err := SendText("unix:/x", 7, text, true); err != nil {
		t.Fatal(err)
	}
	// The log has both calls' arguments, one per line, in order.
	want := []string{
		"@", "--to", "unix:/x", "send-text", "--match", "id:7", "--bracketed-paste=disable", "--stdin",
		"@", "--to", "unix:/x", "send-text", "--match", "id:7", `\r`,
	}
	if got := recorded(t, args); !slices.Equal(got, want) {
		t.Errorf("kitten got\n %q\nwant\n %q", got, want)
	}
	b, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\x1b[200~" + text + "\x1b[201~"; string(b) != want {
		t.Errorf("stdin %q, want %q", b, want)
	}
}

func TestSendTextNoSubmit(t *testing.T) {
	args, _ := stdinKitten(t, ``)
	if err := SendText("unix:/x", 7, "hi", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"@", "--to", "unix:/x", "send-text", "--match", "id:7", "--bracketed-paste=disable", "--stdin"}
	if got := recorded(t, args); !slices.Equal(got, want) {
		t.Errorf("kitten got %q, want %q", got, want)
	}
}

// A text longer than kitty's 2048-byte chunks goes to kitten whole, on one
// stdin.
func TestSendTextLarge(t *testing.T) {
	_, stdin := stdinKitten(t, ``)
	text := strings.Repeat("0123456789abcdef\n", 1<<16) // 1 MiB
	if err := SendText("unix:/x", 7, text, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(stdin)
	if err != nil || string(b) != "\x1b[200~"+text+"\x1b[201~" {
		t.Errorf("stdin of %d bytes, %v", len(b), err)
	}
}

// The paste failing is not the submit failing, and a failed paste sends no
// Enter.
func TestSendTextFailures(t *testing.T) {
	t.Run("paste", func(t *testing.T) {
		args, _ := stdinKitten(t, `echo "Error: no such window" >&2
exit 1`)
		err := SendText("unix:/x", 7, "hi", true)
		var e *SendError
		if !errors.As(err, &e) || e.Submit || IsSubmit(err) || !strings.Contains(err.Error(), "no such window") {
			t.Fatalf("got %v", err)
		}
		if n := len(recorded(t, args)); n != 8 {
			t.Errorf("%d argument lines: Enter was sent after a failed paste", n)
		}
	})
	t.Run("submit", func(t *testing.T) {
		args, _ := stdinKitten(t, `case " $* " in *" --stdin "*) ;; *) exit 1 ;; esac`)
		err := SendText("unix:/x", 7, "hi", true)
		var e *SendError
		if !errors.As(err, &e) || !e.Submit || !IsSubmit(err) {
			t.Fatalf("got %v", err)
		}
		if n := len(recorded(t, args)); n != 15 {
			t.Errorf("%d argument lines", n)
		}
	})
	t.Run("no kitten", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		err := SendText("unix:/x", 7, "hi", true)
		if err == nil || IsSubmit(err) {
			t.Errorf("got %v", err)
		}
	})
}

func TestSendTextHang(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	fakeKitten(t, "exec "+sleep+" 5")
	shortSendLimit(t)
	start := time.Now()
	err = SendText("unix:/x", 7, "hi", true)
	var e *SendError
	if !errors.As(err, &e) || e.Submit || !strings.Contains(err.Error(), "limit passed") {
		t.Errorf("got %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestSendLimit(t *testing.T) {
	if sendLimit != 5*time.Second {
		t.Errorf("send limit %v, want 5s", sendLimit)
	}
}
