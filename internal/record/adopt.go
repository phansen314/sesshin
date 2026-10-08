package record

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

// launchEnv is SESSHIN_JOB and SESSHIN_TOKEN from the environment. Each is ""
// when unset or malformed: a value that isn't a job name or a token is
// ignored, as if unset (hooks-spec.md, Reading the payload). No hook reads
// extra from the environment. Only session-start logs a bad one (logJobEnv),
// so a busy session's every tool call doesn't add a line.
func (e Env) launchEnv() (job, token string) {
	if v := e.Getenv("SESSHIN_JOB"); model.IsJob(v) {
		job = v
	}
	if v := e.Getenv("SESSHIN_TOKEN"); model.IsToken(v) {
		token = v
	}
	return job, token
}

// logJobEnv logs a SESSHIN_JOB or SESSHIN_TOKEN that is set and malformed. The
// value is not echoed: it is arbitrary, and the log is one line per entry.
func (e Env) logJobEnv() {
	if v := e.Getenv("SESSHIN_JOB"); v != "" && !model.IsJob(v) {
		e.Log("SESSHIN_JOB is not a job name; ignored")
	}
	if v := e.Getenv("SESSHIN_TOKEN"); v != "" && !model.IsToken(v) {
		e.Log("SESSHIN_TOKEN is not a token; ignored")
	}
}

// reservation is one reservation as read for an adoption.
type reservation struct {
	// root is reservations/, nil when there is none; name and file are
	// meaningful only when usable. An unusable file is read as missing, as
	// every check does (design-spec.md, Reservations); prune removes it.
	root   fsys.Root
	name   string
	usable bool
	file   model.ReservationFile
}

// readReservation reads the reservation named by job (SESSHIN_JOB, "" for
// none) and token: reservations/<key>_<token>.json, or <token>.json. The one
// file is opened by name; there is no scan. A missing directory or file is no
// reservation, and so is an unusable one. A read that failed for any other
// reason is logged, and also reads as no reservation: a hook decides with what
// it can see.
func (e Env) readReservation(job, token string) reservation {
	root, err := e.FS.OpenRoot(e.Loc.ReservationsDir())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			e.Log(wrap("open reservations", err).Error())
		}
		return reservation{}
	}
	return e.rereadReservation(root, job, token)
}

// rereadReservation reads the reservation again through root, which it keeps.
func (e Env) rereadReservation(root fsys.Root, job, token string) reservation {
	r := reservation{root: root, name: model.ReservationName(job, token)}
	data, err := root.ReadFile(r.name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		e.Log(wrap("read reservation", err).Error())
	default:
		var res model.FileResult
		r.file, res = model.ReadReservation(data, r.name)
		r.usable = res.Usable
	}
	return r
}

// removeReservation removes the reservation's file, logging a failure: it goes stale.
// A file that is already gone is not a failure.
func (e Env) removeReservation(r reservation) {
	if err := r.root.Remove(r.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.Log(wrap("remove reservation", err).Error())
	}
}

// reservationHolds reports whether a fresh reservation of job's key exists:
// a usable one among the names in reservations/ that parse with that key. It
// lists the directory, as the glob <key>_*.json does. A listing or a read
// that fails is logged and holds nothing.
func (e Env) reservationHolds(job string) bool {
	root, err := e.FS.OpenRoot(e.Loc.ReservationsDir())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			e.Log(wrap("open reservations", err).Error())
		}
		return false
	}
	defer root.Close()
	entries, err := root.ReadDir(".")
	if err != nil {
		e.Log(wrap("list reservations", err).Error())
		return false
	}
	key := model.JobKey(job)
	for _, d := range entries {
		name := d.Name()
		if d.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if k, _, ok := model.ParseReservationName(name); !ok || k != key {
			continue
		}
		data, err := root.ReadFile(name)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				e.Log(wrap("read reservation", err).Error())
			}
			continue
		}
		if f, res := model.ReadReservation(data, name); res.Usable && f.Fresh(e.Now) {
			return true
		}
	}
	return false
}

// decision is the Adopt rules' answer (design-spec.md, Reservations).
type decision struct {
	job    *string
	source string
	// extra is the adopted reservation's extra; nil when none was adopted,
	// which leaves the file's own.
	extra *jsonio.Object
	// matched is the reservation named by this session's token, fresh or
	// stale, to remove after sesshin.json is written; its root is nil when
	// there is none.
	matched reservation
}

// release is Creating sesshin.json's step 4 after the write: the matched
// reservation, if any, is removed, but only when sesshin.json was written, so
// that at every moment either it or a session holds the job. A failure is
// logged, and the reservation goes stale.
func (d decision) release(e Env, written bool) {
	if d.matched.root == nil {
		return
	}
	defer d.matched.root.Close()
	if written {
		e.removeReservation(d.matched)
	}
}

// decideJob is Creating sesshin.json's step 3: the Adopt rules, under the state
// lock the caller holds, over sessions/ opened for it. nested is
// lifecycle.json's, nil when unknown. The held-job line is all it logs; the
// reads of other sessions' files log nothing. The caller releases the
// decision.
func (e Env) decideJob(sessions fsys.Root, nested *bool) decision {
	d := decision{source: model.SourceHook}
	if nested != nil && *nested {
		// The variables were inherited: no reservation is opened or removed.
		return d
	}
	job, token := e.launchEnv()
	if token != "" {
		r := e.readReservation(job, token)
		if r.usable {
			d.matched = r
		} else if r.root != nil {
			r.root.Close()
		}
		if r.usable && r.file.Fresh(e.Now) {
			if job != "" {
				d.job = &job
			}
			d.source, d.extra = model.SourceSpawn, r.file.Extra
			return d
		}
		// A stale reservation is removed after the write and delivers
		// nothing: the job is decided by rule 3, or rule 4 without one.
	}
	if job == "" {
		return d
	}
	if e.reservationHolds(job) {
		e.Log("job " + job + " held by a reservation")
		return d
	}
	if holder, ok := e.holder(sessions, job); ok {
		e.Log("job " + job + " held by " + holder)
		return d
	}
	d.job = &job
	return d
}

// holder is Adopt rule 3's check that a live session holds job, and how to
// name it: `#<id>`, or its UUID when it has no ID yet. It reads every other
// session directory under the held sessions/ root, as list does but with no
// warnings: lifecycle.json, statusline.json, and sesshin.json, each missing or
// unusable one read as missing. One with no usable lifecycle.json is left
// out, as in the readers. This session is read too, so that Liveness rule 3
// can supersede an earlier session of its process (a /clear whose SessionEnd
// was lost), but it is never the holder. Liveness is live.Derive's, the
// job a session reports live.Jobs': a session whose liveness is unknown holds
// its job.
func (e Env) holder(sessions fsys.Root, job string) (string, bool) {
	entries, err := sessions.ReadDir(".")
	if err != nil {
		e.Log(wrap("list sessions", err).Error())
		return "", false
	}
	var (
		all      []live.Session
		sesshins []*model.SesshinFile
		stored   []*string
	)
	for _, d := range entries {
		name := d.Name()
		if !d.IsDir() || strings.HasPrefix(name, ".") || !model.IsUUID(name) {
			continue
		}
		root, err := sessions.OpenRoot(name)
		if err != nil {
			continue
		}
		s := live.Session{ID: name}
		var h *model.SesshinFile
		if data, err := root.ReadFile(model.LifecycleName); err == nil {
			if l, r := model.ReadLifecycle(data, name); r.Usable {
				s.Lifecycle = &l
			}
		}
		if data, err := root.ReadFile(model.StatuslineName); err == nil {
			if st, r := model.ReadStatusline(data); r.Usable {
				s.Statusline = &st
			}
		}
		if data, err := root.ReadFile(model.SesshinName); err == nil {
			if hf, r := model.ReadSesshin(data); r.Usable {
				h = &hf
			}
		}
		root.Close()
		if s.Lifecycle == nil {
			continue
		}
		var j *string
		if h != nil {
			j = h.Job
		}
		all, sesshins, stored = append(all, s), append(sesshins, h), append(stored, j)
	}
	started := e.StartedAt
	if started == nil {
		started = func(pid int64) (string, error) { return proc.StartedAt(e.FS, pid) }
	}
	results := live.Derive(all, e.Now, started)
	for i, reported := range live.Jobs(all, results, stored) {
		if reported == nil || model.JobKey(*reported) != model.JobKey(job) || results[i].State == live.Ended || all[i].ID == e.SessionID {
			continue
		}
		if h := sesshins[i]; h != nil && h.ID != nil {
			return "#" + strconv.FormatInt(*h.ID, 10), true
		}
		return all[i].ID, true
	}
	return "", false
}

// adoptResume is session-start's adoption of a resumed session's reservation
// (hooks-spec.md, session-start), under the session lock, when sesshin.json
// already had an ID before the hook: the reservation named by SESSHIN_JOB's key
// and SESSHIN_TOKEN is always removed, and when it is fresh, sets sesshin.json's
// job. source and extra never change: the session keeps its own extra,
// whatever the reservation holds. A resumed session never runs the Adopt
// rules, and a /clear, a /new, or an in-session /resume finds no match and
// takes no lock. The state lock is taken only for a match, and the
// reservation read again under it. Every failure is logged, and the
// reservation goes stale.
func (e Env) adoptResume(root fsys.Root) {
	job, token := e.launchEnv()
	if job == "" || token == "" {
		return
	}
	r := e.readReservation(job, token)
	if r.root != nil {
		r.root.Close()
	}
	if !r.usable {
		return
	}
	sessions, err := e.FS.OpenRoot(e.Loc.SessionsDir())
	if err != nil {
		e.Log(wrap("open sessions", err).Error())
		return
	}
	defer sessions.Close()
	lock, err := sessions.Lock(e.wait())
	if err != nil {
		e.Log(wrap("state lock", err).Error())
		return
	}
	defer lock.Unlock()
	r = e.readReservation(job, token)
	if r.root == nil {
		return
	}
	defer r.root.Close()
	if !r.usable {
		return
	}
	if r.file.Fresh(e.Now) {
		h, _, st, _ := readFile(e, root, model.SesshinName, model.ReadSesshin)
		if st != usable {
			return
		}
		if h.Job == nil || *h.Job != job {
			h.Job = &job
			if e.write(root, model.SesshinName, h) != nil {
				return
			}
		}
	}
	e.removeReservation(r)
}
