package ops

import (
	"cmp"
	"path/filepath"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
)

// ruleLive is resume's conflict (operations.md, Error kinds).
const ruleLive = "live"

// ruleOtherFormat is resume's conflict for a session whose sesshin.json is in
// another format, when it would resume under a job.
const ruleOtherFormat = "other-format"

// ResumeInput is resume's input (resume-input), with its defaults filled in.
type ResumeInput struct {
	Selector Selector
	// Job is the job to resume under instead of the session's own; "" for
	// that.
	Job              string
	Args             []string
	StartTimeoutSecs int64
}

// DecodeResumeInput is resume's own checks: session required, and a selector
// by the rules of Selecting a session; then job, args, and
// start_timeout_secs as spawn's are. No string in args holds a NUL (or is
// not UTF-8, which only the command line can supply).
func DecodeResumeInput(f *model.Fields, p *model.Problems) ResumeInput {
	in := ResumeInput{Selector: decodeSelector(f, p)}
	in.Job = decodeJob(f, p)
	in.Args = DecodeArgs(f, p)
	in.StartTimeoutSecs = decodeStartTimeout(f, p)
	return in
}

// ResumeOutput is resume's result (resume-output): spawn's.
type ResumeOutput = SpawnOutput

// resumer is one run of resume.
type resumer struct {
	launcher
	in ResumeInput
	// rec and view are the session being resumed.
	rec  *sessionRec
	view SessionView
}

// Resume reopens an ended session with claude --resume, in a new tab of the
// caller's terminal, under its job (or the one named), and waits for it to be
// live again (operations.md, resume). It is spawn's reservation, launch, and
// record around a selected session, holding the state lock only to reserve
// the job and to record the window.
func resumeOp(in ResumeInput, env SpawnEnv) Envelope {
	l, cfg, e := loadSetup(env.ReadEnv)
	if e != nil {
		return Failed(e)
	}
	s := &resumer{in: in, launcher: launcher{env: env, l: l, cfg: cfg, hint: "; resume under another job with job"}}
	if res, ok := s.selectSession(); !ok {
		return res
	}
	if e := s.preflight(); e != nil {
		return Failed(e)
	}
	if s.job != "" {
		// Only a resume under a job reserves: the session keeps its own
		// extra, so a resume with no job has nothing to hand over.
		if e := s.reserve(); e != nil {
			return Failed(e)
		}
	}
	return s.launchResume()
}

// selectSession reads every session as list does and selects the one to
// resume: among the ended ones for a job, among all for the other forms,
// which a live session or one of unknown liveness then refuses. It sets
// the session, its view, and the job to resume under. The failure carries
// the unusable files read, as show's does.
func (s *resumer) selectSession() (Envelope, bool) {
	set, _, e := readSessions(s.env.ReadEnv)
	if e != nil {
		return Failed(e), false
	}
	s.warnings = issueWarnings(set)

	vw := viewer{fs: s.env.FS, now: set.now}
	s.in.Selector = s.in.Selector.resolveSelf(s.env.ReadEnv, set.recs)
	pool := s.in.Selector.pool(set.recs, func(r *sessionRec) bool { return r.res.State == live.Ended })
	rec, e := selectOne(s.in.Selector, pool, vw)
	if e != nil {
		return FailedWith(e, s.warnings), false
	}
	s.rec = rec
	s.view = vw.view(s.rec)
	if s.rec.res.State != live.Ended {
		return FailedWith(&Error{
			Kind:    KindConflict,
			Message: "session " + s.view.Name + " is " + s.view.Liveness + ", not ended",
			Details: map[string]any{"rule": ruleLive, "sessions": []SessionRef{s.view.ref()}},
		}, s.warnings), false
	}

	s.job = s.in.Job
	if s.job == "" && s.rec.job != nil {
		s.job = *s.rec.job
	}
	if s.view.TranscriptExists != nil && !*s.view.TranscriptExists {
		s.warnings = append(s.warnings, Warning{
			Kind:    KindTranscriptMissing,
			Message: "the transcript " + *s.view.TranscriptPath + " is not there; claude --resume will likely fail",
			Details: map[string]any{"session": s.view.ref(), "path": *s.view.TranscriptPath},
		})
	}
	return Envelope{}, true
}

// preflight is what resume checks without a lock once it has the session:
// its cwd, the caller's terminal, and the job's reservation and its window.
func (s *resumer) preflight() *Error {
	cwd := ""
	if c := s.rec.Lifecycle.Cwd; c != nil {
		cwd = *c
	}
	if cwd == "" {
		return missingCwd(nil)
	}
	if !filepath.IsAbs(cwd) {
		return missingCwd(&cwd)
	}
	if e := checkDir(s.env.FS, cwd); e != nil {
		return e
	}
	if e := s.terminal(); e != nil {
		return e
	}
	if e := s.formatCheck(); e != nil {
		return e
	}
	return s.reserved()
}

// formatCheck refuses a resume under a job when the session's sesshin.json is
// in another format: the resumed session's session-start leaves such a file
// alone and never adopts the reservation, which would hold the job with no
// session. Without a job nothing is reserved, so the file doesn't matter.
func (s *resumer) formatCheck() *Error {
	if s.job == "" {
		return nil
	}
	path := filepath.Join(s.l.SessionDir(s.rec.ID), model.SesshinName)
	b, err := s.env.FS.ReadFile(path)
	if err != nil {
		return nil // missing, corrupt, or unreadable: not another format
	}
	_, r := model.ReadSesshin(b)
	if !r.OtherFormat {
		return nil
	}
	name := "session " + s.rec.ID[:8]
	format := itoa(r.Found)
	msg := name + "'s sesshin.json is in format " + format + ", older than this sesshin's, and a resume under a job would never adopt its reservation: run sesshin migrate first"
	if r.Found > model.SesshinSchema {
		msg = name + "'s sesshin.json is in format " + format + ", newer than this sesshin's, and a resume under a job would never adopt its reservation: upgrade sesshin first"
	}
	return &Error{
		Kind:    KindConflict,
		Message: msg,
		Details: map[string]any{"rule": ruleOtherFormat, "sessions": []SessionRef{s.view.ref()}, "path": path},
	}
}

// missingCwd is not-found for a cwd that was never recorded (nil) or is not
// an existing directory.
func missingCwd(cwd *string) *Error {
	paths := []string{}
	msg := "the session's cwd was never recorded"
	if cwd != nil {
		paths = []string{*cwd}
		msg = *cwd + " is not an existing directory"
	}
	return &Error{Kind: KindNotFound, Message: msg, Details: map[string]any{"selectors": []string{}, "paths": paths}}
}

// launchResume builds the launch and runs it.
func (s *resumer) launchResume() Envelope {
	title, vars := s.view.Name, []placement.Var(nil)
	if s.rec.Sesshin != nil {
		if t, v, ok := s.b.Stored(s.rec.Sesshin.Placement); ok {
			title, vars = cmp.Or(t, title), v
		}
	}
	args := append([]string{"--resume", s.rec.ID}, s.in.Args...)
	return s.launch(launchPlan{
		spec: placement.LaunchSpec{
			Caller: s.caller,
			Type:   typeTab,
			Cwd:    *s.rec.Lifecycle.Cwd,
			Title:  title,
			Vars:   vars,
			Argv:   ShellArgv(s.cfg.SpawnShell(s.env.Getenv), args, ""),
		},
		timeoutSecs: s.in.StartTimeoutSecs,
		read:        s.read,
		notStarted:  "the session was not live again within %d seconds; it may still start",
		extra:       map[string]any{"session": s.view.ref()},
	})
}

// read is one read of resume's wait, with no lock, of only the session's own
// directory: the session when it is live again (or of unknown liveness, as
// spawn's wait counts it).
func (s *resumer) read(*jsonio.Object) (*sessionSet, *sessionRec, *Error) {
	set, rec, e := readSession(s.env.ReadEnv, s.l, s.rec.ID)
	if rec != nil && rec.res.State == live.Ended {
		rec = nil
	}
	return set, rec, e
}
