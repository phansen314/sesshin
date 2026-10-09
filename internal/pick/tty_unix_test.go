//go:build (linux || darwin) && (amd64 || arm64)

package pick

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const (
	ttyChildVar   = "SESSHIN_TEST_SHOW_FAILURE"
	ttyTermiosVar = "SESSHIN_TEST_TERMIOS_FILE"
)

// TestShowFailureChild is showFailure in a process of its own, which the test below
// starts with a pty as its controlling terminal.
func TestShowFailureChild(t *testing.T) {
	msg := os.Getenv(ttyChildVar)
	if msg == "" {
		t.Skip("run by TestShowFailureOnPty")
	}
	if err := showFailure(msg); err != nil {
		os.Stderr.WriteString("showFailure: " + err.Error() + "\r\n")
		os.Exit(2)
	}
	// The terminal as showFailure left it, reported from here: macOS revokes
	// a terminal when its session leader exits, so the test can't read it
	// afterwards.
	tm, err := unix.IoctlGetTermios(0, ttyGet)
	if err != nil {
		os.Stderr.WriteString("termios: " + err.Error() + "\r\n")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv(ttyTermiosVar), []byte(termiosState(*tm)), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// termiosState is what showFailure must restore: the local modes and the
// control characters.
func termiosState(tm unix.Termios) string {
	return fmt.Sprintf("lflag %#x cc %x", tm.Lflag, tm.Cc)
}

// openPty opens a pty pair.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	return master, slave
}

func termios(t *testing.T, f *os.File) unix.Termios {
	t.Helper()
	tm, err := unix.IoctlGetTermios(int(f.Fd()), ttyGet)
	if err != nil {
		t.Fatal(err)
	}
	return *tm
}

// showFailure (tty_unix.go) shows the message, waits in raw mode for one key, takes ctrl-c
// as that key (no SIGINT), drops what was typed before it, and restores the
// terminal. It is run in a child with a pty as its controlling terminal.
func TestShowFailureOnPty(t *testing.T) {
	master, slave := openPty(t)
	before := termios(t, slave)
	const cooked = unix.ICANON | unix.ECHO | unix.ISIG
	if before.Lflag&cooked != cooked {
		t.Fatalf("a new pty is not cooked: %#x", before.Lflag)
	}
	// Typed ahead, before the message: flushed by ttySetFlush, so it is not the
	// key. The master's input queue is the slave's.
	if _, err := master.WriteString("x"); err != nil {
		t.Fatal(err)
	}

	// What the terminal shows, read as it comes: a pty's master has no read
	// deadline.
	var out struct {
		sync.Mutex
		bytes.Buffer
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			out.Lock()
			out.Write(buf[:n])
			out.Unlock()
			if err != nil {
				return
			}
		}
	}()

	cmd := exec.Command(os.Args[0], "-test.run=^TestShowFailureChild$")
	termiosFile := filepath.Join(t.TempDir(), "termios")
	cmd.Env = append(os.Environ(), ttyChildVar+"=something failed", ttyTermiosVar+"="+termiosFile)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	// Raw mode: no line editing, no echo, no signals.
	deadline := time.Now().Add(10 * time.Second)
	for termios(t, slave).Lflag&cooked != 0 {
		select {
		case err := <-done:
			t.Fatalf("child ended before raw mode: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("raw mode never set")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The key typed ahead did not dismiss it.
	select {
	case err := <-done:
		t.Fatalf("child ended on a key typed ahead: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	// ctrl-c is a key like any other: it ends the wait, and nothing is
	// signaled.
	if _, err := master.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("child: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ctrl-c did not end the wait")
	}

	if after, err := os.ReadFile(termiosFile); err != nil {
		t.Fatal(err)
	} else if want := termiosState(before); string(after) != want {
		t.Errorf("terminal not restored: %s, want %s", after, want)
	}
	// The message was written on the terminal, before the wait.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		out.Lock()
		got := out.String()
		out.Unlock()
		if strings.Contains(got, "something failed\r") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal output %q", got)
		}
	}
}
