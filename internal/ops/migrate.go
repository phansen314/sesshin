package ops

import (
	"cmp"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/migrate"
	"github.com/phansen314/sesshin/internal/model"
)

// migrateLockWait is how long migrate waits for each lock; a test shortens it.
var migrateLockWait = 2 * time.Second

// MigrateInput is migrate's input (migrate-input).
type MigrateInput struct{ DryRun bool }

// DecodeMigrateInput is migrate's own checks: dry_run, a boolean.
func DecodeMigrateInput(f *model.Fields, p *model.Problems) MigrateInput {
	return MigrateInput{DryRun: dryRun(f, p)}
}

// MigrateOutput is migrate's result (migrate-output).
type MigrateOutput struct {
	DryRun      bool               `json:"dry_run"`
	From        int64              `json:"from"`
	To          int64              `json:"to"`
	Applied     []AppliedStep      `json:"applied"`
	Converted   []ConvertedSession `json:"converted"`
	Unconverted []UnconvertedFile  `json:"unconverted"`
}

// AppliedStep is one step migrate ran.
type AppliedStep struct {
	Step int    `json:"step"`
	Name string `json:"name"`
}

// ConvertedSession is one session migrate converted files of, or with dry_run
// would.
type ConvertedSession struct {
	SessionID string   `json:"session_id"`
	ID        *int64   `json:"id"`
	Files     []string `json:"files"`
}

// UnconvertedFile is a file in an older format that the steps couldn't convert.
type UnconvertedFile struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

// migrator is one run of migrate.
type migrator struct {
	env      ReadEnv
	l        loc.Locations
	dryRun   bool
	out      *MigrateOutput
	warnings []Warning
	// foreign is the sesshin.json files left in another format after step 2
	// (newer, or older and unconverted), whose ids a rebuild of state.json
	// can't read.
	foreign []foreignFile
}

// foreignFile is one such file; listed says step 2 already put it in
// unconverted.
type foreignFile struct {
	path, detail string
	listed       bool
}

// Migrate converts the state directory's files to this binary's formats
// (operations.md, migrate; design-spec.md, Migrations): every session's
// files under its session lock, one at a time, then state.json under the
// state lock, never holding both.
func Migrate(in MigrateInput, env ReadEnv) Envelope {
	l, e := resolveLocations(env.GOOS, env.Getenv)
	if e != nil {
		return Failed(e)
	}
	out := &MigrateOutput{
		DryRun: in.DryRun, To: model.LatestMigration,
		Applied: []AppliedStep{}, Converted: []ConvertedSession{}, Unconverted: []UnconvertedFile{},
	}
	m := &migrator{env: env, l: l, dryRun: in.DryRun, out: out}
	if e := m.run(); e != nil {
		return FailedWith(e, m.warnings)
	}
	res := Succeeded(*out)
	res.Warnings = append(res.Warnings, m.warnings...)
	return res
}

func (m *migrator) statePath() string { return filepath.Join(m.l.StateDir, model.StateName) }

func (m *migrator) run() *Error {
	// Step 1.
	data, err := m.env.FS.ReadFile(m.statePath())
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return IOError(m.statePath(), err)
	}
	from := int64(0)
	if !missing {
		s, r := model.ReadState(data)
		switch {
		case r.Usable:
			if e := m.checkMigration(s.Migration); e != nil {
				return e
			}
			from = s.Migration
		case r.OtherFormat && r.Found > model.StateSchema:
			return m.unsupported("schema", r.Found, model.StateSchema)
		case r.OtherFormat: // schema 1 or older: records 0
		default:
			missing = true // corrupt
		}
	}

	sessions, e := m.openSessions()
	if e != nil {
		return e
	}
	if sessions != nil {
		defer sessions.Close()
	}
	if missing {
		has, e := m.hasSessions(sessions)
		if e != nil {
			return e
		}
		if !has {
			m.out.From = model.LatestMigration
			return nil
		}
	}
	m.out.From = from
	if from >= model.LatestMigration {
		return nil
	}
	for _, s := range migrate.Steps {
		if int64(s.N) > from {
			m.out.Applied = append(m.out.Applied, AppliedStep{Step: s.N, Name: s.Name})
		}
	}

	// Step 2.
	if sessions != nil {
		if e := m.sessions(sessions); e != nil {
			return e
		}
	}

	// Step 3: every session lock is released.
	e = m.record(sessions)
	slices.SortFunc(m.out.Unconverted, func(a, b UnconvertedFile) int { return cmp.Compare(a.Path, b.Path) })
	return e
}

// checkMigration fails unsupported-format for a step past this binary's
// latest.
func (m *migrator) checkMigration(n int64) *Error {
	if n > model.LatestMigration {
		return m.unsupported("migration", n, model.LatestMigration)
	}
	return nil
}

func (m *migrator) unsupported(field string, found, supported int64) *Error {
	return &Error{
		Kind:    KindUnsupportedFormat,
		Message: m.statePath() + ": " + field + " " + itoa(found) + " is past the " + itoa(supported) + " this binary supports; use a newer binary",
		Details: map[string]any{"path": m.statePath(), "field": field, "found": found, "supported": supported},
	}
}

// openSessions opens sessions/, nil when it doesn't exist.
func (m *migrator) openSessions() (fsys.Root, *Error) {
	root, err := m.env.FS.OpenRoot(m.l.SessionsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, IOError(m.l.SessionsDir(), err)
	}
	return root, nil
}

// sessionIDs lists the visible UUID-named directories of sessions/, in UUID
// order.
func (m *migrator) sessionIDs(sessions fsys.Root) ([]string, *Error) {
	if sessions == nil {
		return nil, nil
	}
	entries, err := sessions.ReadDir(".")
	if err != nil {
		return nil, IOError(m.l.SessionsDir(), err)
	}
	var ids []string
	for _, ent := range entries {
		if isSessionEntry(ent) {
			ids = append(ids, ent.Name())
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (m *migrator) hasSessions(sessions fsys.Root) (bool, *Error) {
	ids, e := m.sessionIDs(sessions)
	return len(ids) > 0, e
}

// sessions is step 2.
func (m *migrator) sessions(sessions fsys.Root) *Error {
	ids, e := m.sessionIDs(sessions)
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e := m.session(sessions, id); e != nil {
			return e
		}
	}
	return nil
}

// session converts one session's files under its lock.
func (m *migrator) session(sessions fsys.Root, id string) *Error {
	dir := m.l.SessionDir(id)
	sroot, err := sessions.OpenRoot(id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // pruned since the listing
	}
	if err != nil {
		return IOError(dir, err)
	}
	defer sroot.Close()
	lock, err := sroot.Lock(migrateLockWait)
	if lockHeld(err) {
		return &Error{
			Kind:    KindBusy,
			Message: "the lock of session " + id + " was held for " + migrateLockWait.String(),
			Details: map[string]any{"lock": "session", "session_id": id},
		}
	}
	if err != nil {
		return IOError(dir, err)
	}
	defer lock.Unlock()
	if moved, err := sroot.Moved(); err != nil {
		return IOError(dir, err)
	} else if moved {
		return nil // pruned meanwhile
	}

	// The files a pending step covers: sesshin.json.
	name := model.SesshinName
	path := filepath.Join(dir, name)
	data, err := sroot.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return IOError(path, err)
	}
	conv, changed, err := migrate.Convert(migrate.Sesshin, data)
	var me *migrate.Error
	switch {
	case errors.As(err, &me):
		switch me.Kind {
		case migrate.Newer:
			m.warnUnusable(path, "in format "+itoa(me.Found)+", newer than this binary's; left alone")
			m.foreign = append(m.foreign, foreignFile{path: path, detail: "in format " + itoa(me.Found) + ", newer than this binary's"})
		case migrate.Unconverted:
			m.unconverted(path, me.Detail)
			m.foreign = append(m.foreign, foreignFile{path: path, detail: me.Detail, listed: true})
		}
		return nil // corrupt: a hook replaces it
	case err != nil:
		return &Error{Kind: KindInternal, Message: path + ": " + err.Error()}
	case !changed:
		return nil
	}
	item := ConvertedSession{SessionID: id, Files: []string{name}}
	if h, r := model.ReadSesshin(conv); r.Usable {
		item.ID = h.ID
	}
	if !m.dryRun {
		if err := fsys.Publish(sroot, name, conv); err != nil {
			return IOError(path, err)
		}
	}
	m.out.Converted = append(m.out.Converted, item)
	return nil
}

func (m *migrator) warnUnusable(path, msg string) {
	m.warnings = append(m.warnings, Warning{
		Kind:    KindUnusableFile,
		Message: path + ": " + msg,
		Details: map[string]any{"path": path, "reason": ReasonUnsupportedFormat},
	})
}

func (m *migrator) unconverted(path, detail string) {
	m.out.Unconverted = append(m.out.Unconverted, UnconvertedFile{Path: path, Detail: detail})
	m.warnUnusable(path, "could not be converted: "+detail+"; left as it is")
}

// record is step 3: state.json under the state lock.
func (m *migrator) record(sessions fsys.Root) *Error {
	// A dry run with no sessions/ has nothing to lock and nothing to write,
	// but still reads state.json and runs the same checks.
	if sessions != nil || !m.dryRun {
		if sessions == nil {
			root, err := fsys.OpenRootCreate(m.env.FS, m.l.SessionsDir())
			if err != nil {
				return IOError(m.l.SessionsDir(), err)
			}
			defer root.Close()
			sessions = root
		}
		lock, err := sessions.Lock(migrateLockWait)
		if lockHeld(err) {
			return &Error{
				Kind:    KindBusy,
				Message: "the state lock was held for " + migrateLockWait.String(),
				Details: map[string]any{"lock": "state"},
			}
		}
		if err != nil {
			return IOError(m.l.SessionsDir(), err)
		}
		defer lock.Unlock()
	}

	state, err := m.env.FS.OpenRoot(m.l.StateDir)
	if err != nil {
		return IOError(m.l.StateDir, err)
	}
	defer state.Close()
	data, err := state.ReadFile(model.StateName)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return IOError(m.statePath(), err)
	}
	var next model.StateFile
	next.Migration = model.LatestMigration
	rebuild := missing
	if !missing {
		s, r := model.ReadState(data)
		switch {
		case r.Usable:
			if e := m.checkMigration(s.Migration); e != nil {
				return e
			}
			if s.Migration == model.LatestMigration {
				return nil // another migrate finished first
			}
			next.LastID = s.LastID
		case r.OtherFormat && r.Found > model.StateSchema:
			return m.unsupported("schema", r.Found, model.StateSchema)
		case r.OtherFormat:
			conv, _, err := migrate.Convert(migrate.State, data)
			var me *migrate.Error
			switch {
			case errors.As(err, &me) && me.Kind == migrate.Unconverted:
				m.unconverted(m.statePath(), me.Detail)
				return nil
			case errors.As(err, &me):
				rebuild = true
			case err != nil:
				return &Error{Kind: KindInternal, Message: m.statePath() + ": " + err.Error()}
			default:
				cs, _ := model.ReadState(conv)
				next.LastID = cs.LastID
			}
		default:
			rebuild = true
		}
	}
	if rebuild {
		// A sesshin.json in another format holds an id this binary can't
		// read: a rebuild could reissue it, so state.json stays as it is and
		// the number doesn't advance, as for an unconvertible state.json.
		if len(m.foreign) > 0 {
			for _, f := range m.foreign {
				if !f.listed {
					m.out.Unconverted = append(m.out.Unconverted, UnconvertedFile{
						Path:   f.path,
						Detail: f.detail + "; last_id is not rebuilt while a sesshin.json is in another format",
					})
				}
			}
			return nil
		}
		highest, e := m.highestID(sessions)
		if e != nil {
			return e
		}
		next.LastID = highest
	}
	if m.dryRun {
		return nil
	}
	b, err := jsonio.MarshalFile(next)
	if err != nil {
		return &Error{Kind: KindInternal, Message: "encoding state.json: " + err.Error()}
	}
	if err := fsys.PublishSynced(state, model.StateName, b); err != nil {
		return IOError(m.statePath(), err)
	}
	return nil
}

// highestID is the highest id in any usable sesshin.json, 0 when none.
func (m *migrator) highestID(sessions fsys.Root) (int64, *Error) {
	if sessions == nil {
		return 0, nil // a dry run with no sessions/
	}
	ids, e := m.sessionIDs(sessions)
	if e != nil {
		return 0, e
	}
	var highest int64
	for _, id := range ids {
		b, err := sessions.ReadFile(strings.Join([]string{id, model.SesshinName}, "/"))
		if err != nil {
			continue
		}
		if h, r := model.ReadSesshin(b); r.Usable && h.ID != nil {
			highest = max(highest, *h.ID)
		}
	}
	return highest, nil
}
