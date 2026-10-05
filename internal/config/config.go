package config

import (
	"errors"
	"io/fs"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/phansen314/sesshin/internal/fsys"
)

// FileName is the file's name in the config directory.
const FileName = "config.toml"

// Config is config.toml's settings. The retention ones are integers ≥ 0, and
// unbounded above: use the accessors, which saturate, to turn one into a
// time.
type Config struct {
	RetainDays          int64
	RetainHeadlessHours int64
	// Shell is spawn_shell as written; nil when the file leaves it out. Use
	// SpawnShell, which fills in the default.
	Shell []string
}

// Default is the settings of a missing file, and of each key a file leaves
// out.
func Default() Config {
	return Config{RetainDays: 30, RetainHeadlessHours: 24}
}

// SpawnShell is the shell spawn runs claude through: spawn_shell, or
// [$SHELL, -l, -i] when it is absent and SHELL is absolute, else
// [/bin/sh, -l, -i]. A login, interactive shell gives claude the PATH a tab
// opened by hand has. The result is a copy.
func (c Config) SpawnShell(getenv func(string) string) []string {
	if c.Shell != nil {
		return slices.Clone(c.Shell)
	}
	if sh := getenv("SHELL"); filepath.IsAbs(sh) {
		return []string{sh, "-l", "-i"}
	}
	return []string{"/bin/sh", "-l", "-i"}
}

// RetainHeadless is retain_headless_hours as a duration, capped at the
// largest one.
func (c Config) RetainHeadless() time.Duration {
	return saturate(c.RetainHeadlessHours, time.Hour)
}

// saturate is n units, or the largest duration when that overflows.
func saturate(n int64, unit time.Duration) time.Duration {
	if n > int64(math.MaxInt64/unit) {
		return math.MaxInt64
	}
	return time.Duration(n) * unit
}

// MaxDays caps a day count before it reaches time.Time.AddDate, whose
// arithmetic wraps far below math.MaxInt64 (math.MaxInt64 days back lands
// the next day). 1,000,000 days is about 2,700 years: further back than any
// session.
const MaxDays = 1_000_000

// DaysBefore is t moved back days calendar days, with days capped at
// MaxDays. For retain_days, the config's or prune's explicit one.
func DaysBefore(t time.Time, days int64) time.Time {
	return t.AddDate(0, 0, -int(min(days, MaxDays)))
}

// CorruptError is a config.toml that was read but is wrong: operations that
// read it fail with corrupt (operations.md, Error kinds). A deliberate twin of
// hookconf.CorruptError: the TOML parser must not reach sesshin-hook.
type CorruptError struct {
	Path   string
	Detail string
}

func (e *CorruptError) Error() string { return e.Path + ": " + e.Detail }

// Read reads the file at path through fsys, following symlinks. A missing
// file is the default, with no error. A file that can't be read returns the
// read's error; one that is wrong, a *CorruptError.
func Read(fsy fsys.FS, path string) (Config, error) {
	b, err := fsy.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	c, detail := Parse(b)
	if detail != "" {
		return Config{}, &CorruptError{Path: path, Detail: detail}
	}
	return c, nil
}

// file is the TOML layout. Pointers tell a key that is absent from one set
// to its zero value.
type file struct {
	RetainDays          *int64    `toml:"retain_days"`
	RetainHeadlessHours *int64    `toml:"retain_headless_hours"`
	SpawnShell          *[]string `toml:"spawn_shell"`
}

// Parse parses the file's content. On a problem it returns a human-readable
// detail, naming every problem it found: a TOML syntax or type error stops
// the decoder at the first, so that one is reported alone; otherwise every
// unknown key, every negative value, and a bad spawn_shell.
func Parse(b []byte) (Config, string) {
	var f file
	md, err := toml.Decode(string(b), &f)
	if err != nil {
		return Config{}, err.Error()
	}
	var problems []string
	if keys := md.Undecoded(); len(keys) > 0 {
		names := make([]string, len(keys))
		for i, k := range keys {
			names[i] = strconv.Quote(k.String())
		}
		problems = append(problems, "unknown key "+strings.Join(names, ", "))
	}
	c := Default()
	for _, s := range []struct {
		key string
		v   *int64
		to  *int64
	}{
		{"retain_days", f.RetainDays, &c.RetainDays},
		{"retain_headless_hours", f.RetainHeadlessHours, &c.RetainHeadlessHours},
	} {
		switch {
		case s.v == nil:
		case *s.v < 0:
			problems = append(problems, s.key+" is "+strconv.FormatInt(*s.v, 10)+", below 0")
		default:
			*s.to = *s.v
		}
	}
	if sh := f.SpawnShell; sh != nil {
		switch {
		case len(*sh) == 0:
			problems = append(problems, "spawn_shell is empty")
		case slices.Contains(*sh, ""):
			problems = append(problems, "spawn_shell has an empty string")
		case !filepath.IsAbs((*sh)[0]):
			problems = append(problems, "spawn_shell's first word is not an absolute path")
		default:
			c.Shell = slices.Clone(*sh)
		}
	}
	if len(problems) > 0 {
		return Config{}, strings.Join(problems, "; ")
	}
	return c, ""
}
