package kitty

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFocusWindow(t *testing.T) {
	log := recordingKitten(t, ``)
	if err := FocusWindow("unix:/x", 7); err != nil {
		t.Fatal(err)
	}
	if want := []string{"@", "--to", "unix:/x", "focus-window", "--match", "id:7"}; !slices.Equal(recorded(t, log), want) {
		t.Errorf("kitten got %q, want %q", recorded(t, log), want)
	}
}

func TestFocusWindowFailures(t *testing.T) {
	fakeKitten(t, "echo 'Error: no such window' >&2\nexit 1")
	if err := FocusWindow("unix:/x", 7); err == nil || !strings.Contains(err.Error(), "no such window") {
		t.Errorf("got %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if err := FocusWindow("unix:/x", 7); err == nil {
		t.Error("no kitten: no error")
	}
}

func TestFocusWindowHang(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	fakeKitten(t, "exec "+sleep+" 5")
	old := focusLimit
	focusLimit = 200 * time.Millisecond
	t.Cleanup(func() { focusLimit = old })
	start := time.Now()
	err = FocusWindow("unix:/x", 7)
	if err == nil || !strings.Contains(err.Error(), "limit passed") {
		t.Errorf("got %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestFocusLimit(t *testing.T) {
	if focusLimit != 5*time.Second {
		t.Errorf("focus limit %v, want 5s", focusLimit)
	}
}
