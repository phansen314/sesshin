package iterm2

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTakeSymlinkAndDirectory(t *testing.T) {
	t.Run("symlinked file", func(t *testing.T) {
		s, path := written(t)
		real := path + ".real"
		if err := os.Rename(path, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, path); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Take(nonce); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("err %v", err)
		}
		if _, err := os.Stat(real); err != nil {
			t.Errorf("the target was touched: %v", err)
		}
	})
	t.Run("symlinked directory", func(t *testing.T) {
		s, path := written(t)
		dir := filepath.Dir(path)
		moved := dir + "-real"
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, dir); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Take(nonce); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("err %v", err)
		}
		if _, err := os.Stat(filepath.Join(moved, nonce+".json")); err != nil {
			t.Errorf("the file was touched: %v", err)
		}
	})
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777} {
		t.Run("loose directory "+mode.String(), func(t *testing.T) {
			s, path := written(t)
			if err := os.Chmod(filepath.Dir(path), mode); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Take(nonce); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
				t.Errorf("err %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("the file was removed: %v", err)
			}
		})
	}
	t.Run("someone else's directory", func(t *testing.T) {
		s, _ := written(t)
		s.Getuid = func() int { return os.Getuid() + 1 }
		if _, err := s.Take(nonce); err == nil || !strings.Contains(err.Error(), "own") {
			t.Errorf("err %v", err)
		}
	})
}

// A modification time far in the future is as old as one far in the past, for
// Take and for Prune.
func TestFutureModificationTime(t *testing.T) {
	s, path := written(t)
	future := launchNow.Add(121 * time.Second)
	os.Chtimes(path, future, future)
	if n, err := s.Prune(true); n != 1 || err != nil {
		t.Errorf("prune: %d, %v", n, err)
	}
	if _, err := s.Take(nonce); err == nil || !strings.Contains(err.Error(), "120 seconds") {
		t.Errorf("take: %v", err)
	}
	s, path = written(t)
	soon := launchNow.Add(60 * time.Second)
	os.Chtimes(path, soon, soon)
	if _, err := s.Take(nonce); err != nil {
		t.Errorf("a clock a minute off: %v", err)
	}
}

// Write refuses what launch-exec would, and invalid UTF-8, which encoding/json
// would replace.
func TestWriteValidates(t *testing.T) {
	good := LaunchFile{Cwd: "/w", Env: map[string]string{"A": "1"}, Argv: []string{"x"}}
	for name, mod := range map[string]func(*LaunchFile){
		"relative cwd":  func(f *LaunchFile) { f.Cwd = "w" },
		"empty cwd":     func(f *LaunchFile) { f.Cwd = "" },
		"no argv":       func(f *LaunchFile) { f.Argv = nil },
		"NUL in argv":   func(f *LaunchFile) { f.Argv = []string{"a\x00b"} },
		"bad utf8 argv": func(f *LaunchFile) { f.Argv = []string{"a\xffb"} },
		"bad utf8 cwd":  func(f *LaunchFile) { f.Cwd = "/a\xff" },
		"bad utf8 env":  func(f *LaunchFile) { f.Env = map[string]string{"A": "\xc3"} },
		"= in name":     func(f *LaunchFile) { f.Env = map[string]string{"A=B": "1"} },
		"empty name":    func(f *LaunchFile) { f.Env = map[string]string{"": "1"} },
		"NUL in value":  func(f *LaunchFile) { f.Env = map[string]string{"A": "\x00"} },
	} {
		t.Run(name, func(t *testing.T) {
			s, dir := store(t)
			f := good
			mod(&f)
			if _, err := s.Write(f); err == nil {
				t.Error("written")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("something was written: %v", err)
			}
		})
	}
	s, _ := store(t)
	if _, err := s.Write(good); err != nil {
		t.Error(err)
	}
}
