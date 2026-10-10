package loc

import (
	"path/filepath"
	"strconv"
)

// Locations are the paths every entry point resolves from its own
// environment (design-spec.md, Locations). Each is absolute and clean.
type Locations struct {
	ConfigDir      string
	StateDir       string
	ClaudeSettings string
}

// EnvironmentError is a HOME that is unset, empty, or not absolute. Callers
// report it as the environment error kind with details.variable Variable
// (operations.md, Error kinds); a hook exits 0 silently instead
// (hooks-spec.md, Recording an event, step 1).
type EnvironmentError struct {
	Variable string // always "HOME"
	Value    string // as found; "" when unset or empty
}

func (e *EnvironmentError) Error() string {
	if e.Value == "" {
		return e.Variable + " is not set"
	}
	return e.Variable + " is not an absolute path: " + strconv.Quote(e.Value)
}

// SessionsDir is the directory holding one directory per session.
func (l Locations) SessionsDir() string { return filepath.Join(l.StateDir, "sessions") }

// ReservationsDir is the directory holding one file per reservation.
func (l Locations) ReservationsDir() string { return filepath.Join(l.StateDir, "reservations") }

// LaunchesDir is the directory holding one file per iTerm2 launch not yet read.
func (l Locations) LaunchesDir() string { return filepath.Join(l.StateDir, "launches") }

// SessionDir is a session's directory, named by its lowercased UUID.
func (l Locations) SessionDir(id string) string { return filepath.Join(l.SessionsDir(), id) }

// Resolve resolves the locations for goos, "darwin" for macOS and anything
// else the Linux layout, reading variables through getenv (os.Getenv in
// production). HOME must be absolute, even when the variables below make it
// unneeded, so every entry point fails the same way in the same environment.
// XDG_CONFIG_HOME and XDG_STATE_HOME (Linux only) and CLAUDE_CONFIG_DIR (both)
// are used only when absolute, as the XDG Base Directory spec requires; an
// empty or relative value is ignored.
func Resolve(goos string, getenv func(string) string) (Locations, error) {
	home := getenv("HOME")
	if !filepath.IsAbs(home) {
		return Locations{}, &EnvironmentError{Variable: "HOME", Value: home}
	}
	claude := absOr(getenv("CLAUDE_CONFIG_DIR"), filepath.Join(home, ".claude"))
	var l Locations
	if goos == "darwin" {
		l.ConfigDir = filepath.Join(home, "Library", "Application Support", "sesshin")
		l.StateDir = filepath.Join(l.ConfigDir, "state")
	} else {
		l.ConfigDir = filepath.Join(absOr(getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config")), "sesshin")
		l.StateDir = filepath.Join(absOr(getenv("XDG_STATE_HOME"), filepath.Join(home, ".local", "state")), "sesshin")
	}
	l.ClaudeSettings = filepath.Join(claude, "settings.json")
	return l, nil
}

// absOr is v when it is an absolute path, else fallback.
func absOr(v, fallback string) string {
	if filepath.IsAbs(v) {
		return v
	}
	return fallback
}
