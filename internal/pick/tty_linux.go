//go:build linux && (amd64 || arm64)

package pick

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// showFailure writes msg and a newline to /dev/tty, then waits for one key,
// read with the terminal in raw mode (no line editing, no echo, and no
// signals from ctrl-c: it is a key like any other). The terminal is restored
// before it returns.
func showFailure(msg string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	fd := tty.Fd()
	if _, err := tty.WriteString(msg + "\r\n"); err != nil {
		return err
	}
	var saved syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, &saved); err != nil {
		return fmt.Errorf("reading terminal settings: %w", err)
	}
	raw := saved
	raw.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG
	raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME] = 1, 0
	// TCSETSF also discards what was typed ahead, so a key pressed before
	// the message was seen doesn't dismiss it.
	if err := ioctl(fd, tcsetsf, &raw); err != nil {
		return fmt.Errorf("setting raw mode: %w", err)
	}
	defer func() { _ = ioctl(fd, syscall.TCSETS, &saved) }()
	var key [1]byte
	_, err = tty.Read(key[:])
	return err
}

// tcsetsf is TCSETSF: TCSETS+2 on amd64 and arm64, the only architectures
// this file builds for; package syscall does not export it.
const tcsetsf = syscall.TCSETS + 2

func ioctl(fd uintptr, req uintptr, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(t))); errno != 0 {
		return errno
	}
	return nil
}
