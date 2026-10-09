package proc

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/fsys"
)

// StartedAt is /proc's boot ID and stat field 22, read independently.
func TestStartedAtProcfs(t *testing.T) {
	pid := int64(os.Getpid())
	got, err := StartedAt(fsys.OS{}, pid)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	if want := "linux:" + strings.TrimSpace(string(boot)) + ":" + f[19]; got != want {
		t.Errorf("StartedAt = %q, want %q", got, want)
	}
}
