//go:build amd64 || arm64

package pick

import "golang.org/x/sys/unix"

// macOS's termios requests: read, set after discarding what was typed
// ahead (and draining output), and set.
const (
	ttyGet      = unix.TIOCGETA
	ttySetFlush = unix.TIOCSETAF
	ttySet      = unix.TIOCSETA
)
