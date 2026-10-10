package proc

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// parseTdev's constants are x/sys/unix's layout of kinfo_proc.
func TestKinfoLayout(t *testing.T) {
	var k unix.KinfoProc
	if unsafe.Sizeof(k) != kinfoSize || unsafe.Offsetof(k.Eproc)+unsafe.Offsetof(k.Eproc.Tdev) != kinfoTdevOff {
		t.Errorf("kinfo_proc is %d bytes with e_tdev at %d; the constants say %d and %d",
			unsafe.Sizeof(k), unsafe.Offsetof(k.Eproc)+unsafe.Offsetof(k.Eproc.Tdev), kinfoSize, kinfoTdevOff)
	}
}

// This process's own terminal, when it has one, is the one sysctl's struct
// reports.
func TestControllingTTYLive(t *testing.T) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ControllingTTY(int64(os.Getpid()))
	if uint32(k.Eproc.Tdev) == noDev {
		if err != ErrNoTTY {
			t.Errorf("no terminal: %d, %v", got, err)
		}
		return
	}
	if err != nil || got != uint64(uint32(k.Eproc.Tdev)) {
		t.Errorf("got %d, %v; want %d", got, err, uint32(k.Eproc.Tdev))
	}
	if _, err := ControllingTTY(1 << 30); err != ErrNoProcess {
		t.Errorf("no such process: %v", err)
	}
}
