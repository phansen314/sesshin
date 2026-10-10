package record

import (
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/testhook"
	"github.com/phansen314/sesshin/internal/text"
)

// SetCwd sets lifecycle.json's cwd to cwd, scrubbed, and nothing else: it is
// not an event, so the clocks and event_seq stay (hooks-spec.md, cwd-changed).
// It takes the session lock as Record does (lockSession, with its retry after
// a prune), but never adopts or creates the directory. It writes nothing for
// an empty cwd, a session with no usable lifecycle.json, or a cwd already
// stored. It returns why nothing was written, or nil; ErrNothingToRecord for
// a session sesshin never knew is not logged, and any other error has been.
func SetCwd(env Env, cwd string) error {
	cwd = text.Scrub(cwd)
	if cwd == "" {
		return nil
	}
	return env.withLocked(false, func(root fsys.Root) error {
		l, _, st, err := readFile(env, root, model.LifecycleName, func(data []byte) (model.LifecycleFile, model.FileResult) {
			return model.ReadLifecycle(data, env.SessionID)
		})
		switch st {
		case missing:
			return ErrNothingToRecord
		case unreadable, unusable, otherFmt:
			return err
		}
		if l.Cwd != nil && *l.Cwd == cwd {
			return nil
		}
		l.Cwd = &cwd
		return env.write(root, model.LifecycleName, l)
	})
}

// withLocked is step 1: it locks the session directory, runs f on it, and
// unlocks. A failure to lock is logged, except ErrNothingToRecord. Record
// takes it with adopt for a hook that can; SetCwd and terminal-sync, which
// write without recording an event, never adopt.
func (e Env) withLocked(adopt bool, f func(root fsys.Root) error) error {
	root, lock, err := e.lockSession(adopt)
	if err != nil {
		if err != ErrNothingToRecord {
			e.Log(err.Error())
		}
		return err
	}
	defer root.Close()
	defer lock.Unlock()
	testhook.At("record:locked")
	return f(root)
}
