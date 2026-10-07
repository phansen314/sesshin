package record

import (
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

// SetPlacementSync is terminal-sync's write (hooks-spec.md, terminal-sync):
// under the session lock it reads sesshin.json and hands apply the placement
// stored there. apply returns the placement to store and whether it changed;
// only a change is written, and only the placement, with the file's id kept.
// It takes the lock as SetCwd does, but never adopts and never creates the
// directory. It writes nothing for a missing sesshin.json (ErrNothingToRecord,
// not logged), an unusable one (logged, left as it is), one in another format (left as it
// is, not logged), or a null placement.
// apply is not called for the last, and runs with the lock held, so it must
// not start a process. It returns why nothing was written, or nil; any error
// but ErrNothingToRecord has been logged.
func SetPlacementSync(env Env, apply func(old *jsonio.Object) (*jsonio.Object, bool)) error {
	return env.withLocked(false, func(root fsys.Root) error {
		h, _, st, err := readFile(env, root, model.SesshinName, model.ReadSesshin)
		switch st {
		case missing:
			return ErrNothingToRecord
		case unreadable, unusable, otherFmt:
			return err
		}
		if h.Placement == nil {
			return nil
		}
		next, changed := apply(h.Placement)
		if !changed {
			return nil
		}
		h.Placement = next
		return env.write(root, model.SesshinName, h)
	})
}
