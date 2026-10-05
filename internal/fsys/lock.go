package fsys

import (
	"os"
	"syscall"
	"time"
)

// flock is syscall.Flock; a test replaces it to inject EINTR.
var flock = syscall.Flock

// The lock's poll: a first retry after lockFirst, the delay doubling up to
// lockMax. Measured under contention (implementation-spec.md, Locks).
const (
	lockFirst = 10 * time.Microsecond
	lockMax   = 250 * time.Microsecond
)

// pause blocks the thread in select(2) for d, at microsecond resolution.
// On Linux, time.Sleep can't wait less than a millisecond in a process with
// nothing else to run: the runtime then waits in epoll_wait, whose timeout is
// in milliseconds. A signal can end the pause early, which only makes the
// next try sooner.
func pause(d time.Duration) {
	tv := syscall.NsecToTimeval(d.Nanoseconds())
	syscall.Select(0, nil, nil, nil, &tv)
}

func (r *osRoot) Lock(wait time.Duration) (Lock, error) {
	f, err := r.r.Open(".")
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	delay := lockFirst
	for {
		ferr := tryLock(f)
		if ferr == nil {
			return &osLock{f: f}, nil
		}
		left := time.Until(deadline)
		if ferr != syscall.EAGAIN || left <= 0 {
			f.Close()
			return nil, &os.PathError{Op: "flock", Path: ".", Err: ferr}
		}
		pause(min(delay, left))
		delay = min(2*delay, lockMax)
	}
}

// tryLock makes one flock(LOCK_EX|LOCK_NB) on f, retrying EINTR. It returns
// the errno, or another error from reaching the descriptor.
func tryLock(f *os.File) error {
	c, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := c.Control(func(fd uintptr) {
		for {
			ferr = flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB)
			if ferr != syscall.EINTR {
				return
			}
		}
	}); err != nil {
		return err
	}
	return ferr
}

// osLock holds the locked directory's *os.File. While it is referenced, no
// finalizer can close the descriptor and silently release the lock.
type osLock struct {
	f *os.File
}

func (l *osLock) Unlock() error { return l.f.Close() }

// Moved compares the root's directory (fstatat on the root's own descriptor)
// with what Name leads to now (stat, following symlinks). Name leading
// nowhere is moved too.
func (r *osRoot) Moved() (bool, error) {
	here, err := r.r.Stat(".")
	if err != nil {
		return false, err
	}
	there, err := os.Stat(r.name)
	if err != nil {
		if errnoIs(err, syscall.ENOENT) || errnoIs(err, syscall.ENOTDIR) {
			return true, nil
		}
		return false, err
	}
	return !os.SameFile(here, there), nil
}
