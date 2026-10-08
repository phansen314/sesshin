package record

import (
	"errors"
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/testhook"
)

// completeSesshin is step 5, under the session lock: it creates sesshin.json when
// it is missing or corrupt, with a new ID, leaves one in another format alone,
// and completes one whose id is null. A file that has an ID is left alone, but for
// SessionStart, which replaces its placement; so does a SessionStart that
// completes a null id, since the placement it keeps would be the last
// window's. This is the only code that reads sesshin.json for recording, and
// nothing it reads is written into lifecycle.json (Two tiers): recordLifecycle
// has finished by now, and takes only the nested it hands over. A new file, or
// a completed one, has its job decided by the Adopt rules (adopt.go), under
// the state lock that issues its ID; a SessionStart that finds a usable file
// then adopts its reservation (adoptResume).
func (e Env) completeSesshin(root fsys.Root, ev Event, l *model.LifecycleFile) error {
	h, data, st, err := readFile(e, root, model.SesshinName, model.ReadSesshin)
	if st == unreadable || st == otherFmt {
		// Another format is left alone: no ID, no placement, no adoption.
		// It exists but can't be read (a directory in its place, a denied
		// permission): writing a replacement would fail the same way, after
		// state.json had issued an ID, so every hook would burn one. Issue
		// nothing; the next hook tries again.
		return err
	}
	have := st == usable
	if ev.Kind == SessionStart {
		e.logJobEnv()
	}
	err = e.writeSesshin(root, ev, l, h, data, have)
	if have && ev.Kind == SessionStart {
		e.adoptResume(root)
	}
	return err
}

// writeSesshin is completeSesshin's creating, completing, and replacing of
// placement: h is sesshin.json as read, data its bytes, and have says it is
// usable.
func (e Env) writeSesshin(root fsys.Root, ev Event, l *model.LifecycleFile, h model.SesshinFile, data []byte, have bool) error {
	if have && h.ID != nil {
		if !ev.Kind.replacesPlacement() {
			return nil
		}
		h.Placement = e.placement(l.Nested, h.Placement, ev.Source)
		return e.writeIfChanged(root, model.SesshinName, h, data)
	}
	// A new file takes the backend's placement, and an extra of {} unless the
	// Adopt rules give it a reservation's. A completed one keeps its own
	// extra, but for SessionStart, which replaces the placement as it does a
	// file with an ID.
	var placement *jsonio.Object
	switch {
	case !have:
		placement = e.placement(l.Nested, nil, ev.Source)
		h.Extra = &jsonio.Object{}
	case ev.Kind.replacesPlacement():
		placement = e.placement(l.Nested, h.Placement, ev.Source)
	default:
		placement = h.Placement
	}
	return e.issueID(root, l.Nested, have, h, placement, data)
}

// placement is the backend's placement for the session, nil when there is no
// backend yet, or the session is never placed: started by another session
// (nested, which lifecycle.json records so a later async hook needs no lookup
// of its own), or under tmux or screen, whose variables name some other
// window (design-spec.md, Placement). nested unknown does not stop the
// backend. source is SessionStart's, which the backend needs to know a resume.
func (e Env) placement(nested *bool, old *jsonio.Object, source string) *jsonio.Object {
	if e.Placement == nil || nested != nil && *nested || placement.Multiplexed(e.Getenv) {
		return nil
	}
	return e.Placement(old, source == "resume")
}

// issueID is Creating sesshin.json: take the state lock (the lock on sessions/),
// issue last_id + 1, write sesshin.json with it, and release the lock, in that
// order, with the job decided in between by the Adopt rules, and the
// reservation they matched removed after the write, before the unlock. nested
// is lifecycle.json's. have says sesshin.json exists and is usable, with a null
// id and the placement given; data is its bytes. A file that already names a
// job keeps it, and its source: the job is decided once. When the ID can't be issued (the lock's
// wait ran out, state.json couldn't be read or written, or sessions/ couldn't
// be listed), the cause is logged and returned, and a missing or unusable
// sesshin.json is written with a null id (state.json in another format, or a
// rebuild that finds a sesshin.json in another format, are such causes), for the next lifecycle hook to
// complete; one that exists keeps its null id, and takes the placement given
// if that changed. The session lock is held throughout, so the order is
// always session lock, then state lock.
func (e Env) issueID(root fsys.Root, nested *bool, have bool, old model.SesshinFile, placement *jsonio.Object, data []byte) error {
	sessions, err := e.FS.OpenRoot(e.Loc.SessionsDir())
	if err != nil {
		return e.pending(root, have, old, placement, data, e.logErr(wrap("open sessions", err)))
	}
	defer sessions.Close()
	lock, err := sessions.Lock(e.wait())
	if err != nil {
		return e.pending(root, have, old, placement, data, e.logErr(wrap("state lock", err)))
	}
	defer lock.Unlock()
	testhook.At("record:state-locked")
	id, err := e.nextID(sessions)
	if err != nil {
		return e.pending(root, have, old, placement, data, err)
	}
	h := kept(old, &id, placement)
	if old.Job != nil {
		return e.write(root, model.SesshinName, h)
	}
	d := e.decideJob(sessions, nested)
	h.Job, h.Source = d.job, d.source
	if d.extra != nil {
		h.Extra = d.extra
	}
	err = e.write(root, model.SesshinName, h)
	d.release(e, err == nil)
	return err
}

// pending is the way out of an ID that couldn't be issued, whose cause has
// been logged: a missing or unusable sesshin.json is written with a null id, and
// one that has it keeps it, but takes the placement given. It returns the
// write's error if that failed, else cause.
func (e Env) pending(root fsys.Root, have bool, old model.SesshinFile, placement *jsonio.Object, data []byte, cause error) error {
	err := e.writeIfChanged(root, model.SesshinName, kept(old, nil, placement), data)
	switch {
	case err != nil:
		e.Log(model.SesshinName + " not written")
		return err
	case have:
		e.Log(model.SesshinName + " keeps no id")
	default:
		e.Log(model.SesshinName + " written without an id")
	}
	return cause
}

// kept is the sesshin.json to write for a new or completed file: old is the
// file as read, zero when none was usable, and keeps its job and source. A
// new file has no job and source hook until the Adopt rules decide them; so
// has one whose ID couldn't be issued (hooks-spec.md, Creating sesshin.json,
// step 3 and When the ID can't be issued).
func kept(old model.SesshinFile, id *int64, placement *jsonio.Object) model.SesshinFile {
	h := model.SesshinFile{ID: id, Job: old.Job, Source: old.Source, Placement: placement, Extra: old.Extra}
	if h.Source == "" {
		h.Source = model.SourceHook
	}
	return h
}

// nextID issues the next sesshin ID, under the state lock the caller holds:
// state.json is written with last_id + 1, flushed. A state.json that is
// missing or unusable restarts from 0 on a first run, and otherwise from the
// highest id in any sesshin.json (design-spec.md, Sesshin IDs). One that can't be
// read issues nothing: it may hold a last_id this hook can't see. Every error
// it returns has been logged.
func (e Env) nextID(sessions fsys.Root) (int64, error) {
	state, err := e.FS.OpenRoot(e.Loc.StateDir)
	if err != nil {
		return 0, e.logErr(wrap("open state directory", err))
	}
	defer state.Close()
	s, _, st, err := readFile(e, state, model.StateName, model.ReadState)
	if st == unreadable || st == otherFmt {
		// Another format may hold a last_id this hook can't see: never rebuilt.
		return 0, err
	}
	last, rebuilt, migration := s.LastID, int64(-1), s.Migration
	if st != usable {
		last, migration = 0, 0
		others, foreign, highest, err := e.scanIDs(sessions)
		if err == nil && foreign {
			// A sesshin.json in another format has an id this hook can't
			// read: a rebuild could reissue it.
			err = errors.New("last_id not rebuilt: " + model.SesshinName + " in another format")
			e.logFormat(err)
			return 0, err
		}
		if err != nil {
			// Without the listing, a first run can't be told from a lost
			// state.json: issuing from 0 could duplicate an ID in use.
			return 0, e.logErr(wrap("list sessions", err))
		}
		if others {
			last, rebuilt = highest, highest
		} else {
			migration = model.LatestMigration // a first run
		}
	}
	id := last + 1
	out, err := jsonio.MarshalFile(model.StateFile{LastID: id, Migration: migration})
	if err == nil {
		err = fsys.PublishSynced(state, model.StateName, out)
	}
	if err != nil {
		return 0, e.logErr(wrap("write "+model.StateName, err))
	}
	if rebuilt >= 0 {
		e.Log("last_id rebuilt from " + strconv.FormatInt(rebuilt, 10))
	}
	return id, nil
}

// scanIDs reports whether sessions/ holds a session other than this one (a
// directory, not a hidden leftover), whether any sesshin.json there is in
// another format (foreign), and the highest id in any usable one, 0 when none
// has one. It fails when sessions/ can't be
// listed.
func (e Env) scanIDs(sessions fsys.Root) (others, foreign bool, highest int64, err error) {
	entries, err := sessions.ReadDir(".")
	if err != nil {
		return false, false, 0, err
	}
	for _, d := range entries {
		name := d.Name()
		if !d.IsDir() || strings.HasPrefix(name, ".") || name == e.SessionID {
			continue
		}
		others = true
		data, err := sessions.ReadFile(name + "/" + model.SesshinName)
		if err != nil {
			continue
		}
		h, r := model.ReadSesshin(data)
		switch {
		case r.OtherFormat:
			foreign = true
		case r.Usable && h.ID != nil:
			highest = max(highest, *h.ID)
		}
	}
	return others, foreign, highest, nil
}
