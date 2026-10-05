package fsys

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// leftovers lists the temp files and directories in dir.
func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	must(t, err)
	var names []string
	for _, e := range es {
		if strings.HasPrefix(e.Name(), TempPrefix) {
			names = append(names, e.Name())
		}
	}
	return names
}

func wantFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	must(t, err)
	if string(got) != want {
		t.Errorf("%s holds %q, want %q", filepath.Base(path), got, want)
	}
}

func TestPublish(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "s"), 0o700))
	})
	for _, synced := range []bool{false, true} {
		pub := Publish
		if synced {
			pub = PublishSynced
		}
		must(t, pub(r, "s/new.json", []byte("one")))
		wantFile(t, filepath.Join(dir, "s/new.json"), "one")
		must(t, pub(r, "s/new.json", []byte("two")))
		wantFile(t, filepath.Join(dir, "s/new.json"), "two")
		if l := leftovers(t, filepath.Join(dir, "s")); l != nil {
			t.Errorf("synced %v: leftovers %v", synced, l)
		}
		must(t, os.Remove(filepath.Join(dir, "s/new.json")))
	}
}

// Only PublishSynced flushes: the file before publishing, then its
// directory. Publish makes no flush at all.
func TestPublishFlushesOnlyWhenSynced(t *testing.T) {
	for _, tc := range []struct {
		synced bool
		want   []string
	}{
		{false, []string{OpCreateTemp, OpWrite, OpCloseFile, OpRename}},
		{true, []string{OpCreateTemp, OpWrite, OpSyncFile, OpCloseFile, OpRename, OpSyncDir}},
	} {
		var ops []Op
		r, err := Fault{FS: OS{}, Hook: Record(&ops)}.OpenRoot(t.TempDir())
		must(t, err)
		pub := Publish
		if tc.synced {
			pub = PublishSynced
		}
		must(t, pub(r, "state.json", []byte("{}")))
		must(t, r.Close())
		var got []string
		for _, op := range ops[1 : len(ops)-1] { // without openroot and closeroot
			got = append(got, op.Name)
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("synced %v: ops %v, want %v", tc.synced, got, tc.want)
		}
	}
}

// A write that fails at any step before publishing leaves the old file
// intact and no temp file behind.
func TestPublishFailureLeavesOldFile(t *testing.T) {
	for _, tc := range []struct {
		op     string
		synced bool
	}{
		{OpCreateTemp, false},
		{OpWrite, false},
		{OpCloseFile, false},
		{OpRename, false},
		{OpSyncFile, true},
	} {
		dir := t.TempDir()
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("old"), 0o600))
		r, err := Fault{FS: OS{}, Hook: ErrnoAt(tc.op, "", 1, syscall.ENOSPC)}.OpenRoot(dir)
		must(t, err)
		pub := Publish
		if tc.synced {
			pub = PublishSynced
		}
		err = pub(r, "1.json", []byte("new"))
		wantErrno(t, err, syscall.ENOSPC)
		must(t, r.Close())
		wantFile(t, filepath.Join(dir, "1.json"), "old")
		if l := leftovers(t, dir); l != nil {
			t.Errorf("%s: leftovers %v", tc.op, l)
		}
	}
}

// Once published, a failed directory flush is not an error.
func TestPublishAfterPublishingSucceeds(t *testing.T) {
	dir := t.TempDir()
	r, err := Fault{FS: OS{}, Hook: ErrnoAt(OpSyncDir, "", 1, syscall.EIO)}.OpenRoot(dir)
	must(t, err)
	must(t, PublishSynced(r, "1.json", []byte("new")))
	must(t, r.Close())
	wantFile(t, filepath.Join(dir, "1.json"), "new")
}

// The statusline's two-step write: Commit publishes, Abort leaves the old
// file, and neither leaves a temp file.
func TestPrepared(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "statusline.json"), []byte("old"), 0o600))
	})
	path := filepath.Join(dir, "statusline.json")

	p, err := Prepare(r, "statusline.json", []byte("aborted"))
	must(t, err)
	if l := leftovers(t, dir); len(l) != 1 {
		t.Errorf("prepared: temp files %v, want one", l)
	}
	wantFile(t, path, "old")
	p.Abort()
	wantFile(t, path, "old")
	if l := leftovers(t, dir); l != nil {
		t.Errorf("aborted: leftovers %v", l)
	}

	p, err = Prepare(r, "statusline.json", []byte("committed"))
	must(t, err)
	must(t, p.Commit())
	wantFile(t, path, "committed")
	if l := leftovers(t, dir); l != nil {
		t.Errorf("committed: leftovers %v", l)
	}
	fi, err := os.Stat(path)
	must(t, err)
	if fi.Mode() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode())
	}
}

// A failed Commit removes the temp file and leaves the old file.
func TestPreparedCommitFailure(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "statusline.json"), []byte("old"), 0o600))
	r, err := Fault{FS: OS{}, Hook: ErrnoAt(OpRename, "", 1, syscall.EIO)}.OpenRoot(dir)
	must(t, err)
	defer r.Close()
	p, err := Prepare(r, "statusline.json", []byte("new"))
	must(t, err)
	wantErrno(t, p.Commit(), syscall.EIO)
	wantFile(t, filepath.Join(dir, "statusline.json"), "old")
	if l := leftovers(t, dir); l != nil {
		t.Errorf("leftovers %v", l)
	}
}

// RemoveAside renames the directory to a temp name, then removes it; a
// failed removal leaves a hidden leftover and is not an error.
func TestRemoveAside(t *testing.T) {
	setup := func(dir string) {
		must(t, os.MkdirAll(filepath.Join(dir, "sessions/a/sub"), 0o700))
		must(t, os.WriteFile(filepath.Join(dir, "sessions/a/lifecycle.json"), nil, 0o600))
	}

	r, dir := newRoot(t, setup)
	must(t, RemoveAside(r, "sessions/a"))
	es, err := os.ReadDir(filepath.Join(dir, "sessions"))
	must(t, err)
	if len(es) != 0 {
		t.Errorf("sessions holds %v", es)
	}

	dir = t.TempDir()
	setup(dir)
	var ops []Op
	r, err = Fault{FS: OS{}, Hook: Hooks(Record(&ops), ErrnoAt(OpRemoveAll, "", 1, syscall.EIO))}.OpenRoot(dir)
	must(t, err)
	defer r.Close()
	must(t, RemoveAside(r, "sessions/a"))
	l := leftovers(t, filepath.Join(dir, "sessions"))
	if len(l) != 1 {
		t.Fatalf("leftovers %v, want the directory renamed aside", l)
	}
	if ops[1].Name != OpRename || ops[1].NewPath != "sessions/"+l[0] {
		t.Errorf("ops %+v", ops)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", l[0], "lifecycle.json")); err != nil {
		t.Error(err)
	}

	// A failed rename changes nothing.
	r2, err := Fault{FS: OS{}, Hook: ErrnoAt(OpRename, "", 1, syscall.EACCES)}.OpenRoot(dir)
	must(t, err)
	defer r2.Close()
	must(t, os.Mkdir(filepath.Join(dir, "sessions/b"), 0o700))
	wantErrno(t, RemoveAside(r2, "sessions/b"), syscall.EACCES)
	if _, err := os.Stat(filepath.Join(dir, "sessions/b")); err != nil {
		t.Error(err)
	}
}
