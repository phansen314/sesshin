package iterm2

import (
	"os"
	"syscall"
	"testing"
)

func TestStatRdev(t *testing.T) {
	// /dev/null is a character device on every system.
	got, err := statRdev("/dev/null")
	if err != nil || got == 0 {
		t.Errorf("/dev/null: %d, %v", got, err)
	}
	st, _ := os.Stat("/dev/null")
	if want := rdevOf(st.Sys().(*syscall.Stat_t)); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
	if _, err := statRdev("/dev/no-such-tty"); err == nil {
		t.Error("a missing tty")
	}
}
