package iterm2

import (
	"syscall"
	"testing"
)

// macOS's st_rdev is a signed 32-bit integer; a device with its top bit set
// is the unsigned number proc.ControllingTTY reports, not a sign-extended one.
func TestRdevOfDoesNotSignExtend(t *testing.T) {
	st := &syscall.Stat_t{Rdev: -268435451} // 0xF0000005 as an int32
	if got, want := rdevOf(st), uint64(0xF0000005); got != want {
		t.Errorf("got %#x, want %#x", got, want)
	}
	if got := rdevOf(&syscall.Stat_t{Rdev: 268435461}); got != 268435461 {
		t.Errorf("got %d", got)
	}
}
