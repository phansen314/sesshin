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
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/loc"
)

// MaxLaunchAge is how old a launch file may be and still be read, and how old
// it must be for prune to remove it: the age at which a reservation never
// launched is stranded (design-spec.md, Reservations).
const MaxLaunchAge = 120 * time.Second

// LaunchFile is what an iTerm2 launch is to run (design-spec.md, The iTerm2
// backend). It has no schema and no time of its own: the binary that writes it
// reads it, seconds later, and its age is its file's modification time.
type LaunchFile struct {
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
	if err := f.check(); err != nil {
		return "", err
	}
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
	if err := s.checkDir(dir); err != nil {
		return LaunchFile{}, err
	}
	root, err := s.fs().OpenRoot(dir)
	if err != nil {
		return LaunchFile{}, errors.New("the launch file is missing: " + err.Error())
	}
	defer root.Close()
	if st, err := root.Stat("."); err != nil {
		return LaunchFile{}, err
	} else if err := s.checkOwner(st, "launches/"); err != nil || st.Mode().Perm()&0o022 != 0 {
		return LaunchFile{}, errors.New("launches/ is writable by group or others, or not the user's own")
	}
	name := nonce + ".json"
	st, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return LaunchFile{}, errors.New("the launch file is missing (it may have been read already, or removed after 120 seconds)")
	case err != nil:
		return LaunchFile{}, err
	case !st.Mode().IsRegular():
		return LaunchFile{}, errors.New("the launch file is not a regular file (a symlink is refused)")
	case st.Mode().Perm() != fsys.FileMode:
		return LaunchFile{}, errors.New("the launch file's mode is not 0600")
	}
	if err := s.checkOwner(st, "the launch file"); err != nil {
		return LaunchFile{}, err
	}
	data, err := root.ReadFile(name)
	if err != nil {
		return LaunchFile{}, err
	}
	// What was read is what was checked, not a file swapped in between.
	if again, err := root.Lstat(name); err != nil || !os.SameFile(st, again) {
		return LaunchFile{}, errors.New("the launch file changed while it was read")
	}
	if err := root.Remove(name); err != nil {
		return LaunchFile{}, errors.New("removing the launch file: " + err.Error())
	}
	if tooOld(s.now().Sub(st.ModTime())) {
		return LaunchFile{}, errors.New("the launch file is more than 120 seconds old by its modification time")
	}
	f, ok := parseLaunch(data)
	if !ok {
		return LaunchFile{}, errors.New("the file is not a launch file")
	}
	return f, nil
}

// tooOld says whether a file with this age, by its modification time, is
// past MaxLaunchAge: a time that far in the future is as old as one that far
// in the past, since a restore or a touch could set it.
func tooOld(age time.Duration) bool { return age > MaxLaunchAge || age < -MaxLaunchAge }

// checkDir refuses a launches/ that is not a real directory, not the user's
// own, or writable by group or others: someone else could then put a file
// there.
func (s Store) checkDir(dir string) error {
	st, err := s.fs().Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return errors.New("the launch file is missing")
	case err != nil:
		return err
	case !st.IsDir():
		return errors.New("launches/ is not a directory (a symlink is refused)")
	case st.Mode().Perm()&0o022 != 0:
		return errors.New("launches/ is writable by group or others")
	}
	return s.checkOwner(st, "launches/")
}

// checkOwner refuses a file that is not the user's own.
func (s Store) checkOwner(st fs.FileInfo, what string) error {
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != s.uid() {
		return errors.New(what + " is not the user's own")
	}
	return nil
}

// parseLaunch reads a launch file: exactly its three keys, each valid by
// check.
func parseLaunch(data []byte) (LaunchFile, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || len(raw) != 3 {
		return LaunchFile{}, false
	}
	var f LaunchFile
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if dec.Decode(&f) != nil || f.Env == nil || f.check() != nil {
		return LaunchFile{}, false
	}
	return f, true
}

// check is what makes a launch file one: a directory that is an absolute
// path, variables whose names can be set, a program that is not empty, and no
// NUL or invalid UTF-8 in any string, which encoding/json would replace. Write
// refuses what launch-exec would.
func (f LaunchFile) check() error {
	bad := func(s string) bool { return !utf8.ValidString(s) || strings.Contains(s, "\x00") }
	switch {
	case f.Cwd == "" || f.Cwd[0] != '/' || bad(f.Cwd):
		return errors.New("the working directory " + strconv.Quote(f.Cwd) + " is not an absolute path of valid text")
	case len(f.Argv) == 0:
		return errors.New("the program to run is empty")
	}
	for _, a := range f.Argv {
		if bad(a) {
			return errors.New("an argument holds a NUL or invalid UTF-8")
		}
	}
	for k, v := range f.Env {
		if k == "" || strings.Contains(k, "=") || bad(k) || bad(v) {
			return errors.New("the variable " + strconv.Quote(k) + " can't be set")
		}
	}
	return nil
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
		if !tooOld(now.Sub(st.ModTime())) {
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
