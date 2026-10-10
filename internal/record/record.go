package record

import (
	"errors"
	"io/fs"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
	"github.com/phansen314/sesshin/internal/testhook"
	"github.com/phansen314/sesshin/internal/text"
)

// Env is what Record takes from its hook: hook.Call.RecordEnv fills it, and
// tests fill their own.
type Env struct {
	FS  fsys.FS
	Loc loc.Locations
	// SessionID is the payload's, a lowercased UUID: the session directory's
	// name.
	SessionID string
	// Now stamps the event, to the second.
	Now time.Time
	// LockWait is hook_lock_wait_ms: the longest any one lock is waited for.
	LockWait time.Duration
	// Deadline is the hook's lock deadline, on the monotonic clock
	// (hooks-spec.md, The contract, H4): every lock wait is the smaller of
	// LockWait and what is left of it. The zero time sets none, leaving each
	// wait bounded by LockWait alone.
	Deadline time.Time
	Getenv   func(string) string
	// Log appends a line to hooks.log under the hook's verb and session.
	Log func(string)
	// Lookup finds Claude's process for a late adoption; nil is proc.Find.
	Lookup func(fsy fsys.FS, claudePID string) proc.Claude
	// Placement is the terminal backend's: what it recognizes in the
	// environment, given the placement sesshin.json has now (nil when none),
	// which it may keep keys of that only its sync writes, even for another
	// window when resumed (SessionStart's source is resume). Nil places
	// nothing.
	// Record doesn't call it for a session started by another session, or
	// under tmux or screen: those are never placed.
	Placement func(old *jsonio.Object, resumed bool) *jsonio.Object
	// StartedAt is Adopt rule 3's process check: the current start time of a
	// pid, as live.StartedAt says. Nil is proc.StartedAt over FS.
	StartedAt live.StartedAt

	// sessionStart is set by Record for a SessionStart: the only hook that
	// logs a file in another format.
	sessionStart bool
}

// wait is how long to wait for the next lock: LockWait, or what is left of
// the deadline when that is less. Past the deadline it is 0, a single try.
func (e Env) wait() time.Duration {
	if e.Deadline.IsZero() {
		return e.LockWait
	}
	return max(0, min(e.LockWait, time.Until(e.Deadline)))
}

// constError is an error that can be a constant: sesshin-hook's packages have
// no package-level initializer that does work (implementation-spec.md, The
// hook binary).
type constError string

func (e constError) Error() string { return string(e) }

// ErrNothingToRecord is Record's error for a hook that can't adopt and finds
// no session directory, or no lifecycle.json. It is not logged: sesshin never
// knew the session. A lifecycle.json that is unusable or can't be read is
// logged, and returned as its own error.
const ErrNothingToRecord constError = "nothing to record"

// Record records ev, as Recording an event's six steps say: it locks the
// session directory, reads lifecycle.json, applies the clocks and the event's
// effects, writes it, completes or repairs sesshin.json, and unlocks. It returns
// why nothing was recorded, or nil; whatever it returns has been logged,
// ErrNothingToRecord excepted, and a file in another format, which only
// session-start logs. A hook that can't adopt records nothing when
// the session has no usable lifecycle.json: ErrNothingToRecord, with an
// unusable one logged. A lifecycle.json that can't be read, or is in another
// format, is left as it is, for every hook; the first is logged.
func Record(env Env, ev Event) error {
	if !ev.Kind.valid() {
		env.Log("unknown event kind")
		return errors.New("record: unknown event kind")
	}
	env.sessionStart = ev.Kind == SessionStart
	return env.withLocked(ev.Kind.adopts(), func(root fsys.Root) error {
		l, err := env.recordLifecycle(root, ev)
		if err != nil {
			return err
		}
		testhook.At("record:written")
		return env.completeSesshin(root, ev, &l)
	})
}

// lockSession is step 1: it opens the session directory, creating it, and
// its missing parents, for a hook that can adopt, and locks it. A path that
// no longer names the locked directory was renamed aside by a prune: it
// unlocks and tries again, once, with what is left of the deadline.
func (e Env) lockSession(adopt bool) (fsys.Root, fsys.Lock, error) {
	dir := e.Loc.SessionDir(e.SessionID)
	for attempt := 0; ; attempt++ {
		var root fsys.Root
		var err error
		if adopt {
			root, err = fsys.OpenRootCreate(e.FS, dir)
		} else if root, err = e.FS.OpenRoot(dir); errors.Is(err, fs.ErrNotExist) {
			return nil, nil, ErrNothingToRecord
		}
		if err != nil {
			return nil, nil, wrap("open session directory", err)
		}
		lock, err := root.Lock(e.wait())
		if err != nil {
			root.Close()
			return nil, nil, wrap("session lock", err)
		}
		moved, err := root.Moved()
		if err == nil && !moved {
			return root, lock, nil
		}
		lock.Unlock()
		root.Close()
		if err != nil {
			return nil, nil, wrap("session directory check", err)
		}
		if attempt == 1 {
			return nil, nil, errors.New("session directory moved again; event lost")
		}
	}
}

// recordLifecycle is steps 2 to 4: read lifecycle.json, apply the event, and
// write it. It returns what was written. A file that can't be read is
// logged and left as it is: replacing it would wipe its history.
func (e Env) recordLifecycle(root fsys.Root, ev Event) (model.LifecycleFile, error) {
	now := model.FormatTimestamp(e.Now)
	l, _, st, err := readFile(e, root, model.LifecycleName, func(data []byte) (model.LifecycleFile, model.FileResult) {
		return model.ReadLifecycle(data, e.SessionID)
	})
	if st == unreadable || st == otherFmt {
		// Another format is left alone, for every verb, adopting or not.
		return l, err
	}
	fresh := st != usable
	adopt := ev.Kind.adopts()
	if fresh {
		if !adopt {
			// A hook that can't adopt treats an unusable file as missing,
			// and writes nothing.
			return l, ErrNothingToRecord
		}
		l = e.newRecord(ev, now)
	}
	straggler := !fresh && ev.Kind.guarded() && ev.PromptID != "" &&
		l.EndedPromptID != nil && *l.EndedPromptID == text.Scrub(ev.PromptID)
	e.apply(&l, ev, now, straggler)
	return l, e.write(root, model.LifecycleName, l)
}

// wrap names what failed, for the log: `<what>: <error>`, where the error is
// the OS's own words for the errno (permission denied), without the path the
// error carries, since what already names the file. The error keeps its chain,
// so errors.Is and fsys.ErrnoOf still see through it.
func wrap(what string, err error) error { return &opError{what: what, err: err} }

// opError is an OS error with what the hook was doing.
type opError struct {
	what string
	err  error
}

func (e *opError) Error() string { return e.what + ": " + fsys.Describe(e.err) }
func (e *opError) Unwrap() error { return e.err }
