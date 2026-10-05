package ops

import (
	"cmp"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
)

// fileIssue is a session file that wasn't usable.
type fileIssue struct {
	File string // lifecycle.json, sesshin.json, or statusline.json
	Path string
	// Missing: the file isn't there (only lifecycle.json is reported so; the
	// other two are optional).
	Missing bool
	// Why says what is wrong with a file that is there.
	Why string
}

// sessionFiles is what one session directory held.
type sessionFiles struct {
	live.Session
	// Sesshin is sesshin.json, nil when missing or unusable, and when it wasn't
	// asked for.
	Sesshin *model.SesshinFile
	Issues  []fileIssue
}

// readSessionFiles reads the session's files through its root: lifecycle.json
// and statusline.json, and sesshin.json when withSesshin. A file that is missing or
// unusable is nil; the issues say which, in that order. lifecycle.json's
// being missing is an issue, as the others' being missing is not. gone is a
// session whose directory vanished as it was read: pruned by another run,
// skipped. dir is the session's path, for the issues' paths.
func readSessionFiles(sroot fsys.Root, dir, id string, withSesshin bool) (sf sessionFiles, gone bool, e *Error) {
	sf.ID = id
	add := func(is *fileIssue) {
		if is != nil {
			sf.Issues = append(sf.Issues, *is)
		}
	}

	is, missing, e := readOne(sroot, dir, model.LifecycleName, func(b []byte) (bool, string) {
		l, r := model.ReadLifecycle(b, id)
		if r.Usable {
			sf.Lifecycle = &l
		}
		return r.Usable, r.Reason()
	})
	if e != nil {
		return sf, false, e
	}
	if missing {
		if moved, merr := sroot.Moved(); merr == nil && moved {
			return sf, true, nil
		}
		is = &fileIssue{File: model.LifecycleName, Path: filepath.Join(dir, model.LifecycleName), Missing: true}
	}
	add(is)

	if withSesshin {
		is, _, e = readOne(sroot, dir, model.SesshinName, func(b []byte) (bool, string) {
			h, r := model.ReadSesshin(b)
			if r.Usable {
				sf.Sesshin = &h
			}
			return r.Usable, r.Reason()
		})
		if e != nil {
			return sf, false, e
		}
		add(is)
	}

	is, _, e = readOne(sroot, dir, model.StatuslineName, func(b []byte) (bool, string) {
		st, r := model.ReadStatusline(b)
		if r.Usable {
			sf.Statusline = &st
		}
		return r.Usable, r.Reason()
	})
	if e != nil {
		return sf, false, e
	}
	add(is)
	return sf, false, nil
}

// readOne reads one file of a session through its root and has parse judge
// it: parse stores a usable file and says whether it was, with the reason if
// not. The issue is the file's being unusable, or too large or a directory;
// missing is that it isn't there, for the caller to treat as it does; e is
// any other read failure.
func readOne(sroot fsys.Root, dir, name string, parse func([]byte) (ok bool, why string)) (issue *fileIssue, missing bool, e *Error) {
	path := filepath.Join(dir, name)
	b, err := sroot.ReadFile(name)
	switch {
	case err == nil:
		if ok, why := parse(b); !ok {
			issue = &fileIssue{File: name, Path: path, Why: why}
		}
	case errors.Is(err, fs.ErrNotExist):
		missing = true
	case unusableRead(err):
		issue = &fileIssue{File: name, Path: path, Why: err.Error()}
	default:
		e = IOError(path, err)
	}
	return issue, missing, e
}

// unusableRead reports a read failure that makes the file unusable, like a
// malformed one, rather than an OS error to report: a file past MaxRead, or
// a directory in its place.
func unusableRead(err error) bool {
	errno, ok := fsys.ErrnoOf(err)
	return ok && (errno == syscall.EFBIG || errno == syscall.EISDIR)
}

// lockHeld reports that a lock was refused because another holds it.
func lockHeld(err error) bool {
	errno, ok := fsys.ErrnoOf(err)
	return ok && errno == syscall.EAGAIN
}

// isSessionEntry reports whether a directory entry of sessions/ is a session
// directory: a UUID-named directory, not a leftover or a hidden one.
func isSessionEntry(ent fs.DirEntry) bool {
	id := ent.Name()
	return !strings.HasPrefix(id, ".") && ent.IsDir() && model.IsUUID(id)
}

// sessionRec is a session with a usable lifecycle.json, and what was
// derived of it.
type sessionRec struct {
	sessionFiles
	res live.Result
	// job is the reported job (design-spec.md, Reservations): the stored one,
	// unless a live session that started earlier holds it. nil for none.
	job *string
}

// sesshinID is the session's sesshin ID, nil without a usable sesshin.json or while
// an issue is pending.
func (r *sessionRec) sesshinID() *int64 {
	if r.Sesshin == nil {
		return nil
	}
	return r.Sesshin.ID
}

func (r *sessionRec) isHeadless() bool { return headless(r.Lifecycle) }

// sessionIssue is a fileIssue of the session session.
type sessionIssue struct {
	session string
	fileIssue
}

// sessionSet is every session read: those with a usable lifecycle.json, in
// session order, and every issue found, in directory order.
type sessionSet struct {
	now    time.Time
	recs   []*sessionRec
	issues []sessionIssue
}

// readSessions is the shared rules' Reading the sessions (operations.md):
// every session directory under sessions/, with liveness
// derived over all of them together. A missing state directory or sessions/
// is no sessions.
func readSessions(env ReadEnv) (*sessionSet, loc.Locations, *Error) {
	l, e := resolveLocations(env.GOOS, env.Getenv)
	if e != nil {
		return nil, l, e
	}
	root, err := env.FS.OpenRoot(l.SessionsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return &sessionSet{}, l, nil
	}
	if err != nil {
		return nil, l, IOError(l.SessionsDir(), err)
	}
	defer root.Close()
	set, e := readSessionsFrom(env, l, root)
	return set, l, e
}

// readSessionsFrom is the walk of readSessions over sessions/ opened as root,
// which the caller may hold the state lock on: spawn's claim runs it so.
func readSessionsFrom(env ReadEnv, l loc.Locations, root fsys.Root) (*sessionSet, *Error) {
	set := &sessionSet{}
	entries, err := root.ReadDir(".")
	if err != nil {
		return nil, IOError(l.SessionsDir(), err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })

	var files []sessionFiles
	for _, ent := range entries {
		id := ent.Name()
		if !isSessionEntry(ent) {
			continue
		}
		sroot, err := root.OpenRoot(id)
		if errors.Is(err, fs.ErrNotExist) {
			continue // pruned since the listing
		}
		if err != nil {
			return nil, IOError(l.SessionDir(id), err)
		}
		sf, gone, e := readSessionFiles(sroot, l.SessionDir(id), id, true)
		sroot.Close()
		if e != nil {
			return nil, e
		}
		if gone {
			continue
		}
		for _, is := range sf.Issues {
			if is.Missing {
				continue // a session being created, or a leftover: silent
			}
			set.issues = append(set.issues, sessionIssue{id, is})
		}
		if sf.Lifecycle != nil {
			files = append(files, sf)
		}
	}

	set.now, set.recs = deriveRecs(env, files)
	return set, nil
}

// deriveRecs derives liveness and the reported jobs over the sessions read
// together, and returns them in session order, with the clock read once for
// all.
func deriveRecs(env ReadEnv, files []sessionFiles) (time.Time, []*sessionRec) {
	sessions := make([]live.Session, len(files))
	for i, sf := range files {
		sessions[i] = sf.Session
	}
	now := env.Now().UTC()
	results := live.Derive(sessions, now, env.StartedAt)
	stored := make([]*string, len(files))
	for i, sf := range files {
		if sf.Sesshin != nil {
			stored[i] = sf.Sesshin.Job
		}
	}
	reported := live.Jobs(sessions, results, stored)
	var recs []*sessionRec
	for i, sf := range files {
		recs = append(recs, &sessionRec{sessionFiles: sf, res: results[i], job: reported[i]})
	}
	slices.SortFunc(recs, compareOrder)
	return now, recs
}

// readSession reads one session's directory alone, as the wait of resume
// does: nil rec when it has no usable lifecycle.json or has vanished. Its
// liveness and job are derived over it alone.
func readSession(env ReadEnv, l loc.Locations, id string) (*sessionSet, *sessionRec, *Error) {
	root, err := env.FS.OpenRoot(l.SessionDir(id))
	if errors.Is(err, fs.ErrNotExist) {
		return &sessionSet{now: env.Now().UTC()}, nil, nil
	}
	if err != nil {
		return nil, nil, IOError(l.SessionDir(id), err)
	}
	defer root.Close()
	sf, gone, e := readSessionFiles(root, l.SessionDir(id), id, true)
	if e != nil {
		return nil, nil, e
	}
	set := &sessionSet{}
	if gone {
		set.now = env.Now().UTC()
		return set, nil, nil
	}
	for _, is := range sf.Issues {
		if !is.Missing {
			set.issues = append(set.issues, sessionIssue{id, is})
		}
	}
	if sf.Lifecycle == nil {
		set.now = env.Now().UTC()
		return set, nil, nil
	}
	set.now, set.recs = deriveRecs(env, []sessionFiles{sf})
	return set, set.recs[0], nil
}

// compareOrder is session order (operations.md): live sessions, liveness
// unknown included, then ended ones; within each, the most recently seen
// first; ties by sesshin ID, lowest first and none last, then by session_id.
func compareOrder(a, b *sessionRec) int {
	rank := func(r *sessionRec) int {
		if r.res.State == live.Ended {
			return 1
		}
		return 0
	}
	return cmp.Or(
		cmp.Compare(rank(a), rank(b)),
		cmp.Compare(b.res.LastSeen, a.res.LastSeen),
		compareSesshinID(a.sesshinID(), b.sesshinID()),
		strings.Compare(a.ID, b.ID),
	)
}

func compareSesshinID(a, b *int64) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return cmp.Compare(*a, *b)
}

// issueWarnings is the unusable-file warning for each issue of a read.
func issueWarnings(set *sessionSet) []Warning {
	var ws []Warning
	for _, is := range set.issues {
		ws = append(ws, issueWarning(is))
	}
	return ws
}

// issueWarning is the unusable-file warning for an issue.
func issueWarning(is sessionIssue) Warning {
	var effect string
	switch is.File {
	case model.LifecycleName:
		effect = "the session is left out"
	case model.SesshinName:
		effect = "its sesshin ID, job, source, and placement read as null"
	default:
		effect = "its metrics and prompt cache read as null"
	}
	return Warning{
		Kind:    KindUnusableFile,
		Message: is.Path + ": " + is.File + " is unusable: " + is.Why + "; " + effect,
		Details: map[string]any{"path": is.Path},
	}
}
