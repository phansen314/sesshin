package iterm2

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/proc"
)

// The limits of each script (operations.md, The iTerm2 launch, Finding a
// session's window, send, focus; design-spec.md, The iTerm2 backend).
const (
	launchLimit = 10 * time.Second
	existLimit  = time.Second
	locateLimit = 5 * time.Second
	sendLimit   = 5 * time.Second
	focusLimit  = 5 * time.Second
)

// Backend is iTerm2 as a placement.Backend (design-spec.md, Terminal
// backends): every optional ability but sync. Its zero value is the real one;
// its fields are for tests.
type Backend struct {
	// GOOS is the system's name; runtime.GOOS when empty.
	GOOS string
	// Runner runs the scripts; nil is /usr/bin/osascript.
	Runner Runner
	// ControllingTTY is the device number of a pid's controlling terminal;
	// nil is proc.ControllingTTY.
	ControllingTTY func(pid int64) (uint64, error)
	// Rdev is the device number of a tty file (st_rdev), read through fsys;
	// nil is a stat on the real disk.
	Rdev func(path string) (uint64, error)
	// Launches is the launch-file store.
	Launches Store
	// Executable is this binary's path; nil is os.Executable.
	Executable func() (string, error)
	// TempDir is where send's file goes; "" is the system's.
	TempDir string
}

var (
	_ placement.Backend       = Backend{}
	_ placement.Launcher      = Backend{}
	_ placement.WindowChecker = Backend{}
	_ placement.Locator       = Backend{}
	_ placement.Sender        = Backend{}
	_ placement.Focuser       = Backend{}
	_ placement.Sweeper       = Backend{}
	_ placement.Hinter        = Backend{}
)

func (b Backend) goos() string {
	if b.GOOS != "" {
		return b.GOOS
	}
	return runtime.GOOS
}

func (b Backend) run(script string, args []string, limit time.Duration) ([]byte, error) {
	r := b.Runner
	if r == nil {
		r = osascript{}
	}
	out, err := r.Run(script, args, limit)
	if err != nil {
		return nil, runError(err)
	}
	return out, nil
}

// Tag is "iterm2".
func (Backend) Tag() string { return Tag }

// Variables are the two Recognize reads.
func (Backend) Variables() []string { return []string{"TERM_PROGRAM", "ITERM_SESSION_ID"} }

// Sweep is Store.Prune over the launches/ of l: the launch files more than
// 120 seconds old as of now.
func (Backend) Sweep(fs fsys.FS, l loc.Locations, now time.Time, dryRun bool) (int, error) {
	return Store{FS: fs, Dir: l.LaunchesDir(), Now: func() time.Time { return now }}.Prune(dryRun)
}

// Recognize is Recognize on this system.
func (b Backend) Recognize(getenv func(string) string) *jsonio.Object {
	return Recognize(b.goos(), getenv)
}

// Replace returns next as it is: with no sync there is nothing of the old
// placement to keep.
func (Backend) Replace(next, _ *jsonio.Object, _ bool) *jsonio.Object { return next }

// Valid is whether Parse accepts p.
func (Backend) Valid(p *jsonio.Object) bool {
	_, ok := Parse(p)
	return ok
}

// Address is PlacementOf the UUID of p, in capitals.
func (Backend) Address(p *jsonio.Object) *jsonio.Object {
	id, ok := Parse(p)
	if !ok {
		return nil
	}
	return PlacementOf(id)
}

// Stored is no title and no variables: nothing learns them after the launch.
func (Backend) Stored(p *jsonio.Object) (string, []placement.Var, bool) {
	_, ok := Parse(p)
	return "", nil, ok
}

// UserVars is true: the launch script sets user.<name> variables.
func (Backend) UserVars() bool { return true }

// Hint is placement.Hinter: what the environment needs for iTerm2 to place
// the caller.
func (Backend) Hint() string {
	return "on macOS, TERM_PROGRAM=iTerm.app and ITERM_SESSION_ID set"
}

// lines splits a script's output into its records, without the trailing
// newlines: the script ends each record with one, and osascript adds its own
// after the last. No output is no records.
func lines(out []byte) []string {
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// answer reports a script's one-word answer (notRunning, notFound), trimmed.
func answer(out []byte) string { return strings.TrimSpace(string(out)) }

// listed reads the output of listScript: the unique id of every session, in
// capitals. Any record that is not a UUID makes the whole output garbage.
func listed(out []byte) (map[string]bool, error) {
	if answer(out) == notRunning {
		return nil, errors.New("iTerm2 is not running")
	}
	ids := map[string]bool{}
	for _, l := range lines(out) {
		if !isUUID(l) {
			return nil, errors.New("osascript printed " + strconv.Quote(l) + ", not a session's unique id")
		}
		ids[strings.ToUpper(l)] = true
	}
	return ids, nil
}

// Exist runs one script for all of ps and answers for each: gone only when
// iTerm2 is running, answers, and does not list the pane. Not running, and any
// failure, is no answer (design-spec.md, The iTerm2 backend).
func (b Backend) Exist(ps []*jsonio.Object) []placement.Existence {
	out := make([]placement.Existence, len(ps))
	asked := false
	for _, p := range ps {
		if _, ok := Parse(p); ok {
			asked = true
		}
	}
	if !asked {
		return out
	}
	raw, err := b.run(listScript, nil, existLimit)
	if err != nil {
		return out
	}
	ids, err := listed(raw)
	if err != nil {
		return out
	}
	for i, p := range ps {
		id, ok := Parse(p)
		switch {
		case !ok:
		case ids[id]:
			out[i] = placement.Present
		default:
			out[i] = placement.Gone
		}
	}
	return out
}

// Locate asks for every session's unique id and tty and takes the session
// whose tty is pid's controlling terminal, comparing device numbers, never
// names. The stored placement plays no part (operations.md, Finding a
// session's window).
func (b Backend) Locate(_ *jsonio.Object, pid int64, _ func(string) string) (*jsonio.Object, error) {
	ctty := b.ControllingTTY
	if ctty == nil {
		ctty = proc.ControllingTTY
	}
	dev, err := ctty(pid)
	if err != nil {
		return nil, errors.New("the controlling terminal of pid " + strconv.FormatInt(pid, 10) + " is unknown: " + err.Error())
	}
	raw, err := b.run(ttyScript, nil, locateLimit)
	if err != nil {
		return nil, err
	}
	if answer(raw) == notRunning {
		return nil, errors.New("iTerm2 is not running")
	}
	rdev := b.Rdev
	if rdev == nil {
		rdev = statRdev
	}
	for _, l := range lines(raw) {
		id, tty, ok := strings.Cut(l, "\t")
		if !ok || !isUUID(id) {
			return nil, errors.New("osascript printed " + strconv.Quote(l) + ", not a session's unique id and tty")
		}
		if tty == "" {
			continue // a session with no tty to compare, as one that can't be stat'ed
		}
		if got, err := rdev(tty); err == nil && got == dev {
			return PlacementOf(id), nil
		}
	}
	return nil, errors.New("no iTerm2 session has the controlling terminal of pid " + strconv.FormatInt(pid, 10))
}

// statRdev is st_rdev of the file at path, read through fsys.
func statRdev(path string) (uint64, error) {
	st, err := fsys.OS{}.Stat(path)
	if err != nil {
		return 0, err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no device number")
	}
	return rdevOf(sys), nil
}

// Send pastes text into the session as one bracketed paste, written by a
// script from a file (an argument can't hold 1 MiB), and, if submit, presses
// Enter with a second call (operations.md, send, Effects 4 and 5). The file
// has mode 0600, is in the temporary directory, and is removed once the
// first call has returned.
func (b Backend) Send(p *jsonio.Object, text string, submit bool) error {
	id, ok := Parse(p)
	if !ok {
		return &placement.SendError{Err: errors.New("not an iTerm2 placement")}
	}
	if err := b.paste(id, text); err != nil {
		return &placement.SendError{Err: err}
	}
	if !submit {
		return nil
	}
	out, err := b.run(enterScript, []string{id}, sendLimit)
	if err == nil {
		err = sent(out)
	}
	if err != nil {
		return &placement.SendError{Submit: true, Err: err}
	}
	return nil
}

// The bracketed-paste markers sesshin puts around the text itself.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

func (b Backend) paste(id, text string) error {
	f, err := os.CreateTemp(b.TempDir, "sesshin-send-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(pasteStart + text + pasteEnd)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	out, err := b.run(pasteScript, []string{id, f.Name()}, sendLimit)
	if err != nil {
		return err
	}
	return sent(out)
}

// sent reads the answer of a script that writes to a session.
func sent(out []byte) error {
	switch a := answer(out); a {
	case "ok":
		return nil
	case notRunning:
		return errors.New("iTerm2 is not running")
	case notFound:
		return errors.New("iTerm2 has no such session")
	default:
		return errors.New("osascript printed " + strconv.Quote(a) + ", not ok")
	}
}

// Focus selects the session, its tab, and its window, in that order, then
// activates iTerm2.
func (b Backend) Focus(p *jsonio.Object) error {
	id, ok := Parse(p)
	if !ok {
		return errors.New("not an iTerm2 placement")
	}
	out, err := b.run(focusScript, []string{id}, focusLimit)
	if err != nil {
		return err
	}
	return sent(out)
}
