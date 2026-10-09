//go:build (linux || darwin) && (amd64 || arm64)

package pick

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// showFailure writes msg and a newline to /dev/tty, then waits for one key,
// read with the terminal in raw mode (no line editing, no echo, and no
// signals from ctrl-c: it is a key like any other). The terminal is restored
// before it returns. With no /dev/tty, msg goes to stderr instead, since the
// command left it out of stderr's note (cli-spec.md, Output). Only the
// ioctl requests differ by system: tty_linux.go and tty_darwin.go.
func showFailure(msg string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		_, _ = os.Stderr.WriteString("sesshin: " + msg + "\n")
		return err
	}
	defer tty.Close()
	fd := int(tty.Fd())
	if _, err := tty.WriteString(msg + "\r\n"); err != nil {
		return err
	}
	saved, err := unix.IoctlGetTermios(fd, ttyGet)
	if err != nil {
		return fmt.Errorf("reading terminal settings: %w", err)
	}
	raw := *saved
	raw.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
	// ttySetFlush also discards what was typed ahead, so a key pressed
	// before the message was seen doesn't dismiss it.
	if err := unix.IoctlSetTermios(fd, ttySetFlush, &raw); err != nil {
		return fmt.Errorf("setting raw mode: %w", err)
	}
	defer func() { _ = unix.IoctlSetTermios(fd, ttySet, saved) }()
	var key [1]byte
	_, err = tty.Read(key[:])
	return err
}
