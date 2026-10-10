package iterm2

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
)

// MaxLaunchAge is how old a launch file may be and still be read, and how old
// it must be for prune to remove it: the age at which a reservation never
// launched is stranded (design-spec.md, Reservations).
const MaxLaunchAge = 120 * time.Second

// LaunchFile is what an iTerm2 launch is to run (design-spec.md, The iTerm2
// backend). It has no schema: the binary that writes it reads it, seconds
// later.
type LaunchFile struct {
	// CreatedAt is when it was written.
	CreatedAt model.Timestamp `json:"created_at"`
	// Cwd is the directory to start in.
	Cwd string `json:"cwd"`
	// Env are the variables to set on top of the window's own environment.
	Env map[string]string `json:"env"`
	// Argv is the program and its arguments, not empty.
	Argv []string `json:"argv"`
}

// Store is the launch files in launches/ (design-spec.md, State directory
// layout). Its zero value is the real one: the directory resolved from the
// environment as every entry point does, the real disk, the system clock.
// Its fields are for tests.
type Store struct {
	// FS is the disk; nil is fsys.OS.
	FS fsys.FS
	// Dir is launches/'s path; "" resolves it from HOME on this system.
	Dir string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Nonce makes a new nonce; nil is 128 random bits from math/rand/v2,
	// which the runtime seeds, as fsys names its temp files.
	Nonce func() string
	// Getuid is the user's ID; nil is os.Getuid.
	Getuid func() int
}

func (s Store) fs() fsys.FS {
	if s.FS != nil {
		return s.FS
	}
	return fsys.OS{}
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Store) uid() int {
	if s.Getuid != nil {
		return s.Getuid()
	}
	return os.Getuid()
}

func (s Store) dir() (string, error) {
	if s.Dir != "" {
		return s.Dir, nil
	}
	l, err := loc.Resolve(runtime.GOOS, os.Getenv)
	if err != nil {
		return "", err
	}
	return l.LaunchesDir(), nil
}

func (s Store) nonce() string {
	if s.Nonce != nil {
		return s.Nonce()
	}
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], rand.Uint64())
	binary.BigEndian.PutUint64(b[8:], rand.Uint64())
	return hex.EncodeToString(b[:])
}

// IsNonce reports whether s is 32 lowercase hexadecimal digits.
func IsNonce(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := range len(s) {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Write stores a launch file under a new nonce and returns the nonce. The
// directory, mode 0700, is created when first needed; the file has mode 0600.
func (s Store) Write(f LaunchFile) (nonce string, err error) {
	dir, err := s.dir()
	if err != nil {
		return "", err
	}
	if f.Env == nil {
		f.Env = map[string]string{}
	}
	f.CreatedAt = model.FormatTimestamp(s.now())
	data, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	root, err := fsys.OpenRootCreate(s.fs(), dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	nonce = s.nonce()
	if err := fsys.Publish(root, nonce+".json", data); err != nil {
		return "", err
	}
	return nonce, nil
}

// Remove removes the launch file of nonce, for a launch that iTerm2 refused;
// one already gone is not an error.
func (s Store) Remove(nonce string) error {
	dir, err := s.dir()
	if err != nil {
		return err
	}
	root, err := s.fs().OpenRoot(dir)
	if err != nil {
		return ignoreMissing(err)
	}
	defer root.Close()
	return ignoreMissing(root.Remove(nonce + ".json"))
}

func ignoreMissing(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Take reads the launch file of nonce and removes it, as launch-exec does
// before it runs anything (operations.md, The iTerm2 launch). It refuses a
// nonce that is not 32 lowercase hex digits, and a file that is missing, not
// a regular file, not the user's own, of a mode other than 0600, more than
// 120 seconds old, or not a launch file; the error says which. A file that
// passed the first four checks is removed whatever it holds.
func (s Store) Take(nonce string) (LaunchFile, error) {
	if !IsNonce(nonce) {
		return LaunchFile{}, errors.New("the launch nonce is not 32 lowercase hexadecimal digits")
	}
	dir, err := s.dir()
	if err != nil {
		return LaunchFile{}, err
	}
	root, err := s.fs().OpenRoot(dir)
	if err != nil {
		return LaunchFile{}, errors.New("the launch file is missing: " + err.Error())
	}
	defer root.Close()
	name := nonce + ".json"
	st, err := root.Stat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return LaunchFile{}, errors.New("the launch file is missing (it may have been read already, or removed after 120 seconds)")
	case err != nil:
		return LaunchFile{}, err
	case !st.Mode().IsRegular():
		return LaunchFile{}, errors.New("the launch file is not a regular file")
	case st.Mode().Perm() != fsys.FileMode:
		return LaunchFile{}, errors.New("the launch file's mode is not 0600")
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != s.uid() {
		return LaunchFile{}, errors.New("the launch file is not the user's own")
	}
	data, err := root.ReadFile(name)
	if err != nil {
		return LaunchFile{}, err
	}
	if err := root.Remove(name); err != nil {
		return LaunchFile{}, errors.New("removing the launch file: " + err.Error())
	}
	if age := s.now().Sub(st.ModTime()); age > MaxLaunchAge {
		return LaunchFile{}, errors.New("the launch file is more than 120 seconds old")
	}
	f, ok := parseLaunch(data)
	if !ok {
		return LaunchFile{}, errors.New("the file is not a launch file")
	}
	return f, nil
}

// parseLaunch reads a launch file: exactly its four keys, a timestamp, a
// directory that is an absolute path, variables with names that can be set,
// and a program that is not empty.
func parseLaunch(data []byte) (LaunchFile, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || len(raw) != 4 {
		return LaunchFile{}, false
	}
	var f LaunchFile
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if dec.Decode(&f) != nil || f.Env == nil || len(f.Argv) == 0 || len(f.Cwd) == 0 || f.Cwd[0] != '/' {
		return LaunchFile{}, false
	}
	if _, err := time.Parse("2006-01-02T15:04:05Z", string(f.CreatedAt)); err != nil {
		return LaunchFile{}, false
	}
	for k, v := range f.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.Contains(v, "\x00") {
			return LaunchFile{}, false
		}
	}
	for _, a := range f.Argv {
		if strings.Contains(a, "\x00") {
			return LaunchFile{}, false
		}
	}
	return f, true
}

// Prune removes each visible regular file in launches/ last modified more
// than 120 seconds before now, whatever it holds, with no lock, and returns
// how many; with dryRun it removes none and returns how many it would. A
// missing launches/ is none. A failed removal stops the run with its error;
// the ones before it stay removed, and are counted.
func (s Store) Prune(dryRun bool) (int, error) {
	dir, err := s.dir()
	if err != nil {
		return 0, err
	}
	root, err := s.fs().OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	ents, err := root.ReadDir(".")
	if err != nil {
		return 0, err
	}
	now, n := s.now(), 0
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !e.Type().IsRegular() {
			continue
		}
		st, err := root.Stat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return n, err
		}
		if now.Sub(st.ModTime()) <= MaxLaunchAge {
			continue
		}
		if !dryRun {
			if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return n, err
			}
		}
		n++
	}
	return n, nil
}
