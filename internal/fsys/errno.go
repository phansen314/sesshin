package fsys

import (
	"errors"
	"syscall"
)

// ErrnoName returns e's symbolic name, the same on Linux and macOS (e.g.
// "ENOSPC"). ok is false for an errno not in the table, which callers must
// report as internal, never with an invented name. The table is a switch
// per OS (errnoName), not a map, so it costs nothing at initialization. Only
// ops uses it, to name an errno in an error; the tables are for no one else.
//
// Aliases sharing a number get one fixed name on both platforms: EAGAIN (not
// EWOULDBLOCK), ENOTSUP (not EOPNOTSUPP), EDEADLK (not EDEADLOCK).
func ErrnoName(e syscall.Errno) (name string, ok bool) {
	name = errnoName(e)
	return name, name != ""
}

// ErrnoOf finds the errno inside err, through *os.PathError, *os.LinkError,
// and *os.SyscallError.
func ErrnoOf(err error) (syscall.Errno, bool) {
	var e syscall.Errno
	if errors.As(err, &e) {
		return e, true
	}
	return 0, false
}

func errnoIs(err error, want syscall.Errno) bool {
	e, ok := ErrnoOf(err)
	return ok && e == want
}

// Describe is err in the log's words: the OS's text for its errno
// ("permission denied"), without the path an *os.PathError carries, since the
// caller names the file; err.Error() when it holds no errno.
func Describe(err error) string {
	if e, ok := ErrnoOf(err); ok {
		return e.Error()
	}
	return err.Error()
}
