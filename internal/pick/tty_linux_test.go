//go:build linux && (amd64 || arm64)

package pick

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Linux's pty ioctls, which package syscall does not export.
const (
	tiocgptn   = 0x80045430
	tiocsptlck = 0x40045431
)

const ttyChildVar = "SESSHIN_TEST_SHOW_FAILURE"

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
	os.Exit(0)
}

// openPty opens a pty pair via /dev/ptmx.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	var n, unlock uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocsptlck, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Fatal(e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocgptn, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Fatal(e)
	}
	slave, err = os.OpenFile("/dev/pts/"+itoa32(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func itoa32(n uint32) string {
	var b []byte
	for ; n > 0 || len(b) == 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func termios(t *testing.T, f *os.File) syscall.Termios {
	t.Helper()
	var tm syscall.Termios
	if err := ioctl(f.Fd(), syscall.TCGETS, &tm); err != nil {
		t.Fatal(err)
	}
	return tm
}

// showFailure (tty_linux.go) shows the message, waits in raw mode for one key, takes ctrl-c
// as that key (no SIGINT), drops what was typed before it, and restores the
// terminal. It is run in a child with a pty as its controlling terminal.
func TestShowFailureOnPty(t *testing.T) {
	master, slave := openPty(t)
	before := termios(t, slave)
	const cooked = syscall.ICANON | syscall.ECHO | syscall.ISIG
	if before.Lflag&cooked != cooked {
		t.Fatalf("a new pty is not cooked: %#x", before.Lflag)
	}
	// Typed ahead, before the message: flushed by TCSETSF, so it is not the
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
	cmd.Env = append(os.Environ(), ttyChildVar+"=something failed")
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

	if after := termios(t, slave); after.Lflag != before.Lflag || after.Cc != before.Cc {
		t.Errorf("terminal not restored: lflag %#x, want %#x", after.Lflag, before.Lflag)
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
