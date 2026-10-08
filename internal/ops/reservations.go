package ops

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// reservation is one file in reservations/ as read.
type reservation struct {
	// name is the file name, <key>_<token>.json or <token>.json; job is the
	// stored job when usable, "" for none and for an unusable file.
	name, job string
	// usable: it validates, and its name is its job's key and its token.
	// file is meaningful only then; why says what is wrong otherwise.
	usable bool
	why    string
	reason string // the unusable-file reason for why
	file   model.ReservationFile
}

// placementKey is the placement as it is stored, for telling whether it
// changed; "" for none.
func (r reservation) placementKey() string {
	if r.file.Placement == nil {
		return ""
	}
	b, _ := jsonio.MarshalLine(r.file.Placement)
	return string(b)
}

// stale is the reason the reservation is stale or unusable by what the file
// says alone, "" when it is fresh: unusable, else model's Staleness.
// window-gone is judged apart, from the backend's answer.
func (r reservation) stale(now time.Time) string {
	if !r.usable {
		return reasonUnusable
	}
	return r.file.Staleness(now)
}

// listReservations opens reservations/, which prune never creates, once, and
// returns it with the names it holds: each visible regular file whose name
// ends in .json, sorted, whether or not the name parses (one that doesn't is
// unusable). A missing directory is no reservations, and root is nil. Other
// entries are ignored.
func (p *pruner) listReservations() (root fsys.Root, names []string, e *Error) {
	dir := p.l.ReservationsDir()
	root, err := p.env.FS.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, IOError(dir, err)
	}
	names, e = reservationNames(root, dir)
	if e != nil {
		root.Close()
		return nil, nil, e
	}
	return root, names, nil
}

// reservationNames lists the visible regular files of reservations/ (root,
// at dir) whose names end in .json, sorted.
func reservationNames(root fsys.Root, dir string) ([]string, *Error) {
	entries, err := root.ReadDir(".")
	if err != nil {
		return nil, IOError(dir, err)
	}
	var names []string
	for _, ent := range entries {
		name := ent.Name()
		if strings.HasSuffix(name, model.ReservationExt) && !strings.HasPrefix(name, ".") && ent.Type().IsRegular() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// keyed is the names of those that parse with job's key.
func keyed(names []string, job string) []string {
	var out []string
	for _, name := range names {
		if key, _, ok := model.ParseReservationName(name); ok && key == model.JobKey(job) && key != "" {
			out = append(out, name)
		}
	}
	return out
}

// readReservation reads the reservation called name through root, whose path
// is dir. gone is a file that isn't there: removed since it was listed, or
// never made. A file that is past MaxRead or a directory is unusable, as a
// malformed one is.
func readReservation(root fsys.Root, dir, name string) (r reservation, gone bool, e *Error) {
	r.name = name
	path := filepath.Join(dir, name)
	b, err := root.ReadFile(name)
	switch {
	case err == nil:
		f, res := model.ReadReservation(b, name)
		if res.Usable {
			r.usable, r.file = true, f
			if f.Job != nil {
				r.job = *f.Job
			}
		} else {
			r.why, r.reason = res.Reason(), formatReason(res)
		}
	case errors.Is(err, fs.ErrNotExist):
		return r, true, nil
	case unusableRead(err):
		r.why, r.reason = err.Error(), ReasonUnreadable
	default:
		return r, false, IOError(path, err)
	}
	return r, false, nil
}

// readReservations is the first read of every reservation, with no lock.
func (p *pruner) readReservations(root fsys.Root, names []string) ([]reservation, *Error) {
	var out []reservation
	for _, name := range names {
		r, gone, e := readReservation(root, p.l.ReservationsDir(), name)
		if e != nil {
			return nil, e
		}
		if !gone {
			out = append(out, r)
		}
	}
	return out, nil
}

// windowAnswer is what the backend said of one socket's windows.
type windowAnswer map[int64]bool

// askWindows puts the launched reservations not stale by age to the backend,
// once per socket, with no lock held. A socket it gave no answer for is not
// in the result.
func askWindows(env ReadEnv, now time.Time, rsv []reservation) map[string]windowAnswer {
	answers := map[string]windowAnswer{}
	if env.Windows == nil {
		return answers
	}
	asked := map[string]bool{}
	for _, r := range rsv {
		if r.stale(now) != "" || r.file.Placement == nil {
			continue
		}
		pl, ok := kitty.Parse(r.file.Placement)
		if !ok || asked[pl.Socket] {
			continue
		}
		asked[pl.Socket] = true
		if ids, ok := env.Windows(r.file.Placement); ok {
			a := windowAnswer{}
			for _, id := range ids {
				a[id] = true
			}
			answers[pl.Socket] = a
		}
	}
	return answers
}

// reservations is the last of a run: ask about the windows, take the state
// lock once, read each reservation again through root, and remove those still
// stale or unusable. first is the first read. A held lock sets
// ReservationsSkippedLocked and ends it; a removal that fails is io for its
// path, and the ones before it stay removed.
func (p *pruner) reservations(root fsys.Root, first []reservation, out *PruneOutput) *Error {
	if len(first) == 0 {
		return nil
	}
	answers := askWindows(p.env, p.now, first)

	lock, err := p.root.Lock(0)
	if lockHeld(err) {
		out.ReservationsSkippedLocked = true
		return nil
	}
	if err != nil {
		return IOError(p.l.SessionsDir(), err)
	}
	defer lock.Unlock()

	for _, was := range first {
		r, gone, e := readReservation(root, p.l.ReservationsDir(), was.name)
		if e != nil {
			return e
		}
		if gone {
			continue
		}
		reason := r.stale(p.now)
		if reason == "" && windowGone(r, was, answers) {
			reason = reasonWindowGone
		}
		if reason == "" {
			continue
		}
		item := ReservationItem{File: r.name, Reason: reason}
		if r.usable {
			item.Job, item.CreatedAt = r.file.Job, &r.file.CreatedAt
		} else {
			p.warnReservation(p.l.ReservationsDir(), r, out.DryRun)
		}
		if !out.DryRun {
			err := root.Remove(r.name)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return IOError(filepath.Join(p.l.ReservationsDir(), r.name), err)
			}
		}
		out.ReservationsRemoved = append(out.ReservationsRemoved, item)
	}
	slices.SortFunc(out.ReservationsRemoved, func(a, b ReservationItem) int { return cmp.Compare(a.File, b.File) })
	return nil
}

// windowGone: the backend answered for the reservation's socket without its
// window, and it still holds the token and placement it was asked about (was
// is the first read).
func windowGone(r, was reservation, answers map[string]windowAnswer) bool {
	if !r.usable || !was.usable || r.file.Placement == nil ||
		r.file.Token != was.file.Token || r.placementKey() != was.placementKey() {
		return false
	}
	pl, ok := kitty.Parse(r.file.Placement)
	if !ok {
		return false
	}
	a, answered := answers[pl.Socket]
	return answered && !a[pl.WindowID]
}

// warnReservation is the unusable-file warning for a reservation, raised as
// it is removed; dir is reservations/'s path.
func (p *pruner) warnReservation(dir string, r reservation, dryRun bool) {
	path := filepath.Join(dir, r.name)
	effect := "it is removed"
	if dryRun {
		effect = "it would be removed"
	}
	p.warnings = append(p.warnings, Warning{
		Kind:    KindUnusableFile,
		Message: fmt.Sprintf("%s: reservation is unusable: %s; %s", path, r.why, effect),
		Details: map[string]any{"path": path, "reason": r.reason},
	})
}
