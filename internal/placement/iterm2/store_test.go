package iterm2

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// store is a Store over a temp directory at launchNow.
func store(t *testing.T) (Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "launches")
	return Store{Dir: dir, Now: func() time.Time { return launchNow }, Nonce: func() string { return nonce }}, dir
}

func written(t *testing.T) (Store, string) {
	t.Helper()
	s, dir := store(t)
	if _, err := s.Write(LaunchFile{Cwd: "/w", Env: map[string]string{"A": "1"}, Argv: []string{"/bin/sh", "-c", "x"}}); err != nil {
		t.Fatal(err)
	}
	return s, filepath.Join(dir, nonce+".json")
}

func TestTake(t *testing.T) {
	s, path := written(t)
	f, err := s.Take(nonce)
	if err != nil || f.Cwd != "/w" || f.Env["A"] != "1" || len(f.Argv) != 3 {
		t.Fatalf("got %+v, %v", f, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("not removed on read: %v", err)
	}
	if _, err := s.Take(nonce); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("second read: %v", err)
	}
}

func TestTakeRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		nonce string
		prep  func(t *testing.T, s *Store, path string)
		want  string
		gone  bool // the file is removed anyway
	}{
		"short nonce":    {"abc", nil, "32 lowercase", false},
		"upper nonce":    {strings.ToUpper(nonce), nil, "32 lowercase", false},
		"path nonce":     {"../" + nonce[3:], nil, "32 lowercase", false},
		"missing":        {strings.Repeat("a", 32), nil, "missing", false},
		"wrong mode":     {nonce, func(t *testing.T, _ *Store, p string) { os.Chmod(p, 0o644) }, "0600", false},
		"group-writable": {nonce, func(t *testing.T, _ *Store, p string) { os.Chmod(p, 0o660) }, "0600", false},
		"not a regular":  {nonce, func(t *testing.T, _ *Store, p string) { os.Remove(p); os.Mkdir(p, 0o700) }, "not a regular", false},
		"someone else's": {nonce, func(t *testing.T, s *Store, _ string) { s.Getuid = func() int { return os.Getuid() + 1 } }, "own", false},
		"too old": {nonce, func(t *testing.T, _ *Store, p string) {
			os.Chtimes(p, launchNow.Add(-121*time.Second), launchNow.Add(-121*time.Second))
		}, "120 seconds", true},
		"not json": {nonce, func(t *testing.T, _ *Store, p string) { os.WriteFile(p, []byte("hello"), 0o600) }, "not a launch file", true},
		"empty argv": {nonce, func(t *testing.T, _ *Store, p string) {
			rewrite(p, `{"created_at":"2026-10-09T12:00:00Z","cwd":"/w","env":{},"argv":[]}`)
		}, "not a launch file", true},
		"relative cwd": {nonce, func(t *testing.T, _ *Store, p string) {
			rewrite(p, `{"created_at":"2026-10-09T12:00:00Z","cwd":"w","env":{},"argv":["x"]}`)
		}, "not a launch file", true},
		"extra key": {nonce, func(t *testing.T, _ *Store, p string) {
			rewrite(p, `{"created_at":"2026-10-09T12:00:00Z","cwd":"/w","env":{},"argv":["x"],"more":1}`)
		}, "not a launch file", true},
		"bad time": {nonce, func(t *testing.T, _ *Store, p string) {
			rewrite(p, `{"created_at":"yesterday","cwd":"/w","env":{},"argv":["x"]}`)
		}, "not a launch file", true},
		"env not strings": {nonce, func(t *testing.T, _ *Store, p string) {
			rewrite(p, `{"created_at":"2026-10-09T12:00:00Z","cwd":"/w","env":{"A":1},"argv":["x"]}`)
		}, "not a launch file", true},
		"env name": {nonce, func(t *testing.T, _ *Store, p string) {
			rewrite(p, `{"created_at":"2026-10-09T12:00:00Z","cwd":"/w","env":{"A=B":"1"},"argv":["x"]}`)
		}, "not a launch file", true},
		"no env": {nonce, func(t *testing.T, _ *Store, p string) {
			rewrite(p, `{"created_at":"2026-10-09T12:00:00Z","cwd":"/w","env":null,"argv":["x"]}`)
		}, "not a launch file", true},
	} {
		t.Run(name, func(t *testing.T) {
			s, path := written(t)
			if tc.prep != nil {
				tc.prep(t, &s, path)
			}
			if _, err := s.Take(tc.nonce); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
			_, err := os.Stat(path)
			if gone := os.IsNotExist(err); tc.nonce == nonce && gone != tc.gone {
				t.Errorf("removed %v, want %v", gone, tc.gone)
			}
		})
	}
}

func rewrite(path, content string) { os.WriteFile(path, []byte(content), 0o600) }

func TestRemoveAndWriteDirectory(t *testing.T) {
	s, path := written(t)
	st, _ := os.Stat(filepath.Dir(path))
	if st.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %o", st.Mode().Perm())
	}
	if err := s.Remove(nonce); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(nonce); err != nil {
		t.Errorf("removing again: %v", err)
	}
	empty, _ := store(t)
	if err := empty.Remove(nonce); err != nil {
		t.Errorf("no directory: %v", err)
	}
}

func TestRandomNonce(t *testing.T) {
	s := Store{}
	a, b := s.nonce(), s.nonce()
	if !IsNonce(a) || !IsNonce(b) || a == b {
		t.Errorf("%q, %q", a, b)
	}
}

func TestPrune(t *testing.T) {
	s, dir := store(t)
	if n, err := s.Prune(false); n != 0 || err != nil {
		t.Fatalf("no directory: %d, %v", n, err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	age := func(name string, secs int, content string) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		at := launchNow.Add(-time.Duration(secs) * time.Second)
		os.Chtimes(p, at, at)
	}
	age("old.json", 121, "{")
	age("older", 4000, "whatever it holds")
	age("exact.json", 120, "{}")
	age("new.json", 5, "{}")
	age(".sesshin-tmp-old", 4000, "hidden")
	os.Mkdir(filepath.Join(dir, "subdir"), 0o700)
	os.Symlink("old.json", filepath.Join(dir, "link"))

	if n, err := s.Prune(true); n != 2 || err != nil {
		t.Errorf("dry run: %d, %v", n, err)
	}
	if len(files(t, dir)) != 7 {
		t.Errorf("dry run removed: %v", files(t, dir))
	}
	if n, err := s.Prune(false); n != 2 || err != nil {
		t.Errorf("%d, %v", n, err)
	}
	got := strings.Join(files(t, dir), " ")
	if got != ".sesshin-tmp-old exact.json link new.json subdir" {
		t.Errorf("left %q", got)
	}
}
