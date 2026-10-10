package fsys

import (
	"os"
	"path/filepath"
	"testing"
)

// Lstat does not follow a symlink in the last component, on the disk and in a
// root, and a fault hook sees it as a stat.
func TestLstat(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("f", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	st, err := OS{}.Lstat(filepath.Join(dir, "l"))
	if err != nil || st.Mode().IsRegular() {
		t.Errorf("OS.Lstat of a symlink: %v, %v", st, err)
	}
	if st, err := (OS{}).Stat(filepath.Join(dir, "l")); err != nil || !st.Mode().IsRegular() {
		t.Errorf("OS.Stat of a symlink: %v, %v", st, err)
	}
	root, err := OS{}.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if st, err := root.Lstat("l"); err != nil || st.Mode().IsRegular() {
		t.Errorf("root.Lstat of a symlink: %v, %v", st, err)
	}
	if st, err := root.Lstat("f"); err != nil || !st.Mode().IsRegular() {
		t.Errorf("root.Lstat of a file: %v, %v", st, err)
	}
	var seen []string
	f := Fault{FS: OS{}, Hook: func(op Op) error { seen = append(seen, op.Name); return nil }}
	if _, err := f.Lstat(dir); err != nil {
		t.Fatal(err)
	}
	r, err := f.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Lstat("l"); err != nil {
		t.Fatal(err)
	}
	if len(seen) < 3 {
		t.Errorf("hook saw %v", seen)
	}
}
