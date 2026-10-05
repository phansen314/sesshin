package hookconf

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

func TestParseGood(t *testing.T) {
	for _, c := range []struct {
		name, in string
		ms       int
	}{
		{"empty", "", DefaultLockWaitMS},
		{"comments and blanks", "# a comment\n\n  \t\n#hook_lock_wait_ms=1\n", DefaultLockWaitMS},
		{"set", "hook_lock_wait_ms=1500\n", 1500},
		{"no trailing newline", "hook_lock_wait_ms=1500", 1500},
		{"zero", "hook_lock_wait_ms=0", 0},
		{"max", "hook_lock_wait_ms=4000", 4000},
		{"leading zeros", "hook_lock_wait_ms=0100", 100},
		{"crlf", "# a comment\r\nhook_lock_wait_ms=3000\r\n", 3000},
		{"with a comment", "# wait less\nhook_lock_wait_ms=500\n", 500},
	} {
		s, problems := Parse([]byte(c.in))
		if len(problems) > 0 || s.LockWaitMS != c.ms {
			t.Errorf("%s: Parse(%q) = %d, %q; want %d", c.name, c.in, s.LockWaitMS, problems, c.ms)
		}
	}
}

func TestParseBad(t *testing.T) {
	for _, c := range []struct {
		name, in, want string
	}{
		{"out of range", "hook_lock_wait_ms=4001", `line 1: hook_lock_wait_ms is "4001", not an integer from 0 to 4000`},
		{"huge", "hook_lock_wait_ms=99999999999999999999999", `not an integer`},
		{"negative", "hook_lock_wait_ms=-1", `"-1", not an integer`},
		{"plus", "hook_lock_wait_ms=+5", `"+5", not an integer`},
		{"empty value", "hook_lock_wait_ms=", `"", not an integer`},
		{"decimal", "hook_lock_wait_ms=1.5", `"1.5", not an integer`},
		{"trailing space", "hook_lock_wait_ms=15 ", `"15 ", not an integer`},
		{"two carriage returns", "hook_lock_wait_ms=15\r\r\n", `"15\r", not an integer`},
		{"space before =", "hook_lock_wait_ms =15", `line 1: spaces around "="`},
		{"space after =", "hook_lock_wait_ms= 15", `line 1: spaces around "="`},
		{"no =", "\nhook_lock_wait_ms", `line 2: no "="`},
		{"indented comment", "  # x", `line 1: no "="`},
		{"unknown key", "hook_lock_wait=15", `line 1: unknown key "hook_lock_wait"`},
		{"repeated", "hook_lock_wait_ms=1\n\nhook_lock_wait_ms=1", `line 3: hook_lock_wait_ms given again (first on line 1)`},
	} {
		s, problems := Parse([]byte(c.in))
		got := strings.Join(problems, "; ")
		if !strings.Contains(got, c.want) || s != Default() {
			t.Errorf("%s: Parse(%q) = %d, %q; want default and %q", c.name, c.in, s.LockWaitMS, got, c.want)
		}
	}
}

// Every problem is reported, each with its line, and none stops the rest.
func TestParseEveryProblem(t *testing.T) {
	_, problems := Parse([]byte("a=1\nhook_lock_wait_ms=9000\nb\n"))
	want := []string{`line 1: unknown key "a"`, `line 2: hook_lock_wait_ms is "9000"`, `line 3: no "="`}
	if len(problems) != len(want) {
		t.Fatalf("problems %q, want %d", problems, len(want))
	}
	for i, w := range want {
		if !strings.HasPrefix(problems[i], w) {
			t.Errorf("problem %d = %q, want prefix %q", i, problems[i], w)
		}
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	s, err := Read(fsys.OS{}, path)
	if err != nil || s != Default() {
		t.Errorf("missing: %v, %v; want default, nil", s, err)
	}

	write(t, path, "hook_lock_wait_ms=250\n")
	s, err = Read(fsys.OS{}, path)
	if err != nil || s.LockWaitMS != 250 || s.LockWait() != 250*time.Millisecond {
		t.Errorf("good: %v, %v", s, err)
	}

	write(t, path, "hook_lock_wait_ms=x\nother=1\n")
	s, err = Read(fsys.OS{}, path)
	var ce *CorruptError
	if !errors.As(err, &ce) || ce.Path != path || s != Default() {
		t.Fatalf("bad: %v, %v; want default and a *CorruptError", s, err)
	}
	if !strings.Contains(ce.Detail, "line 1") || !strings.Contains(ce.Detail, "line 2") {
		t.Errorf("detail %q names both lines", ce.Detail)
	}

	// A directory in the file's place can't be read: the read's error, not
	// corrupt.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err = Read(fsys.OS{}, path)
	if errno, ok := fsys.ErrnoOf(err); !ok || errno != syscall.EISDIR || s != Default() {
		t.Errorf("directory: %v, %v; want default and EISDIR", s, err)
	}

	// A missing config directory is a missing file.
	s, err = Read(fsys.OS{}, filepath.Join(dir, "absent", FileName))
	if err != nil || s != Default() {
		t.Errorf("missing directory: %v, %v", s, err)
	}
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("hook_lock_wait_ms=2000\n"))
	f.Add([]byte("# c\n\nhook_lock_wait_ms=0"))
	f.Fuzz(func(t *testing.T, b []byte) {
		s, problems := Parse(b)
		if len(problems) > 0 && s != Default() {
			t.Fatalf("problems %q with settings %v", problems, s)
		}
		if s.LockWaitMS < 0 || s.LockWaitMS > MaxLockWaitMS {
			t.Fatalf("LockWaitMS %d out of range", s.LockWaitMS)
		}
	})
}
