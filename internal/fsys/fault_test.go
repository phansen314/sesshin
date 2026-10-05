package fsys

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFaultPassesThrough(t *testing.T) {
	dir := t.TempDir()
	var ops []Op
	fsys := Fault{FS: OS{}, Hook: Record(&ops)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	f, name, err := r.CreateTemp(".")
	must(t, err)
	_, err = f.Write([]byte("x"))
	must(t, err)
	must(t, f.Sync())
	must(t, f.Close())
	must(t, r.Rename(name, "1.json"))
	must(t, r.SyncDir("."))
	got, err := r.ReadFile("1.json")
	must(t, err)
	if string(got) != "x" {
		t.Errorf("got %q", got)
	}

	want := []Op{
		{Name: OpOpenRoot, Path: dir},
		{Root: dir, Name: OpCreateTemp, Path: ".", Mutating: true},
		{Root: dir, Name: OpWrite, Path: name, Mutating: true},
		{Root: dir, Name: OpSyncFile, Path: name},
		{Root: dir, Name: OpCloseFile, Path: name},
		{Root: dir, Name: OpRename, Path: name, NewPath: "1.json", Mutating: true},
		{Root: dir, Name: OpSyncDir, Path: "."},
		{Root: dir, Name: OpReadFile, Path: "1.json"},
	}
	if len(ops) != len(want) {
		t.Fatalf("got %d ops %+v, want %d", len(ops), ops, len(want))
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("op %d: got %+v, want %+v", i, ops[i], want[i])
		}
	}
}

// Roots opened through a Fault root are Fault roots too, and Moved and Lock
// are intercepted.
func TestFaultSubRoot(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "s"), 0o700))
	var ops []Op
	r, err := Fault{FS: OS{}, Hook: Record(&ops)}.OpenRoot(dir)
	must(t, err)
	defer r.Close()
	sub, err := r.OpenRoot("s")
	must(t, err)
	l, err := sub.Lock(0)
	must(t, err)
	moved, err := sub.Moved()
	must(t, err)
	if moved {
		t.Error("moved")
	}
	must(t, l.Unlock())
	must(t, sub.Close())
	s := filepath.Join(dir, "s")
	want := []Op{
		{Name: OpOpenRoot, Path: dir},
		{Root: dir, Name: OpOpenRoot, Path: "s"},
		{Root: s, Name: OpLock, Path: "."},
		{Root: s, Name: OpMoved, Path: "."},
		{Root: s, Name: OpUnlock, Path: "."},
		{Root: s, Name: OpCloseRoot, Path: "."},
	}
	if len(ops) != len(want) {
		t.Fatalf("got ops %+v, want %+v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("op %d: got %+v, want %+v", i, ops[i], want[i])
		}
	}
}

// A Fault without a Hook passes every call through.
func TestFaultNilHook(t *testing.T) {
	r, err := Fault{FS: OS{}}.OpenRoot(t.TempDir())
	must(t, err)
	must(t, r.Mkdir("proj", DirMode))
	must(t, r.Close())
}

func TestErrnoAt(t *testing.T) {
	dir := t.TempDir()
	fsys := Fault{FS: OS{}, Hook: ErrnoAt(OpWrite, "", 2, syscall.ENOSPC)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	f, name, err := r.CreateTemp(".")
	must(t, err)
	_, err = f.Write([]byte("a"))
	must(t, err)
	_, err = f.Write([]byte("b"))
	wantErrno(t, err, syscall.ENOSPC)
	if n, ok := ErrnoOf(err); !ok || n != syscall.ENOSPC {
		t.Errorf("ErrnoOf gave %v, %v", n, ok)
	}
	_, err = f.Write([]byte("c"))
	must(t, err)
	must(t, f.Close())
	got, _ := os.ReadFile(filepath.Join(dir, name))
	if string(got) != "ac" {
		t.Errorf("file holds %q; the failed write must not happen", got)
	}
}

// ErrnoAtMutating counts only calls that change the disk: the reads between
// them do not move it on.
func TestErrnoAtMutating(t *testing.T) {
	dir := t.TempDir()
	fsys := Fault{FS: OS{}, Hook: ErrnoAtMutating(2, syscall.EIO)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	must(t, r.Mkdir("a", DirMode))
	_, err = r.ReadDir(".")
	must(t, err)
	wantErrno(t, r.Mkdir("b", DirMode), syscall.EIO)
	must(t, r.Mkdir("c", DirMode))
	if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
		t.Errorf("b: %v; the failed mkdir must not happen", err)
	}
}

func TestErrnoAtPath(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "1.json"), nil, FileMode))
	must(t, os.WriteFile(filepath.Join(dir, "2.json"), nil, FileMode))
	fsys := Fault{FS: OS{}, Hook: ErrnoAt(OpReadFile, "2.json", 1, syscall.EACCES)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	_, err = r.ReadFile("1.json")
	must(t, err)
	_, err = r.ReadFile("2.json")
	wantErrno(t, err, syscall.EACCES)
	var pe *os.PathError
	if !errors.As(err, &pe) || pe.Path != "2.json" {
		t.Errorf("got %#v, want *os.PathError on 2.json", err)
	}
}

// An injected unlock fault still releases the real lock.
func TestFaultUnlockReleases(t *testing.T) {
	dir := t.TempDir()
	fsys := Fault{FS: OS{}, Hook: ErrnoAt(OpUnlock, "", 1, syscall.EIO)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	l, err := r.Lock(0)
	must(t, err)
	wantErrno(t, l.Unlock(), syscall.EIO)
	l, err = r.Lock(0)
	must(t, err)
	must(t, l.Unlock())
}

// An injected close fault still closes the real root: the inner Fault sees
// the close.
func TestFaultCloseRootCloses(t *testing.T) {
	var inner []Op
	fsys := Fault{
		FS:   Fault{FS: OS{}, Hook: Record(&inner)},
		Hook: ErrnoAt(OpCloseRoot, "", 1, syscall.EIO),
	}
	r, err := fsys.OpenRoot(t.TempDir())
	must(t, err)
	wantErrno(t, r.Close(), syscall.EIO)
	if last := inner[len(inner)-1]; last.Name != OpCloseRoot {
		t.Errorf("inner calls %+v, want the real root closed", inner)
	}
}

func TestHooksFirstErrorWins(t *testing.T) {
	h := Hooks(
		ErrnoAt(OpRemove, "", 1, syscall.EACCES),
		ErrnoAt(OpRemove, "", 1, syscall.EIO),
	)
	if err := h(Op{Name: OpRemove}); err != syscall.EACCES {
		t.Errorf("got %v", err)
	}
}

// CrashBefore kills the process, so it runs in a child: the test binary
// re-executed with FSYS_CRASH_DIR set.
func TestCrashBefore(t *testing.T) {
	if dir := os.Getenv("FSYS_CRASH_DIR"); dir != "" {
		fsys := Fault{FS: OS{}, Hook: CrashBefore(2)}
		r, _ := fsys.OpenRoot(dir)
		r.Mkdir("a", DirMode)
		r.Mkdir("b", DirMode)
		os.Exit(0)
	}
	dir := t.TempDir()
	exe, err := os.Executable()
	must(t, err)
	p, err := os.StartProcess(exe, []string{exe, "-test.run=^TestCrashBefore$"}, &os.ProcAttr{
		Env:   append(os.Environ(), "FSYS_CRASH_DIR="+dir),
		Files: []*os.File{nil, nil, os.Stderr},
	})
	must(t, err)
	st, err := p.Wait()
	must(t, err)
	ws := st.Sys().(syscall.WaitStatus)
	if !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("child ended %v, want killed by SIGKILL", st)
	}
	if _, err := os.Lstat(filepath.Join(dir, "a")); err != nil {
		t.Errorf("step 1 did not happen: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "b")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("step 2 happened: %v", err)
	}
}

// Readlink passes through a Fault, which can fail it.
func TestFaultReadlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "l")
	must(t, os.Symlink("target/x", link))
	var ops []Op
	got, err := Fault{FS: OS{}, Hook: Record(&ops)}.Readlink(link)
	if err != nil || got != "target/x" {
		t.Errorf("Readlink = %q, %v", got, err)
	}
	if want := (Op{Name: OpReadlink, Path: link}); len(ops) != 1 || ops[0] != want {
		t.Errorf("ops %+v, want %+v", ops, want)
	}
	_, err = Fault{FS: OS{}, Hook: ErrnoAt(OpReadlink, link, 1, syscall.EACCES)}.Readlink(link)
	if !errors.Is(err, syscall.EACCES) {
		t.Errorf("injected: %v", err)
	}
}
