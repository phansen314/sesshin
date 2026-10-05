package main

import (
	"os"
	"syscall"
)

// silenceStderr replaces file descriptor 2 with /dev/null, so a stray write
// from the runtime or a library can never reach Claude (hooks-spec.md, H3).
// It is process setup, not one of sesshin's file operations. When it fails, the
// hook carries on: it never fails over this.
func silenceStderr() {
	// Not O_CLOEXEC: with stderr closed the open lands on 2 itself, and a
	// child started later (kitten @ ls) must inherit it open. Nothing execs
	// between here and the dup, so a descriptor above 2 does not leak.
	fd, err := syscall.Open(os.DevNull, syscall.O_WRONLY, 0)
	if err != nil {
		return
	}
	if fd != 2 { // with stderr closed, the open may have taken 2 itself
		_ = dup(fd, 2)
		_ = syscall.Close(fd)
	}
}
