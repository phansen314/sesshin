package ops

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/config"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/backends"
	"github.com/phansen314/sesshin/internal/proc"
)

// ReadEnv is what the operations read from outside: the filesystem,
// the environment, the clock, and the process table. Tests give it temp
// directories, a fixed clock, and a fake table.
type ReadEnv struct {
	FS     fsys.FS
	Getenv func(string) string
	// GOOS selects the layout of the locations, as loc.Resolve takes it.
	GOOS      string
	Now       func() time.Time
	StartedAt live.StartedAt
	// Lookup finds Claude's process for the selector self, as a hook finds
	// its own; nil is proc.FindCaller.
	Lookup func(fsy fsys.FS, claudePID string) proc.Claude
	// Backends are the terminal backends, in detection order
	// (design-spec.md, Terminal backends). Nothing outside them reads a
	// placement.
	Backends []placement.Backend
}

// OSReadEnv is the real environment.
func OSReadEnv() ReadEnv {
	return ReadEnv{
		FS:        fsys.OS{},
		Getenv:    os.Getenv,
		GOOS:      runtime.GOOS,
		Now:       time.Now,
		StartedAt: func(pid int64) (string, error) { return proc.StartedAt(fsys.OS{}, pid) },
		Backends:  backends.All(),
	}
}

// backendOf is the backend a stored placement's terminal tag names, or nil
// for none: no placement, or a tag this binary has no backend for.
func (e ReadEnv) backendOf(p *jsonio.Object) placement.Backend {
	return placement.Of(e.Backends, p)
}

// valid is the backend of a stored placement when it accepts the placement;
// ok is false for no placement, an unknown tag, and a placement its backend
// rejects.
func (e ReadEnv) valid(p *jsonio.Object) (b placement.Backend, ok bool) {
	if b = e.backendOf(p); b == nil || !b.Valid(p) {
		return nil, false
	}
	return b, true
}

// whyNoPlacement says why a stored placement can't be used, for
// no-placement: there is none, its terminal is one this binary has no backend
// for (named: model allows only a lowercase tag), or its backend rejects it.
func (e ReadEnv) whyNoPlacement(p *jsonio.Object) string {
	tag := placement.TagOf(p)
	switch b := e.backendOf(p); {
	case tag == "":
		return "it has no placement"
	case b == nil:
		return "its placement names the terminal " + strconv.Quote(tag) + ", which this sesshin has no backend for"
	default:
		return "its " + b.Tag() + " placement is not valid"
	}
}

// TabTitle is the tab title a stored placement's backend says it has, or ""
// for none: what the pickers show.
func (e ReadEnv) TabTitle(p *jsonio.Object) string {
	if b := e.backendOf(p); b != nil {
		if title, _, ok := b.Stored(p); ok {
			return title
		}
	}
	return ""
}

// loadSetup resolves the locations and reads config.toml: environment, or
// corrupt or io for the file.
func loadSetup(env ReadEnv) (loc.Locations, config.Config, *Error) {
	l, e := resolveLocations(env.GOOS, env.Getenv)
	if e != nil {
		return l, config.Config{}, e
	}
	cfgPath := filepath.Join(l.ConfigDir, config.FileName)
	cfg, err := config.Read(env.FS, cfgPath)
	if err != nil {
		return l, config.Config{}, configError(cfgPath, err)
	}
	return l, cfg, nil
}

func ptrTo[T any](v T) *T { return &v }

// headless is design-spec.md's term: started by another session, or by an
// SDK.
func headless(l *model.LifecycleFile) bool {
	return l.Nested != nil && *l.Nested || l.Entrypoint != nil && strings.HasPrefix(*l.Entrypoint, "sdk-")
}
