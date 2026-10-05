package fsys

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// newRoot creates a temp directory, runs setup in it, and opens it as a Root.
func newRoot(t *testing.T, setup func(dir string)) (Root, string) {
	t.Helper()
	dir := t.TempDir()
	if setup != nil {
		setup(dir)
	}
	r, err := OS{}.OpenRoot(dir)
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

func wantErrno(t *testing.T, err error, want syscall.Errno) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// within runs f, failing the test if it hasn't returned within 5 seconds:
// for calls that must not block.
func within(t *testing.T, what string, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked", what)
		return nil
	}
}

// symlinkTree has a regular file, a directory, and a symlink to each, all
// inside the root.
func symlinkTree(t *testing.T) func(string) {
	return func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("{}"), 0o600))
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o700))
		must(t, os.WriteFile(filepath.Join(dir, "sub", "x"), []byte("x"), 0o600))
		must(t, os.Symlink("1.json", filepath.Join(dir, "2.json")))
		must(t, os.Symlink("sub", filepath.Join(dir, "link")))
	}
}

// Symlinks inside the root are followed (implementation-spec.md, Roots).
func TestSymlinksFollowed(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	got, err := r.ReadFile("2.json")
	must(t, err)
	if string(got) != "{}" {
		t.Errorf("2.json: got %q", got)
	}
	got, err = r.ReadFile("link/x")
	must(t, err)
	if string(got) != "x" {
		t.Errorf("link/x: got %q", got)
	}
	es, err := r.ReadDir("link")
	must(t, err)
	if len(es) != 1 || es[0].Name() != "x" {
		t.Errorf("ReadDir(link) = %v", es)
	}
	fi, err := r.Stat("link")
	must(t, err)
	if !fi.IsDir() {
		t.Errorf("Stat(link): mode %v, want a directory", fi.Mode())
	}
	sub, err := r.OpenRoot("link")
	must(t, err)
	defer sub.Close()
	if _, err := sub.ReadFile("x"); err != nil {
		t.Error(err)
	}
}

// A symlink leading outside the root is refused, with os.Root's error,
// which holds no errno.
func TestSymlinkOutsideRefused(t *testing.T) {
	outside := t.TempDir()
	must(t, os.WriteFile(filepath.Join(outside, "x"), []byte("x"), 0o600))
	r, _ := newRoot(t, func(dir string) {
		must(t, os.Symlink(filepath.Join(outside, "x"), filepath.Join(dir, "1.json")))
	})
	_, err := r.ReadFile("1.json")
	if err == nil {
		t.Fatal("read a file outside the root")
	}
	if e, ok := ErrnoOf(err); ok {
		t.Errorf("got errno %v, want os.Root's error", e)
	}
}

func TestReadFileDirectory(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	_, err := r.ReadFile("sub")
	wantErrno(t, err, syscall.EISDIR)
}

func TestReadFileMissing(t *testing.T) {
	r, _ := newRoot(t, nil)
	_, err := r.ReadFile("1.json")
	wantErrno(t, err, syscall.ENOENT)
}

// Sizes around the read buffer's growth points.
func TestReadFileSizes(t *testing.T) {
	r, dir := newRoot(t, nil)
	for _, n := range []int{0, 1, 511, 512, 513, 100_000} {
		want := strings.Repeat("a", n)
		must(t, os.WriteFile(filepath.Join(dir, "f"), []byte(want), 0o600))
		got, err := r.ReadFile("f")
		must(t, err)
		if string(got) != want {
			t.Errorf("size %d: read %d bytes", n, len(got))
		}
	}
}

// A FIFO reads as empty without blocking, with or without a writer holding it
// open, through a root and outside one (implementation-spec.md, Roots: reads
// can't block).
func TestReadFileFIFODoesNotBlock(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, syscall.Mkfifo(filepath.Join(dir, "1.json"), 0o600))
	})
	fifo := filepath.Join(dir, "1.json")
	readEmpty := func() {
		t.Helper()
		for _, read := range []func() ([]byte, error){
			func() ([]byte, error) { return r.ReadFile("1.json") },
			func() ([]byte, error) { return OS{}.ReadFile(fifo) },
		} {
			must(t, within(t, "ReadFile on a FIFO", func() error {
				got, err := read()
				if err == nil && len(got) != 0 {
					err = fmt.Errorf("got %q", got)
				}
				return err
			}))
		}
	}
	readEmpty()

	// O_RDWR opens a FIFO without waiting for a reader: a writer that never
	// writes.
	w, err := os.OpenFile(fifo, os.O_RDWR, 0)
	must(t, err)
	defer w.Close()
	readEmpty()
}

// A FIFO where a directory should be fails with ENOTDIR rather than
// blocking the open.
func TestFIFOInPlaceOfDirectory(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, syscall.Mkfifo(filepath.Join(dir, "sub"), 0o600))
	})
	wantErrno(t, within(t, "ReadDir", func() error { _, err := r.ReadDir("sub"); return err }), syscall.ENOTDIR)
	wantErrno(t, within(t, "Root.OpenRoot", func() error { _, err := r.OpenRoot("sub"); return err }), syscall.ENOTDIR)
	wantErrno(t, within(t, "OpenRoot", func() error { _, err := OS{}.OpenRoot(filepath.Join(dir, "sub")); return err }), syscall.ENOTDIR)
}

// A file replaced over and over by concurrent renames, as lifecycle.json is
// under a burst of hooks, is always read whole, in one version or another.
func TestReadFileUnderConcurrentReplacement(t *testing.T) {
	const writers = 4
	r, dir := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("version"), 0o600))
	})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				tmp := filepath.Join(dir, fmt.Sprintf("tmp%d-%d", w, i))
				if err := os.WriteFile(tmp, []byte("version"), 0o600); err != nil {
					t.Error(err)
					return
				}
				if err := os.Rename(tmp, filepath.Join(dir, "1.json")); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for range 5000 {
		got, err := r.ReadFile("1.json")
		if err != nil || string(got) != "version" {
			t.Errorf("read %q, %v", got, err)
			break
		}
	}
	close(stop)
	wg.Wait()
}

// Files are FileMode whatever the umask, even one that takes the owner's
// bits.
func TestCreateTempMode(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o700))
	})
	for _, umask := range []int{0, 0o022, 0o077, 0o277, 0o777} {
		old := syscall.Umask(umask)
		f, name, err := r.CreateTemp("sub")
		syscall.Umask(old)
		must(t, err)
		must(t, f.Close())
		fi, err := os.Lstat(filepath.Join(dir, name))
		must(t, err)
		if got := fi.Mode(); got != 0o600 {
			t.Errorf("umask %03o: mode %v, want 0600", umask, got)
		}
	}
}

func TestCreateTemp(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o700))
	})
	f, name, err := r.CreateTemp("sub")
	must(t, err)
	if !strings.HasPrefix(name, "sub/"+TempPrefix) {
		t.Fatalf("name %q", name)
	}
	_, err = f.Write([]byte("hello"))
	must(t, err)
	must(t, f.Close())
	got, err := os.ReadFile(filepath.Join(dir, name))
	must(t, err)
	if string(got) != "hello" {
		t.Errorf("got %q", got)
	}

	_, name2, err := r.CreateTemp("sub")
	must(t, err)
	if name2 == name {
		t.Errorf("two temp files named %q", name)
	}
	_, top, err := r.CreateTemp(".")
	must(t, err)
	if !strings.HasPrefix(top, TempPrefix) {
		t.Errorf("name %q", top)
	}
}

func TestMkdirMode(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	r, dir := newRoot(t, nil)
	must(t, r.Mkdir("s", DirMode))
	must(t, OS{}.MkdirAll(filepath.Join(dir, "a", "b"), DirMode))
	for _, p := range []string{"s", "a", "a/b"} {
		fi, err := os.Lstat(filepath.Join(dir, p))
		must(t, err)
		if got := fi.Mode().Perm(); got != 0o700 {
			t.Errorf("%s: mode %v, want 0700", p, got)
		}
	}
	wantErrno(t, r.Mkdir("s", DirMode), syscall.EEXIST)
}

func TestRenameReplaces(t *testing.T) {
	r, _ := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "tmp"), []byte("new"), 0o600))
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("old"), 0o600))
	})
	must(t, r.Rename("tmp", "1.json"))
	got, _ := r.ReadFile("1.json")
	if string(got) != "new" {
		t.Errorf("1.json is %q", got)
	}
	_, err := r.Stat("tmp")
	wantErrno(t, err, syscall.ENOENT)
}

func TestOpenRootErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := OS{}.OpenRoot(filepath.Join(dir, "missing"))
	wantErrno(t, err, syscall.ENOENT)
	file := filepath.Join(dir, "file")
	must(t, os.WriteFile(file, nil, 0o600))
	_, err = OS{}.OpenRoot(file)
	wantErrno(t, err, syscall.ENOTDIR)
	var pe *os.PathError
	if !errors.As(err, &pe) || pe.Path != file {
		t.Errorf("got %v, want a *os.PathError on %q", err, file)
	}
	_, err = OS{}.OpenRoot("")
	wantErrno(t, err, syscall.ENOENT)

	r, _ := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "f"), nil, 0o600))
	})
	_, err = r.OpenRoot("f")
	wantErrno(t, err, syscall.ENOTDIR)
	if !errors.As(err, &pe) || pe.Path != "f" {
		t.Errorf("got %v, want a *os.PathError on f", err)
	}
	_, err = r.OpenRoot("missing")
	wantErrno(t, err, syscall.ENOENT)
}

// Name is the path the root was opened with, not the path handed to
// os.OpenRoot; a root opened through another is named by the joined path.
func TestOpenRootName(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "s"), 0o700))
	})
	if got := r.Name(); got != dir {
		t.Errorf("Name() = %q, want %q", got, dir)
	}
	sub, err := r.OpenRoot("s")
	must(t, err)
	defer sub.Close()
	if got, want := sub.Name(), filepath.Join(dir, "s"); got != want {
		t.Errorf("sub Name() = %q, want %q", got, want)
	}
}

// A root reached through a symlink opens the symlink's target.
func TestOpenRootFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "real"), 0o700))
	must(t, os.WriteFile(filepath.Join(dir, "real", "x"), []byte("x"), 0o600))
	link := filepath.Join(dir, "link")
	must(t, os.Symlink("real", link))
	r, err := OS{}.OpenRoot(link)
	must(t, err)
	defer r.Close()
	got, err := r.ReadFile("x")
	must(t, err)
	if string(got) != "x" {
		t.Errorf("got %q", got)
	}
}

func TestFSReadFileFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "real"), []byte("x"), 0o600))
	must(t, os.Symlink("real", filepath.Join(dir, "hooks.properties")))
	got, err := OS{}.ReadFile(filepath.Join(dir, "hooks.properties"))
	must(t, err)
	if string(got) != "x" {
		t.Errorf("got %q", got)
	}
	_, err = OS{}.ReadFile(filepath.Join(dir, "missing"))
	wantErrno(t, err, syscall.ENOENT)
}

// RemoveAll removes a directory with everything under it, and never follows
// a symlink out of it.
func TestRemoveAll(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.MkdirAll(filepath.Join(dir, "a/b"), 0o700))
		must(t, os.WriteFile(filepath.Join(dir, "a/b/1.json"), nil, 0o600))
		must(t, os.Mkdir(filepath.Join(dir, "keep"), 0o700))
		must(t, os.WriteFile(filepath.Join(dir, "keep/x"), nil, 0o600))
		must(t, os.Symlink("../keep", filepath.Join(dir, "a/link")))
	})
	must(t, r.RemoveAll("a"))
	must(t, r.RemoveAll("a"))
	if _, err := os.Lstat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Errorf("a: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep/x")); err != nil {
		t.Errorf("followed a symlink: %v", err)
	}
}

func TestOpenAppend(t *testing.T) {
	r, dir := newRoot(t, nil)
	for _, umask := range []int{0, 0o022, 0o277} {
		name := fmt.Sprintf("log%03o", umask)
		old := syscall.Umask(umask)
		a, err := r.OpenAppend(name)
		syscall.Umask(old)
		must(t, err)
		_, err = a.Write([]byte("one\n"))
		must(t, err)
		must(t, a.Close())
		fi, err := os.Lstat(filepath.Join(dir, name))
		must(t, err)
		if got := fi.Mode(); got != 0o600 {
			t.Errorf("umask %03o: mode %v, want 0600", umask, got)
		}
	}
	// An existing file is appended to, and keeps its mode.
	must(t, os.WriteFile(filepath.Join(dir, "x"), []byte("zero\n"), 0o640))
	must(t, os.Chmod(filepath.Join(dir, "x"), 0o640))
	a, err := r.OpenAppend("x")
	must(t, err)
	_, err = a.Write([]byte("one\n"))
	must(t, err)
	fi, err := a.Stat()
	must(t, err)
	must(t, a.Close())
	got, err := os.ReadFile(filepath.Join(dir, "x"))
	must(t, err)
	if string(got) != "zero\none\n" || fi.Size() != 9 || fi.Mode() != 0o640 {
		t.Errorf("got %q, size %d, mode %v", got, fi.Size(), fi.Mode())
	}
}

func TestOpenAppendFIFODoesNotBlock(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, syscall.Mkfifo(filepath.Join(dir, "log"), 0o600))
	})
	err := within(t, "OpenAppend on a FIFO", func() error {
		a, err := r.OpenAppend("log")
		if err == nil {
			a.Close()
		}
		return err
	})
	wantErrno(t, err, syscall.ENXIO)

	// A reader that never reads: the open succeeds, and a write past the
	// pipe's capacity fails rather than blocking.
	rd, err := os.OpenFile(filepath.Join(dir, "log"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	must(t, err)
	defer rd.Close()
	a, err := r.OpenAppend("log")
	must(t, err)
	defer a.Close()
	err = within(t, "a write to a full FIFO", func() error {
		_, err := a.Write(make([]byte, 1<<20)) // fills the pipe: a short write
		if !errors.Is(err, io.ErrShortWrite) {
			return fmt.Errorf("first write: %v, want a short write", err)
		}
		_, err = a.Write([]byte("x"))
		return err
	})
	wantErrno(t, err, syscall.EAGAIN)
}

func TestOpenAppendDirectory(t *testing.T) {
	r, _ := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "log"), 0o700))
	})
	_, err := r.OpenAppend("log")
	wantErrno(t, err, syscall.EISDIR)
}

// TryLock is per descriptor: a second descriptor on the same file finds it
// held until the first closes.
func TestAppendTryLock(t *testing.T) {
	r, _ := newRoot(t, nil)
	a, err := r.OpenAppend("log")
	must(t, err)
	b, err := r.OpenAppend("log")
	must(t, err)
	defer b.Close()
	must(t, a.TryLock())
	wantErrno(t, b.TryLock(), syscall.EAGAIN)
	must(t, a.Close())
	must(t, b.TryLock())
}

// A file past MaxRead fails with EFBIG, through a root and outside one.
func TestReadFileTooBig(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		f, err := os.Create(filepath.Join(dir, "big"))
		must(t, err)
		must(t, f.Truncate(MaxRead+1))
		must(t, f.Close())
		must(t, os.WriteFile(filepath.Join(dir, "max"), make([]byte, MaxRead), 0o600))
	})
	_, err := r.ReadFile("big")
	wantErrno(t, err, syscall.EFBIG)
	_, err = OS{}.ReadFile(filepath.Join(dir, "big"))
	wantErrno(t, err, syscall.EFBIG)
	b, err := r.ReadFile("max")
	must(t, err)
	if len(b) != MaxRead {
		t.Errorf("read %d bytes, want %d", len(b), MaxRead)
	}
}

// OpenRootCreate opens a directory, creating it and its missing parents, mode
// DirMode, only when it isn't there; any other failure is the open's own.
func TestOpenRootCreate(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "a", "b")
	r, err := OpenRootCreate(OS{}, dir)
	must(t, err)
	r.Close()
	fi, err := os.Stat(dir)
	must(t, err)
	if fi.Mode().Perm() != DirMode&^umask(t) {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	r, err = OpenRootCreate(OS{}, dir) // already there: opened, not recreated
	must(t, err)
	r.Close()

	file := filepath.Join(base, "file")
	must(t, os.WriteFile(file, nil, 0o600))
	if _, err := OpenRootCreate(OS{}, file); !errnoIs(err, syscall.ENOTDIR) {
		t.Errorf("a file: %v", err)
	}
	if _, err := OpenRootCreate(OS{}, filepath.Join(file, "x")); !errnoIs(err, syscall.ENOTDIR) {
		t.Errorf("under a file: %v", err)
	}
}

func umask(t *testing.T) os.FileMode {
	t.Helper()
	old := syscall.Umask(0)
	syscall.Umask(old)
	return os.FileMode(old)
}
