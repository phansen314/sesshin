package fsys

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

// Every errno from 1 to 255 with a real message on this platform must have a
// name, so a missing one fails CI rather than shipping.
func TestErrnoTableComplete(t *testing.T) {
	for n := 1; n <= 255; n++ {
		e := syscall.Errno(n)
		if e.Error() == fmt.Sprintf("errno %d", n) {
			continue
		}
		if _, ok := ErrnoName(e); !ok {
			t.Errorf("errno %d (%q) has no name", n, e.Error())
		}
	}
}

// Each name belongs to one errno, except an alias with its own number, which
// shares its canonical name (sharedNames, per platform). The switch can't
// hold one number twice: the compiler refuses it.
func TestErrnoNamesUnique(t *testing.T) {
	count := map[string]int{}
	for n := 1; n <= 4095; n++ {
		if name, ok := ErrnoName(syscall.Errno(n)); ok {
			count[name]++
		}
	}
	for name, n := range count {
		want := sharedNames[name]
		if want == 0 {
			want = 1
		}
		if n != want {
			t.Errorf("name %s used for %d errnos, want %d", name, n, want)
		}
	}
}

// Aliases get one fixed name on both platforms (aliasCases, per platform).
func TestErrnoAliases(t *testing.T) {
	cases := append(aliasCases, []struct {
		e    syscall.Errno
		want string
	}{
		{syscall.EAGAIN, "EAGAIN"},
		{syscall.ENOTSUP, "ENOTSUP"},
		{syscall.EDEADLK, "EDEADLK"},
		{syscall.ENOSPC, "ENOSPC"},
		{syscall.ENOENT, "ENOENT"},
	}...)
	for _, tc := range cases {
		if got, _ := ErrnoName(tc.e); got != tc.want {
			t.Errorf("ErrnoName(%d) = %q, want %q", int(tc.e), got, tc.want)
		}
	}
}

func TestErrnoNameUnknown(t *testing.T) {
	if name, ok := ErrnoName(syscall.Errno(4000)); ok || name != "" {
		t.Errorf("ErrnoName(4000) = %q, %v", name, ok)
	}
	if _, ok := ErrnoName(0); ok {
		t.Error("errno 0 has a name")
	}
}

func TestErrnoOf(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want syscall.Errno
		ok   bool
	}{
		{&os.PathError{Op: "write", Path: "a", Err: syscall.ENOSPC}, syscall.ENOSPC, true},
		{&os.LinkError{Op: "link", Old: "a", New: "b", Err: syscall.EEXIST}, syscall.EEXIST, true},
		{os.NewSyscallError("flock", syscall.EBADF), syscall.EBADF, true},
		{fmt.Errorf("wrapped: %w", syscall.EIO), syscall.EIO, true},
		{fmt.Errorf("plain"), 0, false},
	} {
		got, ok := ErrnoOf(tc.err)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ErrnoOf(%v) = %v, %v; want %v, %v", tc.err, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDescribe(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "/x/y", Err: syscall.EACCES}
	if got := Describe(err); got != "permission denied" {
		t.Errorf("got %q", got)
	}
	if got := Describe(errors.New("plain")); got != "plain" {
		t.Errorf("got %q", got)
	}
}
