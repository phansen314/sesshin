package ops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/config"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
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
	// Windows asks the placement's terminal backend which windows exist on
	// the placement's socket: their IDs, and false for no answer, however
	// the question failed (design-spec.md, Placement). Nil answers nothing.
	Windows func(placement *jsonio.Object) ([]int64, bool)
}

// OSReadEnv is the real environment.
func OSReadEnv() ReadEnv {
	return ReadEnv{
		FS:        fsys.OS{},
		Getenv:    os.Getenv,
		GOOS:      runtime.GOOS,
		Now:       time.Now,
		StartedAt: func(pid int64) (string, error) { return proc.StartedAt(fsys.OS{}, pid) },
		Windows:   kittyWindows,
	}
}

// kittyWindows is the real ReadEnv.Windows: kitty is the only backend, so a
// placement it can't use has no answer.
func kittyWindows(placement *jsonio.Object) ([]int64, bool) {
	p, ok := kitty.Parse(placement)
	if !ok {
		return nil, false
	}
	ids, err := kitty.Windows(p.Socket)
	return ids, err == nil
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
