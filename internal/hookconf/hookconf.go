package hookconf

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

// FileName is the file's name in the config directory.
const FileName = "hooks.properties"

// The one key, and its bounds (design-spec.md, Hook settings).
const (
	KeyLockWaitMS     = "hook_lock_wait_ms"
	DefaultLockWaitMS = 2000
	MaxLockWaitMS     = 4000
)

// Settings are the hook binary's settings.
type Settings struct {
	LockWaitMS int
}

// Default is the settings of a missing or bad file.
func Default() Settings { return Settings{LockWaitMS: DefaultLockWaitMS} }

// LockWait is how long a hook waits for each lock.
func (s Settings) LockWait() time.Duration {
	return time.Duration(s.LockWaitMS) * time.Millisecond
}

// CorruptError is a file that was read but is bad. Hooks use the default
// without logging it; install reports it as corrupt (operations.md, Error
// kinds). A deliberate twin of config.CorruptError: the TOML parser must not
// reach sesshin-hook.
type CorruptError struct {
	Path   string
	Detail string
}

func (e *CorruptError) Error() string { return e.Path + ": " + e.Detail }

// Read reads the file at path through fsys, following symlinks. A missing
// file is the default, with no error. A file that can't be read returns the
// read's error, and one that is bad a *CorruptError; either way the
// settings returned are the default.
func Read(fsy fsys.FS, path string) (Settings, error) {
	b, err := fsy.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}
	s, problems := Parse(b)
	if len(problems) > 0 {
		return Default(), &CorruptError{Path: path, Detail: strings.Join(problems, "; ")}
	}
	return s, nil
}

// Parse parses the file's content: one key=value per line, no spaces around
// "=", one trailing "\r" per line dropped (CRLF reads as LF), blank lines and
// lines starting with "#" ignored. It returns every problem, each naming its
// line; with any problem the settings are the
// default. A key given twice is a problem, as an unknown key is: the file
// has one meaning or none.
func Parse(b []byte) (Settings, []string) {
	s := Default()
	var problems []string
	seen := 0 // the line the key was set on, or 0
	for i, line := range strings.Split(string(b), "\n") {
		n := i + 1
		line = strings.TrimSuffix(line, "\r")
		at := "line " + strconv.Itoa(n) + ": "
		if strings.Trim(line, " \t") == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		switch {
		case !ok:
			problems = append(problems, at+"no \"=\" in "+strconv.Quote(line))
			continue
		case strings.TrimRight(key, " \t") != key || strings.TrimLeft(value, " \t") != value:
			problems = append(problems, at+"spaces around \"=\" in "+strconv.Quote(line))
			continue
		case key != KeyLockWaitMS:
			problems = append(problems, at+"unknown key "+strconv.Quote(key))
			continue
		case seen != 0:
			problems = append(problems, at+key+" given again (first on line "+strconv.Itoa(seen)+")")
			continue
		}
		seen = n
		ms, ok := parseMS(value)
		if !ok {
			problems = append(problems, at+key+" is "+strconv.Quote(value)+", not an integer from 0 to "+strconv.Itoa(MaxLockWaitMS))
			continue
		}
		s.LockWaitMS = ms
	}
	if len(problems) > 0 {
		return Default(), problems
	}
	return s, nil
}

// parseMS accepts decimal digits only (no sign, no spaces) with a value from
// 0 to MaxLockWaitMS.
func parseMS(v string) (int, bool) {
	if v == "" {
		return 0, false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n > MaxLockWaitMS {
		return 0, false
	}
	return n, true
}
