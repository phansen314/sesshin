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
	// key names the file; job is the stored job when usable, else the key.
	key, job string
	// usable: it validates and its job's key is its file name. file is
	// meaningful only then; why says what is wrong otherwise.
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
// returns it with the keys it names: each visible regular file named
// <key>.json with a valid key, sorted. A missing directory is no
// reservations, and root is nil. Other entries are ignored.
func (p *pruner) listReservations() (root fsys.Root, keys []string, e *Error) {
	dir := p.l.ReservationsDir()
	root, err := p.env.FS.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, IOError(dir, err)
	}
	entries, err := root.ReadDir(".")
	if err != nil {
		root.Close()
		return nil, nil, IOError(dir, err)
	}
	for _, ent := range entries {
		name := ent.Name()
		key, ok := strings.CutSuffix(name, model.ReservationExt)
		if !ok || strings.HasPrefix(name, ".") || !ent.Type().IsRegular() || !model.IsJobKey(key) {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return root, keys, nil
}

// readReservation reads reservations/<key>.json through root, whose path is
// dir. gone is a file that isn't there: removed since it was listed, or
// never made. A file that is past MaxRead or a directory is unusable, as a
// malformed one is.
func readReservation(root fsys.Root, dir, key string) (r reservation, gone bool, e *Error) {
	r.key, r.job = key, key
	path := filepath.Join(dir, key+model.ReservationExt)
	b, err := root.ReadFile(key + model.ReservationExt)
	switch {
	case err == nil:
		f, res := model.ReadReservation(b, key)
		if res.Usable {
			r.usable, r.file, r.job = true, f, f.Job
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

// readReservations is the first read of every job, with no lock.
func (p *pruner) readReservations(root fsys.Root, keys []string) ([]reservation, *Error) {
	var out []reservation
	for _, key := range keys {
		r, gone, e := readReservation(root, p.l.ReservationsDir(), key)
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
		r, gone, e := readReservation(root, p.l.ReservationsDir(), was.key)
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
		item := ReservationItem{Job: r.job, Reason: reason}
		if r.usable {
			item.CreatedAt = &r.file.CreatedAt
		} else {
			p.warnReservation(p.l.ReservationsDir(), r, out.DryRun)
		}
		if !out.DryRun {
			err := root.Remove(r.key + model.ReservationExt)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return IOError(filepath.Join(p.l.ReservationsDir(), r.key+model.ReservationExt), err)
			}
		}
		out.ReservationsRemoved = append(out.ReservationsRemoved, item)
	}
	slices.SortFunc(out.ReservationsRemoved, func(a, b ReservationItem) int { return cmp.Compare(a.Job, b.Job) })
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
	path := filepath.Join(dir, r.key+model.ReservationExt)
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
