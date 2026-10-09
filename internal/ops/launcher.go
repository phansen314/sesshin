package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/phansen314/sesshin/internal/config"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
)

// The rules and reasons spawn and resume raise (operations.md, Error kinds),
// as send's are in send.go.
const (
	ruleJobTaken        = "job-taken"
	reasonUnavailable   = "unavailable"
	reasonLaunchFailed  = "launch-failed"
	reasonLaunchUnknown = "launch-unknown"
	reasonUnsupported   = "unsupported"
)

// The waits of spawn and resume (implementation-spec.md, Spawn): the state
// lock to reserve and to record the window, and the pace of the wait for the
// session.
const (
	claimLockWait  = 500 * time.Millisecond
	recordLockWait = 2 * time.Second
	pollInterval   = 100 * time.Millisecond
)

// launcher is what spawn and resume share: the caller's window, the job's
// reservations as the first read found them, and the reservation, the record,
// and the release that follow it.
type launcher struct {
	env SpawnEnv
	l   loc.Locations
	cfg config.Config
	// b is the caller's backend, ln its launching, and caller the placement
	// it recognized: set by terminal.
	b      placement.Backend
	ln     placement.Launcher
	caller *jsonio.Object

	// job is the job to reserve; "" for none.
	job string
	// extra is the extra the reservation hands the session; nil for {}. Only
	// spawn sets it: a resumed session keeps the extra in its sesshin.json.
	extra *jsonio.Object
	// hint ends the job-taken message.
	hint string

	// first is the job's launched reservations not stale by age, as the first
	// read found them (before any lock), and answers what the backend said of
	// their windows.
	first   []reservation
	answers map[string]placement.Existence
	// token and name are the new reservation's, once made; token is "" for a
	// launch with no reservation.
	token, name string

	warnings []Warning
}

// terminal recognizes the caller's terminal, and checks its backend can
// launch a window: tmux and screen are ruled out before any backend is asked
// (design-spec.md, Placement).
func (s *launcher) terminal() *Error {
	b, ln, p, e := callerBackend(s.env.ReadEnv)
	s.b, s.ln, s.caller = b, ln, p
	return e
}

// callerBackend is the caller's backend, its launching, and the placement it
// recognized, or
// terminal unavailable when no backend recognizes the caller's terminal, or
// unsupported when the backend can't launch.
func callerBackend(env ReadEnv) (placement.Backend, placement.Launcher, *jsonio.Object, *Error) {
	b, p := placement.Detect(env.Backends, env.Getenv)
	switch {
	case placement.Multiplexed(env.Getenv):
		return nil, nil, nil, unavailable("the caller runs under tmux or screen, whose window variables name another window")
	case b == nil:
		return nil, nil, nil, unavailable("the caller is not in a window of a terminal sesshin has a backend for" + hints(env.Backends))
	}
	ln, ok := b.(placement.Launcher)
	if !ok {
		return b, nil, p, unsupported(b, "launch a window")
	}
	return b, ln, p, nil
}

// reserved reads the job's reservations, without a lock, and asks the backend
// about the windows of those that are usable, launched, and fresh.
func (s *launcher) reserved() *Error {
	if s.job == "" {
		return nil
	}
	root, err := s.env.FS.OpenRoot(s.l.ReservationsDir())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return IOError(s.l.ReservationsDir(), err)
	}
	defer root.Close()
	names, e := reservationNames(root, s.l.ReservationsDir())
	if e != nil {
		return e
	}
	now := s.env.Now().UTC()
	for _, name := range keyed(names, s.job) {
		r, gone, e := readReservation(root, s.l.ReservationsDir(), name)
		if e != nil {
			return e
		}
		if !gone && r.usable && r.file.Placement != nil && r.stale(now) == "" {
			s.first = append(s.first, r)
		}
	}
	s.answers = askWindows(s.env.ReadEnv, now, s.first)
	return nil
}

// firstRead is the reservation called name as the first read found it; the
// zero reservation (unusable) when there was none.
func (s *launcher) firstRead(name string) reservation {
	for _, r := range s.first {
		if r.name == name {
			return r
		}
	}
	return reservation{}
}

// unavailable is terminal unavailable: no backend recognizes the caller's
// terminal.
func unavailable(detail string) *Error {
	return terminalError("no terminal backend recognizes the caller's terminal: "+detail, reasonUnavailable, nil, detail)
}

// reserve makes the reservation under the state lock, with a job or without.
// With a job it is refused when a live session reports it or a fresh
// reservation of its key names it; stale reservations of the key are removed.
// The lock is released before it returns.
func (s *launcher) reserve() *Error {
	sroot, err := fsys.OpenRootCreate(s.env.FS, s.l.SessionsDir())
	if err != nil {
		return IOError(s.l.SessionsDir(), err)
	}
	defer sroot.Close()
	lock, err := sroot.Lock(claimLockWait)
	if lockHeld(err) {
		return &Error{
			Kind:    KindBusy,
			Message: "the state lock was held for " + claimLockWait.String(),
			Details: map[string]any{"lock": "state"},
		}
	}
	if err != nil {
		return IOError(s.l.SessionsDir(), err)
	}
	defer lock.Unlock()

	var job *string
	if s.job != "" {
		job = &s.job
		set, e := readSessionsFrom(s.env.ReadEnv, s.l, sroot)
		if e != nil {
			return e
		}
		for _, is := range set.issues {
			s.warnings = appendNew(s.warnings, issueWarning(is))
		}
		for _, r := range set.recs {
			if r.res.State != live.Ended && r.job != nil && model.JobKey(*r.job) == model.JobKey(s.job) {
				vw := viewer{fs: s.env.FS, now: set.now}
				return s.taken(*r.job, []SessionRef{vw.view(r).ref()})
			}
		}
	}

	rroot, err := fsys.OpenRootCreate(s.env.FS, s.l.ReservationsDir())
	if err != nil {
		return IOError(s.l.ReservationsDir(), err)
	}
	defer rroot.Close()
	now := s.env.Now().UTC()
	if job != nil {
		names, e := reservationNames(rroot, s.l.ReservationsDir())
		if e != nil {
			return e
		}
		var stale []string
		for _, name := range keyed(names, s.job) {
			cur, gone, e := readReservation(rroot, s.l.ReservationsDir(), name)
			if e != nil {
				return e
			}
			switch {
			case gone, !cur.usable:
				// Unusable ones are ignored here; prune removes them.
			case cur.stale(now) == "" && !windowGone(cur, s.firstRead(name), s.answers):
				return s.taken(cur.job, nil)
			default:
				stale = append(stale, name)
			}
		}
		for _, name := range stale {
			if err := rroot.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return IOError(filepath.Join(s.l.ReservationsDir(), name), err)
			}
		}
	}
	extra := s.extra
	if extra == nil {
		extra = &jsonio.Object{}
	}
	return s.write(rroot, model.ReservationFile{
		Job:       job,
		Token:     s.env.Token(),
		CreatedAt: model.FormatTimestamp(now),
		Extra:     extra,
	})
}

// taken is conflict job-taken; held is the job as stored by the holder, and
// sessions are those holding it, none for a reservation. The message names
// the stored job when its case differs from the one asked for.
func (s *launcher) taken(held string, sessions []SessionRef) *Error {
	if sessions == nil {
		sessions = []SessionRef{}
	}
	as := ""
	if held != s.job {
		as = ", as " + held
	}
	return &Error{
		Kind:    KindConflict,
		Message: "the job " + s.job + " is held" + as + s.hint,
		Details: map[string]any{"rule": ruleJobTaken, "sessions": sessions},
	}
}

// write publishes the reservation, under the name its job and token give.
func (s *launcher) write(rroot fsys.Root, f model.ReservationFile) *Error {
	data, err := jsonio.MarshalFile(f)
	if err != nil {
		return internal("encoding the reservation: %v", err)
	}
	name := model.ReservationName(s.job, f.Token)
	if err := fsys.Publish(rroot, name, data); err != nil {
		return IOError(filepath.Join(s.l.ReservationsDir(), name), err)
	}
	s.token, s.name = f.Token, name
	return nil
}

// failed is the error of a launch the backend did not complete, with the
// warnings so far. A refusal frees the job at once; an unknown outcome keeps
// the reservation, for the session that starts to adopt, and it strands in
// 120 seconds if none does.
func (s *launcher) failed(err error) Envelope {
	reason := reasonLaunchFailed
	if placement.IsUnknown(err) {
		reason = reasonLaunchUnknown
	} else if s.token != "" {
		s.release()
	}
	return FailedWith(backendError(s.b, reason, err.Error()), s.warnings)
}

// appendNew adds w unless a warning of its kind and path is there already:
// the claim's read and the last read of the wait can find the same file.
func appendNew(ws []Warning, w Warning) []Warning {
	for _, x := range ws {
		if x.Kind == w.Kind && x.Details["path"] == w.Details["path"] {
			return ws
		}
	}
	return append(ws, w)
}

// underLock runs fn with the state lock, waited for up to recordLockWait,
// and the reservations root opened through it (nil when reservations/ is
// missing). It is the record and the release of step 4 and 5, which differ
// only in what they do to the reservation.
func (s *launcher) underLock(fn func(rroot fsys.Root) error) error {
	sroot, err := s.env.FS.OpenRoot(s.l.SessionsDir())
	if err != nil {
		return err
	}
	defer sroot.Close()
	lock, err := sroot.Lock(recordLockWait)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	rroot, err := s.env.FS.OpenRoot(s.l.ReservationsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return fn(nil)
	}
	if err != nil {
		return err
	}
	defer rroot.Close()
	return fn(rroot)
}

// mine reads this run's reservation again and reports whether it is there,
// usable, and still holds this run's token. One that is gone (the session
// adopted it, or it was released by hand) or unusable is not this run's to
// touch.
func (s *launcher) mine(rroot fsys.Root) (r reservation, mine bool, err error) {
	if rroot == nil {
		return r, false, nil
	}
	r, gone, e := readReservation(rroot, s.l.ReservationsDir(), s.name)
	if e != nil {
		return r, false, errors.New(e.Message)
	}
	return r, !gone && r.usable && r.file.Token == s.token, nil
}

// record puts the launched window into the reservation, if it still holds
// this token. A lock not taken in time or a rewrite that fails is the
// placement-not-recorded warning: the reservation then strands 120 seconds
// after created_at unless the session adopts it first.
func (s *launcher) record(pl *jsonio.Object) {
	err := s.underLock(func(rroot fsys.Root) error {
		r, mine, err := s.mine(rroot)
		if err != nil || !mine {
			return err
		}
		r.file.Placement = pl
		if e := s.write(rroot, r.file); e != nil {
			return errors.New(e.Message)
		}
		return nil
	})
	if err != nil {
		s.warnings = append(s.warnings, Warning{
			Kind:    KindPlacementNotRecorded,
			Message: "the launched window was not recorded in the reservation: " + err.Error(),
			Details: map[string]any{"job": s.jobDetail(), "placement": pl},
		})
	}
}

// release removes the reservation of a launch the backend refused, if it
// still holds this token, so the job is free at once. A failure is ignored:
// the reservation then strands in 120 seconds, and the error stays terminal.
func (s *launcher) release() {
	_ = s.underLock(func(rroot fsys.Root) error {
		_, mine, err := s.mine(rroot)
		if err != nil || !mine {
			return err
		}
		return rroot.Remove(s.name)
	})
}

// jobDetail is the job for a warning's details: its name, or nil for none.
func (s *launcher) jobDetail() any {
	if s.job == "" {
		return nil
	}
	return s.job
}

// jobRef is the job to report: nil for none.
func (s *launcher) jobRef() *string {
	if s.job == "" {
		return nil
	}
	return &s.job
}

// checkDir is not-found unless path is an existing directory.
func checkDir(files fsys.FS, path string) *Error {
	st, err := files.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR), err == nil && !st.IsDir():
		return missingCwd(&path)
	case err != nil:
		return IOError(path, err)
	}
	return nil
}

// launchPlan is what spawn and resume each decide for launch.
type launchPlan struct {
	// spec is the launch, without the job's variables.
	spec placement.LaunchSpec
	// timeoutSecs is start_timeout_secs.
	timeoutSecs int64
	// read is one read of the wait, given the launched window: the session if
	// it has started, and the set read.
	read func(launched *jsonio.Object) (*sessionSet, *sessionRec, *Error)
	// notStarted is the not-started message, with the seconds waited as %d;
	// extra adds to its details.
	notStarted string
	extra      map[string]any
}

// launch opens the window, then records it in the reservation and waits for
// the session. SESSHIN_TOKEN is set whenever there is a reservation, and
// SESSHIN_JOB with a job; the session's extra travels in the reservation.
func (s *launcher) launch(p launchPlan) Envelope {
	spec := p.spec
	job := s.jobRef()
	if job != nil {
		spec.Env = append(spec.Env, placement.Var{Name: "SESSHIN_JOB", Value: s.job})
	}
	if s.token != "" {
		spec.Env = append(spec.Env, placement.Var{Name: "SESSHIN_TOKEN", Value: s.token})
	}

	launched, err := s.ln.Launch(spec)
	if err != nil {
		return s.failed(err)
	}

	pl := launched
	if s.token != "" {
		s.record(pl)
	}
	out := SpawnOutput{Job: job, Placement: pl}
	var last *sessionSet
	if p.timeoutSecs > 0 {
		set, rec, e := s.poll(p.timeoutSecs, func() (*sessionSet, *sessionRec, *Error) { return p.read(launched) })
		if e != nil {
			return Failed(e)
		}
		last = set
		if rec != nil {
			v := viewer{fs: s.env.FS, now: set.now}.view(rec)
			out.Session = &v
		}
	}
	res := Succeeded(out)
	res.Warnings = append(res.Warnings, s.warnings...)
	if last != nil {
		for _, is := range last.issues {
			res.Warnings = appendNew(res.Warnings, issueWarning(is))
		}
	}
	if p.timeoutSecs > 0 && out.Session == nil {
		details := map[string]any{"job": job, "placement": pl, "waited_secs": p.timeoutSecs}
		maps.Copy(details, p.extra)
		res.Warnings = append(res.Warnings, Warning{
			Kind:    KindNotStarted,
			Message: fmt.Sprintf(p.notStarted, p.timeoutSecs),
			Details: details,
		})
	}
	return res
}

// poll calls read every pollInterval, with no lock, until it returns the
// session or timeoutSecs has passed. The session is nil on a timeout; the set
// is the last read's. The deadline is judged on env.Now, which on the real
// clock is monotonic.
func (s *launcher) poll(timeoutSecs int64, read func() (*sessionSet, *sessionRec, *Error)) (*sessionSet, *sessionRec, *Error) {
	start := s.env.Now()
	limit := time.Duration(timeoutSecs) * time.Second
	for {
		set, rec, e := read()
		if e != nil {
			return nil, nil, e
		}
		if rec != nil {
			return set, rec, nil
		}
		if s.env.Now().Sub(start) >= limit {
			return set, nil, nil
		}
		s.env.Sleep(pollInterval)
	}
}

// hints is what each backend needs of the caller's environment, for the
// unavailable message: " (for kitty, …)", or "" when none says.
func hints(list []placement.Backend) string {
	var hs []string
	for _, b := range list {
		if h, ok := b.(placement.Hinter); ok {
			hs = append(hs, "for "+b.Tag()+", "+h.Hint())
		}
	}
	if len(hs) == 0 {
		return ""
	}
	return " (" + strings.Join(hs, "; ") + ")"
}
