package ops

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

// The rules update raises (operations.md, Error kinds).
const (
	ruleNoSesshinFile = "no-sesshin-file"
	ruleExtraTooLarge = "extra-too-large"
)

// updateLockWait is how long update waits for the session's lock (design-spec.md,
// Locks).
const updateLockWait = 500 * time.Millisecond

// The values of no-sesshin-file's file.
const (
	fileMissing     = "missing"
	fileUnusable    = "unusable"
	fileOtherFormat = "other-format"
	filePending     = "pending"
)

// UpdateInput is update's input (update-input).
type UpdateInput struct {
	Selector Selector
	Extra    ExtraChange
}

// ExtraChange is one of two forms: ReplaceAll set, or Merge and Remove
// (either may be empty, not both).
type ExtraChange struct {
	ReplaceAll *jsonio.Object
	Merge      *jsonio.Object
	Remove     []string
}

// DecodeUpdateInput is update's own checks: session required, and a selector
// by the rules of Selecting a session; extra required, one of its two forms,
// merge and remove sharing no key, and replace_all and merge within extra's
// limits (operations.md, update).
func DecodeUpdateInput(f *model.Fields, p *model.Problems) UpdateInput {
	in := UpdateInput{Selector: decodeSelector(f, p)}
	if v, ok := f.Required("extra"); ok {
		in.Extra = decodeExtraChange(v, p)
	}
	return in
}

// decodeExtraChange checks update's extra: {replace_all} or {merge, remove}.
// The input evidently means the replace_all form when it has that key, and
// the merge/remove form otherwise; only that form's problems are reported.
func decodeExtraChange(v any, p *model.Problems) ExtraChange {
	var c ExtraChange
	f, ok := p.Object(v, "/extra")
	if !ok {
		return c
	}
	defer f.Done()
	if rv, has := f.Optional("replace_all"); has {
		c.ReplaceAll = decodeExtra(p, rv, "/extra/replace_all")
		for _, k := range []string{"merge", "remove"} {
			if _, ok := f.Optional(k); ok {
				p.Add(f.Ptr(k), "cannot be combined with replace_all")
			}
		}
		return c
	}
	mv, hasMerge := f.Optional("merge")
	rv, hasRemove := f.Optional("remove")
	if !hasMerge && !hasRemove {
		p.Add("/extra", "give replace_all, or merge and/or remove")
		return c
	}
	mergeOK, removeOK := true, true
	if hasMerge {
		c.Merge = decodeExtra(p, mv, "/extra/merge")
		mergeOK = c.Merge != nil
		if mergeOK && c.Merge.Len() == 0 {
			p.Add("/extra/merge", "must set at least one key")
			mergeOK = false
		}
	}
	if hasRemove {
		c.Remove, removeOK = removeKeys(p, rv, "/extra/remove")
	}
	if mergeOK && removeOK && c.Merge != nil {
		for i, k := range c.Remove {
			if _, dup := c.Merge.Get(k); dup {
				p.AddAdditional(jsonio.Pointer("/extra/remove", strconv.Itoa(i)), "is also in merge")
			}
		}
	}
	return c
}

// removeKeys checks extra.remove: a non-empty array of distinct strings. It
// returns them and whether they are valid.
func removeKeys(p *model.Problems, v any, ptr string) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		p.Add(ptr, "expected an array")
		return nil, false
	}
	if len(arr) == 0 {
		p.Add(ptr, "must name at least one key")
		return nil, false
	}
	var keys []string
	valid := true
	for i, item := range arr {
		iptr := jsonio.Pointer(ptr, strconv.Itoa(i))
		s, ok := p.String(item, iptr)
		switch {
		case !ok:
			valid = false
		case slices.Contains(keys, s):
			p.Add(iptr, "duplicate item")
			valid = false
		default:
			keys = append(keys, s)
		}
	}
	return keys, valid
}

// apply is the extra after the change: old's members in place, merge's keys
// set (new ones appended in the order given), remove's deleted.
func (c ExtraChange) apply(old *jsonio.Object) *jsonio.Object {
	if c.ReplaceAll != nil {
		return c.ReplaceAll
	}
	extra := &jsonio.Object{Members: old.Members} // Set and Delete never write to Members
	if c.Merge != nil {
		for _, m := range c.Merge.Members {
			extra.Set(m.Key, m.Value)
		}
	}
	for _, k := range c.Remove {
		extra.Delete(k)
	}
	return extra
}

// UpdateOutput is update's result (update-output).
type UpdateOutput struct {
	Session SessionView `json:"session"`
	Changed []string    `json:"changed"`
}

// updateOp changes a session's extra under its lock (operations.md, update).
// It never takes the state lock, and never creates sesshin.json.
func updateOp(in UpdateInput, env ReadEnv) Envelope {
	set, l, e := readSessions(env)
	if e != nil {
		return Failed(e)
	}
	warnings := issueWarnings(set)
	fail := func(e *Error) Envelope { return FailedWith(e, warnings) }

	vw := viewer{fs: env.FS, now: set.now}
	in.Selector = in.Selector.resolveSelf(env, set.recs)
	rec, e := selectOne(in.Selector, set.recs, vw)
	if e != nil {
		return fail(e)
	}
	v := vw.view(rec)

	changed, e := updateLocked(in, env, l.SessionsDir(), rec, v)
	if e != nil {
		return fail(e)
	}

	// The output is read after the lock is released.
	set, _, e = readSessions(env)
	if e != nil {
		return fail(e)
	}
	for _, r := range set.recs {
		if r.ID == rec.ID {
			vw = viewer{fs: env.FS, now: set.now}
			res := Succeeded(UpdateOutput{Session: vw.view(r), Changed: changed})
			res.Warnings = append(res.Warnings, warnings...)
			for _, is := range set.issues {
				res.Warnings = appendNew(res.Warnings, issueWarning(is))
			}
			return res
		}
	}
	return fail(missingSession(in.Selector.Raw, rec.ID))
}

// updateLocked is steps 2 to 5: lock the session's directory, read
// sesshin.json again, change its extra, and write it unless it is unchanged.
// It returns the fields changed.
func updateLocked(in UpdateInput, env ReadEnv, sessionsDir string, rec *sessionRec, v SessionView) ([]string, *Error) {
	dir := filepath.Join(sessionsDir, rec.ID)
	sessions, err := env.FS.OpenRoot(sessionsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, missingSession(in.Selector.Raw, rec.ID)
	}
	if err != nil {
		return nil, IOError(sessionsDir, err)
	}
	defer sessions.Close()
	sroot, err := sessions.OpenRoot(rec.ID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, missingSession(in.Selector.Raw, rec.ID) // pruned since the read
	}
	if err != nil {
		return nil, IOError(dir, err)
	}
	defer sroot.Close()
	lock, err := sroot.Lock(updateLockWait)
	if lockHeld(err) {
		return nil, &Error{
			Kind:    KindBusy,
			Message: "the lock of session " + rec.ID + " was held for " + updateLockWait.String(),
			Details: map[string]any{"lock": "session", "session_id": rec.ID},
		}
	}
	if err != nil {
		return nil, IOError(dir, err)
	}
	defer lock.Unlock()
	// A prune that renamed the directory aside while update waited leaves the
	// descriptor on a directory that is no longer there.
	if moved, err := sroot.Moved(); err != nil {
		return nil, IOError(dir, err)
	} else if moved {
		return nil, missingSession(in.Selector.Raw, rec.ID)
	}

	path := filepath.Join(dir, model.SesshinName)
	data, err := sroot.ReadFile(model.SesshinName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, noSesshinFile(v, rec.ID, fileMissing, path)
	}
	if err != nil {
		return nil, IOError(path, err)
	}
	h, res := model.ReadSesshin(data)
	switch {
	case res.OtherFormat:
		return nil, otherFormat(v, rec.ID, res.Found, path)
	case !res.Usable:
		return nil, noSesshinFile(v, rec.ID, fileUnusable, path)
	case h.ID == nil:
		return nil, noSesshinFile(v, rec.ID, filePending, path)
	}

	extra := in.Extra.apply(h.Extra)
	if why := model.ExtraProblem(extra); why != "" {
		return nil, &Error{
			Kind:    KindConflict,
			Message: "the extra of session " + v.Name + " would break its limits: " + why,
			Details: map[string]any{"rule": ruleExtraTooLarge, "sessions": []SessionRef{v.ref()}},
		}
	}
	if jsonio.Equal(extra, h.Extra) {
		return []string{}, nil
	}
	h.Extra = extra
	out, err := jsonio.MarshalFile(h)
	if err != nil {
		return nil, internal("encoding %s: %v", path, err)
	}
	if err := fsys.Publish(sroot, model.SesshinName, out); err != nil {
		return nil, IOError(path, err)
	}
	return []string{"extra"}, nil
}

// missingSession is not-found for the session sel selected, id, whose
// directory was pruned while update waited for its lock.
func missingSession(sel, id string) *Error {
	return &Error{
		Kind:    KindNotFound,
		Message: "session " + id + " is gone: its directory was pruned",
		Details: map[string]any{"selectors": []string{sel}, "paths": []string{}},
	}
}

// noSesshinFile is conflict no-sesshin-file: the session has no sesshin.json
// update can change. The message names the way out by file and by the
// session's liveness (operations.md, update).
func noSesshinFile(v SessionView, uuid, file, path string) *Error {
	name := "session " + uuid[:8]
	ended := v.Liveness == "ended"
	var msg string
	switch file {
	case fileMissing:
		if ended {
			msg = name + " has no sesshin.json (it ended before any hook wrote one): run sesshin resume " + uuid + ", then retry"
		} else {
			msg = name + " has no sesshin.json yet (it is " + v.Liveness + ", and its first hook hasn't written one): retry after its next prompt"
		}
	case fileUnusable:
		if ended {
			msg = name + "'s sesshin.json is corrupt, and no hook of an ended session will rewrite it: run sesshin resume " + uuid + ", then retry"
		} else {
			msg = name + "'s sesshin.json is corrupt: retry after its next prompt, which writes it afresh"
		}
	default:
		if ended {
			msg = name + " has no sesshin ID yet (its ID was never issued), and no hook of an ended session will complete it: run sesshin resume " + uuid + ", then retry"
		} else {
			msg = name + " has no sesshin ID yet (its ID couldn't be issued, and its reservation's extra may still arrive): retry after its next prompt, which completes it"
		}
	}
	return noSesshinFileError(v, file, path, msg)
}

// otherFormat is conflict no-sesshin-file for a sesshin.json in another
// format, found: no hook rewrites it, so neither a prompt nor a resume helps.
// An older one waits for migrate, and a newer one for a newer sesshin.
func otherFormat(v SessionView, uuid string, found int64, path string) *Error {
	name := "session " + uuid[:8]
	format := strconv.FormatInt(found, 10)
	msg := name + "'s sesshin.json is in format " + format + ", older than this sesshin's: run sesshin migrate, then retry"
	if found > model.SesshinSchema {
		msg = name + "'s sesshin.json is in format " + format + ", newer than this sesshin's: upgrade sesshin, then retry"
	}
	return noSesshinFileError(v, fileOtherFormat, path, msg)
}

// noSesshinFileError is conflict no-sesshin-file with its details.
func noSesshinFileError(v SessionView, file, path, msg string) *Error {
	return &Error{
		Kind:    KindConflict,
		Message: msg,
		Details: map[string]any{
			"rule":     ruleNoSesshinFile,
			"file":     file,
			"path":     path,
			"sessions": []SessionRef{v.ref()},
		},
	}
}
