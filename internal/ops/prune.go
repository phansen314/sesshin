package ops

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/config"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
)

// The reasons prune removes a reservation (prune-output), in precedence
// order.
const (
	reasonUnusable   = "unusable"
	reasonExpired    = model.StaleExpired
	reasonStranded   = model.StaleStranded
	reasonWindowGone = "window-gone"
)

// PruneInput is prune's input (prune-input).
type PruneInput struct {
	DryRun bool
	// RetainDays is an explicit retain_days, which applies even when the
	// config's is 0; nil when none was given.
	RetainDays *int64
}

// DecodePruneInput is prune's own checks: dry_run a boolean, retain_days an
// integer of at least 1, and at most 2^63 − 1.
func DecodePruneInput(f *model.Fields, p *model.Problems) PruneInput {
	var in PruneInput
	if v, ok := f.Optional("dry_run"); ok {
		in.DryRun, _ = p.Bool(v, f.Ptr("dry_run"))
	}
	if v, ok := f.Optional("retain_days"); ok {
		if n, ok := p.Int(v, f.Ptr("retain_days"), 1, math.MaxInt64); ok {
			in.RetainDays = &n
		}
	}
	return in
}

// PruneOutput is prune's result (prune-output).
type PruneOutput struct {
	DryRun         bool         `json:"dry_run"`
	Cutoff         *string      `json:"cutoff"`
	HeadlessCutoff *string      `json:"headless_cutoff"`
	Pruned         []PrunedItem `json:"pruned"`
	KeptEnded      int          `json:"kept_ended"`
	SkippedLocked  int          `json:"skipped_locked"`

	ReservationsRemoved       []ReservationItem `json:"reservations_removed"`
	ReservationsSkippedLocked bool              `json:"reservations_skipped_locked"`
}

// ReservationItem is one reservation prune removed, or with dry_run would.
type ReservationItem struct {
	Job string `json:"job"`
	// CreatedAt is nil for an unusable reservation.
	CreatedAt *model.Timestamp `json:"created_at"`
	Reason    string           `json:"reason"`
}

// PrunedItem is one session prune removed, or with dry_run would.
type PrunedItem struct {
	SessionID string          `json:"session_id"`
	ID        *int64          `json:"id"`
	LastSeen  model.Timestamp `json:"last_seen"`
	Headless  bool            `json:"headless"`
}

// pruner is one run of prune.
type pruner struct {
	env      ReadEnv
	l        loc.Locations
	root     fsys.Root // sessions/
	now      time.Time
	cutoff   model.Timestamp // "" when off
	hCutoff  model.Timestamp // "" when off
	warnings []Warning
}

// Prune removes the ended sessions last seen before the retention window
// and the stale and unusable reservations (operations.md, prune; design-spec.md,
// Retention). It reads state.json only for the migration status, tries each candidate's session lock
// once, and, with none held, the state lock once for the reservations.
func pruneOp(in PruneInput, env ReadEnv) Envelope {
	l, cfg, e := loadSetup(env)
	if e != nil {
		return Failed(e)
	}

	now := env.Now().UTC()
	p := &pruner{env: env, l: l, now: now}
	out := PruneOutput{DryRun: in.DryRun, Pruned: []PrunedItem{}, ReservationsRemoved: []ReservationItem{}}
	days := cfg.RetainDays
	if in.RetainDays != nil {
		days = *in.RetainDays
	}
	if days > 0 {
		p.cutoff = model.FormatTimestamp(config.DaysBefore(now, days))
		out.Cutoff = ptrTo(string(p.cutoff))
	}
	if cfg.RetainHeadlessHours > 0 {
		p.hCutoff = model.FormatTimestamp(now.Add(-cfg.RetainHeadless()))
		out.HeadlessCutoff = ptrTo(string(p.hCutoff))
	}

	// prune never creates the state directory: without sessions/ there is
	// nothing to judge.
	root, err := env.FS.OpenRoot(l.SessionsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return Succeeded(out)
	}
	if err != nil {
		return Failed(IOError(l.SessionsDir(), err))
	}
	defer root.Close()
	p.root = root

	if e := p.run(&out); e != nil {
		return Failed(e)
	}
	res := Succeeded(out)
	res.Warnings = append(res.Warnings, p.warnings...)
	return res
}

// run judges the sessions and the reservations, filling out.
func (p *pruner) run(out *PruneOutput) *Error {
	entries, err := p.root.ReadDir(".")
	if err != nil {
		return IOError(p.l.SessionsDir(), err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })

	var sessions []live.Session // those with a usable lifecycle.json
	var newest model.Timestamp
	for _, ent := range entries {
		id := ent.Name()
		if !isSessionEntry(ent) {
			continue
		}
		s, gone, e := p.read(id)
		if e != nil {
			return e
		}
		if gone {
			continue
		}
		if s.Lifecycle != nil {
			newest = max(newest, s.Lifecycle.LastEventAt)
			sessions = append(sessions, s)
		}
		if s.Statusline != nil {
			newest = max(newest, s.Statusline.ReceivedAt)
		}
	}

	rroot, keys, e := p.listReservations()
	if e != nil {
		return e
	}
	if rroot != nil {
		defer rroot.Close()
	}
	rsv, e := p.readReservations(rroot, keys)
	if e != nil {
		return e
	}
	for _, r := range rsv {
		if r.usable {
			newest = max(newest, r.file.CreatedAt)
		}
	}

	// A clock earlier than anything hooks recorded can't be trusted: nothing
	// is judged, and no cutoff is reported.
	if model.FormatTimestamp(p.now) < newest {
		out.Cutoff, out.HeadlessCutoff = nil, nil
		return nil
	}
	results := live.Derive(sessions, p.now, p.env.StartedAt)
	type candidate struct {
		idx      int
		lastSeen model.Timestamp
	}
	var cands []candidate
	for i, r := range results {
		switch {
		case r.State != live.Ended:
		case p.prunable(r.LastSeen, headless(sessions[i].Lifecycle)):
			cands = append(cands, candidate{i, r.LastSeen})
		default:
			out.KeptEnded++
		}
	}
	slices.SortFunc(cands, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(a.lastSeen, b.lastSeen), strings.Compare(sessions[a.idx].ID, sessions[b.idx].ID))
	})

	for _, c := range cands {
		item, kept, skipped, e := p.judgeLocked(sessions, c.idx, out.DryRun)
		switch {
		case e != nil:
			return e
		case skipped:
			out.SkippedLocked++
		case kept:
			out.KeptEnded++
		case item != nil:
			out.Pruned = append(out.Pruned, *item)
		}
	}
	slices.SortFunc(out.Pruned, func(a, b PrunedItem) int {
		return cmp.Or(cmp.Compare(a.LastSeen, b.LastSeen), strings.Compare(a.SessionID, b.SessionID))
	})

	// Every session lock is released: the state lock comes last.
	if e := p.reservations(rroot, rsv, out); e != nil {
		return e
	}
	return nil
}

// prunable is the window test: last seen before the cutoff, or, for a
// headless session, before the headless cutoff. An off cutoff never matches.
func (p *pruner) prunable(lastSeen model.Timestamp, headless bool) bool {
	return p.cutoff != "" && lastSeen < p.cutoff ||
		headless && p.hCutoff != "" && lastSeen < p.hCutoff
}

// judgeLocked tries the session's lock once. Held: skipped. Under it, the
// session's files are read again and it is judged again, with the rest of
// the first scan around it for rule 3. Still prunable, it is renamed aside
// (unless dryRun); otherwise kept says it is an ended session inside the
// window, or item and kept are both zero for one that is no longer ended or
// has gone.
func (p *pruner) judgeLocked(sessions []live.Session, idx int, dryRun bool) (item *PrunedItem, kept, skipped bool, e *Error) {
	id := sessions[idx].ID
	dir := p.l.SessionDir(id)
	sroot, err := p.root.OpenRoot(id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, false, nil // pruned by someone else since the scan
	}
	if err != nil {
		return nil, false, false, IOError(dir, err)
	}
	defer sroot.Close()
	lock, err := sroot.Lock(0)
	if lockHeld(err) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, IOError(dir, err)
	}
	defer lock.Unlock()

	// Another prune may have renamed it aside while this one waited on its
	// path: the descriptor then leads to a directory that is no longer there.
	if moved, err := sroot.Moved(); err != nil {
		return nil, false, false, IOError(dir, err)
	} else if moved {
		return nil, false, false, nil
	}

	fresh, gone, e := p.readFrom(sroot, id)
	if e != nil {
		return nil, false, false, e
	}
	if gone {
		return nil, false, false, nil
	}
	if fresh.Lifecycle == nil {
		return nil, false, false, nil // the warning was raised by readFrom
	}
	all := slices.Clone(sessions)
	all[idx] = fresh
	r := live.Derive(all, p.now, p.env.StartedAt)[idx]
	if r.State != live.Ended {
		return nil, false, false, nil
	}
	hl := headless(fresh.Lifecycle)
	if !p.prunable(r.LastSeen, hl) {
		return nil, true, false, nil
	}

	pi := PrunedItem{SessionID: id, LastSeen: r.LastSeen, Headless: hl}
	b, err := sroot.ReadFile(model.SesshinName)
	switch {
	case err == nil:
		if h, res := model.ReadSesshin(b); res.Usable {
			pi.ID = h.ID
		}
	case errors.Is(err, fs.ErrNotExist), unusableRead(err): // no ID to report
	default:
		return nil, false, false, IOError(filepath.Join(dir, model.SesshinName), err)
	}
	if !dryRun {
		if err := fsys.RemoveAside(p.root, id); err != nil {
			return nil, false, false, IOError(dir, err)
		}
	}
	return &pi, false, false, nil
}

// read reads one session directory through the sessions root.
func (p *pruner) read(id string) (s live.Session, gone bool, e *Error) {
	sroot, err := p.root.OpenRoot(id)
	if errors.Is(err, fs.ErrNotExist) {
		return live.Session{}, true, nil
	}
	if err != nil {
		return live.Session{}, false, IOError(p.l.SessionDir(id), err)
	}
	defer sroot.Close()
	return p.readFrom(sroot, id)
}

// readFrom reads the session's files through its root, by readSessionFiles.
// A missing or unusable lifecycle.json raises the unusable-file warning and
// leaves Lifecycle nil; an unusable statusline.json leaves Statusline nil,
// without one; sesshin.json isn't read here. gone is a session whose directory
// vanished as it was read: pruned by another run, skipped.
func (p *pruner) readFrom(sroot fsys.Root, id string) (s live.Session, gone bool, e *Error) {
	sf, gone, e := readSessionFiles(sroot, p.l.SessionDir(id), id, false)
	if e != nil || gone {
		return sf.Session, gone, e
	}
	for _, is := range sf.Issues {
		if is.File != model.LifecycleName {
			continue
		}
		msg := "lifecycle.json is unusable: " + is.Why
		if is.Missing {
			msg = "lifecycle.json is missing"
		}
		p.warn(is.Path, is.Reason, msg)
	}
	return sf.Session, false, nil
}

func (p *pruner) warn(path, reason, msg string) {
	p.warnings = append(p.warnings, Warning{
		Kind:    KindUnusableFile,
		Message: fmt.Sprintf("%s: %s; the session is kept", path, msg),
		Details: map[string]any{"path": path, "reason": reason},
	})
}
