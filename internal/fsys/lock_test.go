package fsys

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestMain runs the test binary as a child process when FSYS_CHILD names
// what it is to do: the lock tests between processes re-execute it.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FSYS_CHILD"); mode != "" {
		if err := runChild(mode, os.Getenv("FSYS_DIR")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runChild(mode, dir string) error {
	switch mode {
	case "increment":
		n, err := strconv.Atoi(os.Getenv("FSYS_N"))
		if err != nil {
			return err
		}
		for range n {
			if err := increment(dir); err != nil {
				return err
			}
		}
		return nil
	case "hold":
		// Take the lock, say so, and hold it until stdin closes.
		r, err := OS{}.OpenRoot(dir)
		if err != nil {
			return err
		}
		l, err := r.Lock(0)
		if err != nil {
			return err
		}
		fmt.Println("locked")
		io.Copy(io.Discard, os.Stdin)
		return l.Unlock()
	}
	return fmt.Errorf("unknown child mode %q", mode)
}

// increment is one hook's read-modify-write of a counter, under the lock.
func increment(dir string) error {
	r, err := OS{}.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	l, err := r.Lock(10 * time.Second)
	if err != nil {
		return err
	}
	defer l.Unlock()
	n := 0
	b, err := r.ReadFile("n")
	if err == nil {
		if n, err = strconv.Atoi(string(b)); err != nil {
			return err
		}
	} else if !errnoIs(err, syscall.ENOENT) {
		return err
	}
	err = Publish(r, "n", []byte(strconv.Itoa(n+1)))
	return err
}

func child(t *testing.T, mode, dir string, env ...string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	must(t, err)
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), append([]string{"FSYS_CHILD=" + mode, "FSYS_DIR=" + dir}, env...)...)
	cmd.Stderr = os.Stderr
	return cmd
}

// hold starts a child holding dir's lock, and returns once it is held. The
// lock is released when the returned function is called, or at cleanup.
func hold(t *testing.T, dir string) (release func()) {
	t.Helper()
	cmd := child(t, "hold", dir)
	stdin, err := cmd.StdinPipe()
	must(t, err)
	stdout, err := cmd.StdoutPipe()
	must(t, err)
	must(t, cmd.Start())
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("holder said %q, %v", line, err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			stdin.Close()
			if err := cmd.Wait(); err != nil {
				t.Errorf("holder: %v", err)
			}
		})
	}
	t.Cleanup(release)
	return release
}

// 8 processes × 100 increments under the lock lose no update
// (implementation-spec.md, Performance gate).
func TestLockNoLostUpdates(t *testing.T) {
	const writers, each = 8, 100
	dir := t.TempDir()
	var cmds []*exec.Cmd
	for range writers {
		cmd := child(t, "increment", dir, "FSYS_N="+strconv.Itoa(each))
		must(t, cmd.Start())
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		must(t, cmd.Wait())
	}
	b, err := os.ReadFile(filepath.Join(dir, "n"))
	must(t, err)
	if got := string(b); got != strconv.Itoa(writers*each) {
		t.Errorf("counter %s, want %d", got, writers*each)
	}
	es, err := os.ReadDir(dir)
	must(t, err)
	if len(es) != 1 {
		t.Errorf("directory holds %v, want only n", es)
	}
}

// A lock held by another process makes a bounded wait end at its deadline,
// with EAGAIN: not before, and not long after.
func TestLockWaitEndsAtDeadline(t *testing.T) {
	r, dir := newRoot(t, nil)
	hold(t, dir)
	const wait = 300 * time.Millisecond
	start := time.Now()
	_, err := r.Lock(wait)
	took := time.Since(start)
	wantErrno(t, err, syscall.EAGAIN)
	if took < wait || took > wait+100*time.Millisecond {
		t.Errorf("gave up after %v, want %v", took, wait)
	}
}

// The try-lock, wait 0, fails at once.
func TestLockTryFailsAtOnce(t *testing.T) {
	r, dir := newRoot(t, nil)
	hold(t, dir)
	start := time.Now()
	_, err := r.Lock(0)
	took := time.Since(start)
	wantErrno(t, err, syscall.EAGAIN)
	if took > 20*time.Millisecond {
		t.Errorf("try-lock took %v", took)
	}
}

// A wait ends soon after the holder releases the lock: within one poll at
// most.
func TestLockWaitGetsReleasedLock(t *testing.T) {
	r, dir := newRoot(t, nil)
	release := hold(t, dir)
	const after = 100 * time.Millisecond
	time.AfterFunc(after, release)
	start := time.Now()
	l, err := r.Lock(5 * time.Second)
	took := time.Since(start)
	must(t, err)
	must(t, l.Unlock())
	if took < after || took > after+lockMax+50*time.Millisecond {
		t.Errorf("got the lock after %v, want about %v", took, after)
	}
}

func TestLockContention(t *testing.T) {
	r1, dir := newRoot(t, nil)
	r2, err := OS{}.OpenRoot(dir)
	must(t, err)
	defer r2.Close()

	l, err := r1.Lock(0)
	must(t, err)
	_, err = r2.Lock(0)
	wantErrno(t, err, syscall.EAGAIN)
	// A second lock through the same root is a second open file
	// description, so it contends too.
	_, err = r1.Lock(0)
	wantErrno(t, err, syscall.EAGAIN)

	must(t, l.Unlock())
	l2, err := r2.Lock(0)
	must(t, err)
	must(t, l2.Unlock())
}

// The lock is on the directory, not a path: a root reached through a
// symlink, or opened through another root, contends on the same lock.
func TestLockThroughOtherPaths(t *testing.T) {
	r1, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "s"), 0o700))
	})
	s1, err := r1.OpenRoot("s")
	must(t, err)
	defer s1.Close()
	link := filepath.Join(t.TempDir(), "s")
	must(t, os.Symlink(filepath.Join(dir, "s"), link))
	s2, err := OS{}.OpenRoot(link)
	must(t, err)
	defer s2.Close()

	l, err := s1.Lock(0)
	must(t, err)
	defer l.Unlock()
	_, err = s2.Lock(0)
	wantErrno(t, err, syscall.EAGAIN)
}

// A held lock survives garbage collection: the Lock keeps its descriptor
// referenced, so no finalizer closes it. Finalizers run on their own
// goroutine after a collection, so each collection is followed by a pause
// that lets them run.
func TestLockSurvivesGC(t *testing.T) {
	r1, dir := newRoot(t, nil)
	r2, err := OS{}.OpenRoot(dir)
	must(t, err)
	defer r2.Close()

	l, err := r1.Lock(0)
	must(t, err)
	for range 10 {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	_, err = r2.Lock(0)
	wantErrno(t, err, syscall.EAGAIN)
	runtime.KeepAlive(l)
	must(t, l.Unlock())
}

// EINTR is retried, injected through the flock variable: for the try-lock,
// and for each poll of a wait.
func TestLockRetriesEINTR(t *testing.T) {
	r, _ := newRoot(t, nil)
	calls := 0
	flock = func(fd, how int) error {
		calls++
		if how != syscall.LOCK_EX|syscall.LOCK_NB {
			t.Errorf("flock how %d, want LOCK_EX|LOCK_NB", how)
		}
		if calls <= 2 {
			return syscall.EINTR
		}
		return syscall.Flock(fd, how)
	}
	t.Cleanup(func() { flock = syscall.Flock })

	l, err := r.Lock(0)
	must(t, err)
	must(t, l.Unlock())
	if calls != 3 {
		t.Errorf("flock called %d times, want 3", calls)
	}

	// EAGAIN, EINTR, EAGAIN, then success: the wait polls through EINTR.
	calls = 0
	flock = func(fd, how int) error {
		calls++
		switch calls {
		case 1, 3:
			return syscall.EAGAIN
		case 2:
			return syscall.EINTR
		}
		return syscall.Flock(fd, how)
	}
	l, err = r.Lock(time.Second)
	must(t, err)
	must(t, l.Unlock())
	if calls != 4 {
		t.Errorf("flock called %d times, want 4", calls)
	}
}

// An errno other than EAGAIN ends a wait at once.
func TestLockOtherErrnoEndsWait(t *testing.T) {
	r, _ := newRoot(t, nil)
	flock = func(fd, how int) error { return syscall.ENOLCK }
	t.Cleanup(func() { flock = syscall.Flock })
	start := time.Now()
	_, err := r.Lock(5 * time.Second)
	wantErrno(t, err, syscall.ENOLCK)
	if took := time.Since(start); took > time.Second {
		t.Errorf("took %v", took)
	}
}

// Moved catches a directory renamed aside after it was opened, as prune
// does, and one replaced or removed; a directory left alone, or reached
// through a symlink, has not moved (implementation-spec.md, Roots).
func TestMoved(t *testing.T) {
	parent := t.TempDir()
	open := func(t *testing.T, name string) (Root, string) {
		t.Helper()
		p := filepath.Join(parent, name)
		must(t, os.Mkdir(p, 0o700))
		r, err := OS{}.OpenRoot(p)
		must(t, err)
		t.Cleanup(func() { r.Close() })
		return r, p
	}
	moved := func(t *testing.T, r Root, want bool) {
		t.Helper()
		l, err := r.Lock(0)
		must(t, err)
		defer l.Unlock()
		got, err := r.Moved()
		must(t, err)
		if got != want {
			t.Errorf("Moved() = %v, want %v", got, want)
		}
	}

	t.Run("untouched", func(t *testing.T) {
		r, _ := open(t, "a")
		moved(t, r, false)
	})
	t.Run("renamed aside", func(t *testing.T) {
		r, p := open(t, "b")
		must(t, os.Rename(p, filepath.Join(parent, TempPrefix+"b")))
		moved(t, r, true)
	})
	t.Run("renamed aside and recreated", func(t *testing.T) {
		r, p := open(t, "c")
		must(t, os.Rename(p, filepath.Join(parent, TempPrefix+"c")))
		must(t, os.Mkdir(p, 0o700))
		moved(t, r, true)
	})
	t.Run("removed", func(t *testing.T) {
		r, p := open(t, "d")
		must(t, os.Remove(p))
		moved(t, r, true)
	})
	t.Run("replaced by a file", func(t *testing.T) {
		r, p := open(t, "e")
		must(t, os.Remove(p))
		must(t, os.WriteFile(p, nil, 0o600))
		moved(t, r, true)
	})
	t.Run("through a symlink", func(t *testing.T) {
		_, p := open(t, "f")
		link := filepath.Join(parent, "flink")
		must(t, os.Symlink(p, link))
		r, err := OS{}.OpenRoot(link)
		must(t, err)
		defer r.Close()
		moved(t, r, false)
	})
	t.Run("opened through a root", func(t *testing.T) {
		_, p := open(t, "g")
		top, err := OS{}.OpenRoot(parent)
		must(t, err)
		defer top.Close()
		r, err := top.OpenRoot("g")
		must(t, err)
		defer r.Close()
		moved(t, r, false)
		must(t, os.Rename(p, filepath.Join(parent, TempPrefix+"g")))
		moved(t, r, true)
	})
}
