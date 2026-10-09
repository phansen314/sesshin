//go:build amd64 || arm64

package pick

import "golang.org/x/sys/unix"

// Linux's termios requests: read, set after discarding what was typed
// ahead, and set.
const (
	ttyGet      = unix.TCGETS
	ttySetFlush = unix.TCSETSF
	ttySet      = unix.TCSETS
)
