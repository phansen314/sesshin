package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"syscall"
	"time"

	"github.com/phansen314/sesshin/internal/config"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// The rules and reasons spawn and resume raise (operations.md, Error kinds),
// as send's are in send.go.
const (
	ruleJobTaken        = "job-taken"
	reasonUnavailable   = "unavailable"
	reasonLaunchFailed  = "launch-failed"
	reasonLaunchUnknown = "launch-unknown"
)

// The waits of spawn and resume (implementation-spec.md, Spawn): the state
// lock to claim a job and to record the window, and the pace of the wait for
// the session.
const (
	claimLockWait  = 500 * time.Millisecond
	recordLockWait = 2 * time.Second
	pollInterval   = 100 * time.Millisecond
)

// launcher is what spawn and resume share: the caller's window, the job's
// reservation as the first read found it, and the claim, the record, and the
// release that follow it.
type launcher struct {
	env  SpawnEnv
	l    loc.Locations
	cfg  config.Config
	sock kitty.Parsed // the caller's window

	// job is the job to reserve; "" for none.
	job string
	// hint ends the job-taken message.
	hint string

	// first is the job's reservation as the first read found it (before any
	// lock), and answers what the backend said of its window.
	first   reservation
	answers map[string]windowAnswer
	// token is the new reservation's, once claimed.
	token string

	warnings []Warning
}

// terminal recognizes the caller's terminal: kitty is the only backend, and
// tmux and screen are ruled out before it is asked (design-spec.md,
// Placement).
func (s *launcher) terminal() *Error {
	sock, e := callerWindow(s.env.Getenv)
	s.sock = sock
	return e
}

// callerWindow is the caller's kitty window, or terminal unavailable.
func callerWindow(getenv func(string) string) (kitty.Parsed, *Error) {
	sock, ok := kitty.RecognizeParsed(getenv)
	switch {
	case placement.Multiplexed(getenv):
		return sock, unavailable("the caller runs under tmux or screen, whose window variables name another window")
	case !ok:
		return sock, unavailable("the caller is not in a kitty window with remote control on (KITTY_LISTEN_ON and KITTY_WINDOW_ID)")
	}
	return sock, nil
}

// reserved reads the job's reservation, without a lock, and asks the backend
// about its window when it is usable, launched, and fresh.
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
	r, gone, e := readReservation(root, s.l.ReservationsDir(), s.job)
	if e != nil {
		return e
	}
	if !gone && r.usable && r.file.Placement != nil && r.stale(s.env.Now().UTC()) == "" {
		s.first = r
		s.answers = askWindows(s.env.ReadEnv, s.env.Now().UTC(), []reservation{r})
	}
	return nil
}

// unavailable is terminal unavailable: no backend recognizes the caller's
// terminal.
func unavailable(detail string) *Error {
	return terminalError("no terminal backend recognizes the caller's terminal: "+detail, reasonUnavailable, nil, detail)
}

// claim reserves the job under the state lock: refused when a live session
// reports it or a fresh reservation names it, else the reservation is made,
// replacing a stale or unusable one. The lock is released before it returns.
func (s *launcher) claim() *Error {
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

	set, e := readSessionsFrom(s.env.ReadEnv, s.l, sroot)
	if e != nil {
		return e
	}
	for _, is := range set.issues {
		s.warnings = appendNew(s.warnings, issueWarning(is))
	}
	for _, r := range set.recs {
		if r.res.State != live.Ended && r.job != nil && *r.job == s.job {
			vw := viewer{fs: s.env.FS, now: set.now}
			return s.taken([]SessionRef{vw.view(r).ref()})
		}
	}

	rroot, err := fsys.OpenRootCreate(s.env.FS, s.l.ReservationsDir())
	if err != nil {
		return IOError(s.l.ReservationsDir(), err)
	}
	defer rroot.Close()
	cur, gone, e := readReservation(rroot, s.l.ReservationsDir(), s.job)
	if e != nil {
		return e
	}
	now := s.env.Now().UTC()
	if !gone && cur.stale(now) == "" && !windowGone(cur, s.first, s.answers) {
		return s.taken(nil)
	}
	return s.write(rroot, model.ReservationFile{
		Job:       s.job,
		Token:     s.env.Token(),
		CreatedAt: model.FormatTimestamp(now),
	})
}

// taken is conflict job-taken; sessions are those holding the job, none for a
// reservation.
func (s *launcher) taken(sessions []SessionRef) *Error {
	if sessions == nil {
		sessions = []SessionRef{}
	}
	return &Error{
		Kind:    KindConflict,
		Message: "the job " + s.job + " is held" + s.hint,
		Details: map[string]any{"rule": ruleJobTaken, "sessions": sessions},
	}
}

// write publishes the reservation over what is there.
func (s *launcher) write(rroot fsys.Root, f model.ReservationFile) *Error {
	data, err := jsonio.MarshalFile(f)
	if err != nil {
		return internal("encoding the reservation: %v", err)
	}
	name := f.Job + model.ReservationExt
	if err := fsys.Publish(rroot, name, data); err != nil {
		return IOError(filepath.Join(s.l.ReservationsDir(), name), err)
	}
	s.token = f.Token
	return nil
}

// failed is the error of a launch the backend did not complete, with the
// warnings so far. A refusal frees the job at once; an unknown outcome keeps
// the reservation, for the session that starts to adopt, and it strands in
// 120 seconds if none does.
func (s *launcher) failed(err error) Envelope {
	reason := reasonLaunchFailed
	if kitty.IsUnknown(err) {
		reason = reasonLaunchUnknown
	} else if s.job != "" {
		s.release()
	}
	return FailedWith(kittyError(reason, err.Error()), s.warnings)
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

// mine reads the job's reservation again and reports whether it is usable
// and still holds this run's token. One that is gone (the session adopted
// it), unusable, or another's (removed, and the job claimed again) is not
// this run's to touch.
func (s *launcher) mine(rroot fsys.Root) (r reservation, mine bool, err error) {
	if rroot == nil {
		return r, false, nil
	}
	r, gone, e := readReservation(rroot, s.l.ReservationsDir(), s.job)
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
			Details: map[string]any{"job": s.job, "placement": pl},
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
		return rroot.Remove(s.job + model.ReservationExt)
	})
}

// claimJob reserves the job when there is one.
func (s *launcher) claimJob() *Error {
	if s.job == "" {
		return nil
	}
	return s.claim()
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
	spec kitty.LaunchSpec
	// timeoutSecs is start_timeout_secs.
	timeoutSecs int64
	// read is one read of the wait, given the launched window: the session if
	// it has started, and the set read.
	read func(window int64) (*sessionSet, *sessionRec, *Error)
	// notStarted is the not-started message, with the seconds waited as %d;
	// extra adds to its details.
	notStarted string
	extra      map[string]any
}

// launch opens the window, then records it in the reservation and waits for
// the session.
func (s *launcher) launch(p launchPlan) Envelope {
	spec := p.spec
	var job *string
	if s.job != "" {
		job = &s.job
		spec.Env = []kitty.Var{{Name: "SESSHIN_JOB", Value: s.job}, {Name: "SESSHIN_TOKEN", Value: s.token}}
	}

	id, err := s.env.Launch(spec)
	if err != nil {
		return s.failed(err)
	}

	pl := kitty.PlacementOf(s.sock.Socket, id)
	if s.job != "" {
		s.record(pl)
	}
	out := SpawnOutput{Job: job, Placement: pl}
	var last *sessionSet
	if p.timeoutSecs > 0 {
		set, rec, e := s.poll(p.timeoutSecs, func() (*sessionSet, *sessionRec, *Error) { return p.read(id) })
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
