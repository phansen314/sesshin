package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/settings"
)

// File names in the state directory that install and uninstall write
// (design-spec.md, Files).
const (
	installName  = "install.json"
	proposalName = "settings.proposed.json"
	hookName     = "sesshin-hook"
)

// Setup is what install and uninstall need of the process: the disk, the
// environment, this sesshin's own executable, the clock, and its build. A test
// builds one over a temporary directory with fakes (implementation-spec.md,
// Install and uninstall).
type Setup struct {
	FS     fsys.FS
	GOOS   string
	Getenv func(string) string
	// Executable is this sesshin's executable with symlinks resolved.
	Executable func() (string, error)
	Now        func() time.Time
	Build      buildinfo.Info
	// SelfTest tests the sesshin-hook at hook against this sesshin's build, and
	// returns why it failed, or nil. It is the real self-test unless a test
	// replaces it.
	SelfTest func(hook string, sesshin buildinfo.Info) *Error
}

// OSSetup is the Setup of the running process.
func OSSetup(b buildinfo.Info) Setup {
	return Setup{
		FS:         fsys.OS{},
		GOOS:       runtime.GOOS,
		Getenv:     os.Getenv,
		Executable: ResolvedExecutable,
		Now:        time.Now,
		Build:      b,
		SelfTest:   OSSelfTester().Test,
	}
}

// ResolvedExecutable is os.Executable with symlinks resolved, so the
// sesshin-hook recorded is the one beside the real binary, as a package
// manager's symlink into a versioned directory would otherwise hide.
func ResolvedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// hookBinary is the sesshin-hook beside this sesshin. The file is not required to
// exist: install's self-test says so, and uninstall needs only the name.
func (s Setup) hookBinary() (string, *Error) {
	exe, err := s.Executable()
	if err != nil {
		return "", pathError(exe, err)
	}
	return filepath.Join(filepath.Dir(exe), hookName), nil
}

// pathError is an OS error at path as io, or internal when it holds no errno.
func pathError(path string, err error) *Error {
	if path == "" {
		path = "(executable)"
	}
	return IOError(path, err)
}

// resolve resolves the locations, or reports environment.
func (s Setup) resolve() (loc.Locations, *Error) { return resolveLocations(s.GOOS, s.Getenv) }

// resolveLocations resolves the locations, or reports environment.
func resolveLocations(goos string, getenv func(string) string) (loc.Locations, *Error) {
	l, err := loc.Resolve(goos, getenv)
	var ee *loc.EnvironmentError
	if errors.As(err, &ee) {
		return loc.Locations{}, &Error{
			Kind:    KindEnvironment,
			Message: ee.Error(),
			Details: map[string]any{"variable": ee.Variable},
		}
	}
	if err != nil {
		return loc.Locations{}, &Error{Kind: KindInternal, Message: err.Error()}
	}
	return l, nil
}

func corrupt(path, detail string) *Error {
	return &Error{
		Kind:    KindCorrupt,
		Message: path + ": " + detail,
		Details: map[string]any{"path": path, "detail": detail},
	}
}

// readSettings reads and checks Claude Code's settings.json through the
// path, following a symlink. A missing file is an empty object; any other
// read error is io, and a file that is wrong is corrupt.
func (s Setup) readSettings(path string) (*jsonio.Object, *Error) {
	data, err := s.FS.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return settings.Empty(), nil
	}
	if err != nil {
		return nil, IOError(path, err)
	}
	tree, err := settings.Parse(data)
	var ce *settings.CorruptError
	if errors.As(err, &ce) {
		return nil, corrupt(path, ce.Detail)
	}
	if err != nil {
		return nil, &Error{Kind: KindInternal, Message: path + ": " + err.Error()}
	}
	return tree, nil
}

// readInstall reads install.json from the state directory. One that is
// missing or unusable reads as missing (usable false); any other read error
// is io.
func (s Setup) readInstall(stateDir string) (in model.InstallFile, usable bool, e *Error) {
	path := filepath.Join(stateDir, installName)
	data, err := s.FS.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return in, false, nil
	}
	if err != nil {
		return in, false, IOError(path, err)
	}
	in, r := model.ReadInstall(data)
	return in, r.Usable, nil
}

// ours is the predicate for sesshin's entries (operations.md, sesshin's entries):
// this sesshin's sesshin-hook, the one install.json recorded (when there is one),
// or any path named sesshin-hook.
func ours(hook, recorded string) func(string) bool {
	return func(p string) bool {
		return p == hook || recorded != "" && p == recorded || settings.SesshinHookName(p)
	}
}

// ChangeItem is one item of an operation's changes.
type ChangeItem struct {
	What   string `json:"what"`
	Action string `json:"action"`
}

func changeItems(cs []settings.Change) []ChangeItem {
	out := make([]ChangeItem, len(cs))
	for i, c := range cs {
		out[i] = ChangeItem{What: c.What, Action: string(c.Action)}
	}
	return out
}

// writeProposal writes the proposed settings.json atomically in the state
// directory, creating it if need be, and returns the commands that review
// and apply it.
func (s Setup) writeProposal(stateDir, settingsPath string, tree *jsonio.Object) (proposalPath string, apply []string, e *Error) {
	data, err := settings.Marshal(tree)
	if err != nil {
		return "", nil, &Error{Kind: KindInternal, Message: "encoding the proposal: " + err.Error()}
	}
	proposalPath = filepath.Join(stateDir, proposalName)
	if e := s.publish(stateDir, proposalName, data); e != nil {
		return "", nil, e
	}
	return proposalPath, []string{
		"diff -uN " + shellQuote(settingsPath) + " " + shellQuote(proposalPath),
		"cat " + shellQuote(proposalPath) + " > " + shellQuote(settingsPath),
	}, nil
}

// publish replaces name in dir, creating dir if need be, atomically.
func (s Setup) publish(dir, name string, data []byte) *Error {
	root, err := fsys.OpenRootCreate(s.FS, dir)
	if err != nil {
		return IOError(dir, err)
	}
	defer root.Close()
	if err := fsys.Publish(root, name, data); err != nil {
		return IOError(filepath.Join(dir, name), err)
	}
	return nil
}

// shellQuote single-quotes p for sh, each ' written as '\”: always, unlike
// a hook's command, which quotes only when it must.
func shellQuote(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// dryRun is the one field both inputs have.
func dryRun(f *model.Fields, p *model.Problems) bool {
	var dry bool
	if v, ok := f.Optional("dry_run"); ok {
		dry, _ = p.Bool(v, f.Ptr("dry_run"))
	}
	return dry
}

func internal(format string, args ...any) *Error {
	return &Error{Kind: KindInternal, Message: fmt.Sprintf(format, args...)}
}
