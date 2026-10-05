package hooklog

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

const session = "0fa3d657-1c2b-4d5e-8f90-123456789abc"

var now = time.Date(2026, 10, 3, 18, 31, 51, 999_000_000, time.FixedZone("MDT", -6*3600))

func newRoot(t *testing.T) (fsys.Root, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := fsys.OS{}.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(b)
}

func TestLine(t *testing.T) {
	for _, c := range []struct {
		name, verb, session, msg, want string
	}{
		{"plain", "stop", session, "state lock: wait ran out",
			"2026-10-04T00:31:51Z stop " + session + " state lock: wait ran out\n"},
		{"no session", "session-start", "", "HOME unset", "2026-10-04T00:31:51Z session-start - HOME unset\n"},
		{"no verb", "", "", "x", "2026-10-04T00:31:51Z - - x\n"},
		{"controls", "stop", session, "a\nb\r\tc\x00\x1f\x7f\\n", "2026-10-04T00:31:51Z stop " + session + ` a\nb\r\tc\x00\x1f\x7f\\n` + "\n"},
		{"space in a field", "a b", "c\nd", "m", `2026-10-04T00:31:51Z a\x20b c\nd m` + "\n"},
		{"utf-8 kept", "stop", session, "café ✓", "2026-10-04T00:31:51Z stop " + session + " café ✓\n"},
		{"line separators", "stop", session, "a\u2028b\u2029c\u0085d\u009fe", "2026-10-04T00:31:51Z stop " + session + ` a\u2028b\u2029c\u0085d\u009fe` + "\n"},
		{"invalid utf-8 kept", "stop", "\xff", "x\xc2", "2026-10-04T00:31:51Z stop \xff x\xc2\n"},
		{"utf-8 in a field", "stöp ✓", session, "m", `2026-10-04T00:31:51Z stöp\x20✓ ` + session + " m\n"},
	} {
		if got := string(Line(now, c.verb, c.session, c.msg)); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// A long message is cut at MaxMessage bytes, never inside a character.
func TestLineCut(t *testing.T) {
	for _, pad := range []int{0, 1, 2, 3} {
		msg := strings.Repeat("a", pad) + strings.Repeat("✓", MaxMessage)
		line := string(Line(now, "stop", session, msg))
		got := strings.TrimSuffix(line[strings.LastIndexByte(line[:len(line)-1], ' ')+1:], "\n")
		if !strings.HasSuffix(got, "…") || len(got) > MaxMessage+len("…") || len(got) < MaxMessage-3 {
			t.Errorf("pad %d: message of %d bytes", pad, len(got))
		}
		if !strings.HasPrefix(got, strings.Repeat("a", pad)+"✓") || strings.ContainsRune(got, '\uFFFD') {
			t.Errorf("pad %d: cut inside a character", pad)
		}
		if strings.Count(line, "\n") != 1 {
			t.Errorf("pad %d: not one line", pad)
		}
	}
}

func TestAppend(t *testing.T) {
	r, dir := newRoot(t)
	must(t, Append(r, now, "stop", session, "one"))
	must(t, Append(r, now, "stop", session, "two"))
	want := string(Line(now, "stop", session, "one")) + string(Line(now, "stop", session, "two"))
	if got := read(t, filepath.Join(dir, FileName)); got != want {
		t.Errorf("log %q, want %q", got, want)
	}
	if fi, err := os.Stat(filepath.Join(dir, FileName)); err != nil || fi.Mode() != fsys.FileMode {
		t.Errorf("mode: %v, %v", fi.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, RotatedName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rotated below the limit: %v", err)
	}
}

// Past the limit the log becomes hooks.log.1, replacing the previous one,
// and the next entry starts a new log. At the limit, it stays.
func TestRotation(t *testing.T) {
	r, dir := newRoot(t)
	log, old := filepath.Join(dir, FileName), filepath.Join(dir, RotatedName)
	line := Line(now, "stop", session, "x")
	n := int64(len(line))

	must(t, appendLimit(r, now, "stop", session, "x", n))
	if got := read(t, log); got != string(line) {
		t.Fatalf("at the limit: log %q", got)
	}
	must(t, appendLimit(r, now, "stop", session, "x", n))
	if got := read(t, log); got != "" {
		t.Errorf("past the limit: log %q, want none", got)
	}
	if got := read(t, old); got != strings.Repeat(string(line), 2) {
		t.Errorf("rotated %q", got)
	}
	must(t, appendLimit(r, now, "stop", session, "y", n))
	must(t, appendLimit(r, now, "stop", session, "z", n))
	if got, want := read(t, old), string(Line(now, "stop", session, "y"))+string(Line(now, "stop", session, "z")); got != want {
		t.Errorf("second rotation: %q, want %q", got, want)
	}
}

// Two hooks that both saw the log past the limit: the second to get the
// lock finds the path naming a new file, and leaves the full log alone.
func TestRotationRaceAfterRename(t *testing.T) {
	r, dir := newRoot(t)
	must(t, os.WriteFile(filepath.Join(dir, FileName), []byte("full\n"), 0o600))
	a, err := r.OpenAppend(FileName)
	must(t, err)
	b, err := r.OpenAppend(FileName)
	must(t, err)
	defer b.Close()

	must(t, rotate(r, a, 1))
	must(t, a.Close())
	// A new, nearly empty log, which b must not move over the full one.
	must(t, Append(r, now, "stop", session, "fresh"))
	must(t, rotate(r, b, 1))
	if got := read(t, filepath.Join(dir, RotatedName)); got != "full\n" {
		t.Errorf("hooks.log.1 = %q, want the full log", got)
	}
	if got := read(t, filepath.Join(dir, FileName)); !strings.HasSuffix(got, "fresh\n") {
		t.Errorf("hooks.log = %q, want the fresh log", got)
	}

	// The same with nothing at the path yet.
	c, err := r.OpenAppend(FileName)
	must(t, err)
	defer c.Close()
	must(t, os.Rename(filepath.Join(dir, FileName), filepath.Join(dir, "elsewhere")))
	must(t, rotate(r, c, 1))
	if got := read(t, filepath.Join(dir, RotatedName)); got != "full\n" {
		t.Errorf("hooks.log.1 = %q after a rotation with no log", got)
	}
}

// A hook that finds the lock held skips: another is rotating.
func TestRotationSkipsWhenLocked(t *testing.T) {
	r, dir := newRoot(t)
	must(t, os.WriteFile(filepath.Join(dir, FileName), []byte("full\n"), 0o600))
	holder, err := r.OpenAppend(FileName)
	must(t, err)
	defer holder.Close()
	must(t, holder.TryLock())
	must(t, appendLimit(r, now, "stop", session, "x", 1))
	if _, err := os.Stat(filepath.Join(dir, RotatedName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rotated while another held the lock")
	}
}

// Errors come back for tests; nothing panics.
func TestAppendErrors(t *testing.T) {
	_, dir := newRoot(t)
	for _, c := range []struct {
		op   string
		want syscall.Errno
	}{
		{fsys.OpOpenAppend, syscall.EACCES},
		{fsys.OpWrite, syscall.ENOSPC},
		{fsys.OpTryLock, syscall.EBADF},
		{fsys.OpRename, syscall.EROFS},
	} {
		fr, err := fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(c.op, "", 1, c.want)}.OpenRoot(dir)
		must(t, err)
		err = appendLimit(fr, now, "stop", session, "x", 1)
		fr.Close()
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.op, err, c.want)
		}
	}
}

// Many hooks appending at once, rotating as they go: every line in either
// file is whole, and hooks.log.1 is always a log that was past the limit,
// never a fresh one moved over it.
func TestConcurrentAppends(t *testing.T) {
	r, dir := newRoot(t)
	const writers, each = 16, 200
	limit := int64(4096)
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				msg := fmt.Sprintf("writer %d line %d %s", w, i, strings.Repeat("p", i%50))
				if err := appendLimit(r, now, "post-tool-use", session, msg, limit); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	re := regexp.MustCompile(`^2026-10-04T00:31:51Z post-tool-use ` + session + ` writer \d+ line \d+ p*$`)
	lines := 0
	for _, name := range []string{FileName, RotatedName} {
		f, err := os.Open(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		must(t, err)
		s := bufio.NewScanner(f)
		for s.Scan() {
			lines++
			if !re.MatchString(s.Text()) {
				t.Errorf("%s: torn line %q", name, s.Text())
			}
		}
		f.Close()
	}
	if lines == 0 {
		t.Error("no lines")
	}
	if fi, err := os.Stat(filepath.Join(dir, RotatedName)); err != nil || fi.Size() <= limit {
		t.Errorf("hooks.log.1: %v, %v; want a log past the limit", fi, err)
	}
}
