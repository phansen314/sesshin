package proc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/fsys"
)

// The real /proc, with the test binary run under the names Claude goes by.
// As "claude", it runs the test binary again as its child, which looks
// Claude up as a hook would and prints what it found.
const helperEnv = "SESSHIN_PROC_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "claude":
		cmd := exec.Command(os.Getenv("SESSHIN_PROC_TEST_BIN"), "-test.run=^$")
		cmd.Env = append(os.Environ(), helperEnv+"=hook")
		if os.Getenv("SESSHIN_PROC_SET_PID") != "" {
			// As Claude does for everything it starts.
			cmd.Env = append(cmd.Env, "CLAUDE_PID="+strconv.Itoa(os.Getpid()))
		}
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "hook":
		_ = json.NewEncoder(os.Stdout).Encode(Find(fsys.OS{}, os.Getenv("CLAUDE_PID")))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runAs runs the test binary as Claude, from a copy at path, with env added
// to an environment without CLAUDE_PID or CLAUDECODE; it returns Claude's
// pid and what its child found.
func runAs(t *testing.T, path string, env ...string) (int64, Claude) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	base := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "CLAUDE_PID=") || strings.HasPrefix(kv, "CLAUDECODE=")
	})
	cmd := exec.Command(path, "-test.run=^$")
	cmd.Env = append(append(base, helperEnv+"=claude", "SESSHIN_PROC_TEST_BIN="+self), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := int64(cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	var c Claude
	if err := json.Unmarshal(out.Bytes(), &c); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	return pid, c
}

func TestFindLive(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, path string
		env        []string
		claudePID  bool
		nested     bool
	}{
		// The walk stops at the nearest claude: the helper, even when the
		// test itself runs under a Claude session.
		{"walk by name", "a/claude", nil, false, false},
		{"walk by versioned executable", "b/claude/versions/9.9.9", nil, false, false},
		{"started by another session", "c/claude", []string{"CLAUDECODE=1"}, false, true},
		{"CLAUDE_PID names an executable not named claude", "d/node", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			if tc.claudePID {
				env = append(env, "SESSHIN_PROC_SET_PID=1")
			}
			pid, got := runAs(t, filepath.Join(dir, tc.path), env...)
			if got.PID != pid || got.Nested == nil || *got.Nested != tc.nested {
				t.Errorf("found %+v; want pid %d, nested %v", got, pid, tc.nested)
			}
			if !strings.HasPrefix(got.StartedAt, "linux:") || len(got.StartedAt) > 128 {
				t.Errorf("started at %q", got.StartedAt)
			}
		})
	}
}

// CLAUDE_PID names the hook's parent: the test's own parent here.
func TestFindClaudePID(t *testing.T) {
	ppid := int64(os.Getppid())
	got := Find(fsys.OS{}, strconv.FormatInt(ppid, 10))
	want, err := StartedAt(fsys.OS{}, ppid)
	if err != nil || got.PID != ppid || got.StartedAt != want || got.Nested == nil {
		t.Errorf("Find = %+v; want pid %d started %q (%v)", got, ppid, want, err)
	}
}

func TestStartedAtLive(t *testing.T) {
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
	if again, _ := StartedAt(fsys.OS{}, pid); again != got {
		t.Errorf("StartedAt changed: %q, then %q", got, again)
	}
	if _, err := StartedAt(fsys.OS{}, 1<<30); err == nil {
		t.Error("StartedAt of no process succeeded")
	}
}

// The lookup a hook makes, by CLAUDE_PID: a few small reads of /proc.
func BenchmarkFind(b *testing.B) {
	ppid := strconv.Itoa(os.Getppid())
	b.ReportAllocs()
	for b.Loop() {
		if Find(fsys.OS{}, ppid).PID == 0 {
			b.Fatal("not found")
		}
	}
}
