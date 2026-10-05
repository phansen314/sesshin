package statusline

import (
	"errors"
	"io/fs"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/proc"
	"github.com/phansen314/sesshin/internal/testhook"
)

// FallbackLine is the line printed when there is nothing to render, with no
// trailing newline (hooks-spec.md, Rendering). It has no sesshin ID: the ID needs
// a session, and this path has none, or failed before reading one.
const FallbackLine = "🧠 0%"

// clockStepBack is how far ahead of a tick a stored received_ns may be and
// still count as newer. Ticks that overlap are about 300 ms apart; a stored
// time further ahead than this was written before the wall clock stepped back.
const clockStepBack = time.Minute

// Tick is one run of the statusline, as recording takes it.
type Tick struct {
	FS fsys.FS
	// SessionDir is the session's directory, sessions/<uuid> under the state
	// directory. It need not exist.
	SessionDir string
	// Now is when the tick started: received_ns and received_at.
	Now time.Time
	// Stdin is the raw payload, stored verbatim.
	Stdin []byte
	// Payload is Stdin decoded. Its SessionID, a lowercased UUID, is the one
	// lifecycle.json must name; its Err, when set, has been logged already.
	Payload payload.Payload
	// ClaudePID is CLAUDE_PID from the environment, "" when unset.
	ClaudePID string
	// Lookup looks up Claude's process on every tick; nil is proc.Find. A
	// test injects its own.
	Lookup func(fsys.FS, string) proc.Claude
	// Log records a line in hooks.log.
	Log func(string)
}

// Found is what steps 1–4 found. Every field is its step's result, and the
// zero value when the step found nothing or panicked: no previous tick, no
// Claude, no branch, no sample, no burn rate.
type Found struct {
	// Previous is the previous statusline.json, nil when missing or
	// unusable.
	Previous *model.StatuslineFile
	Claude   proc.Claude
	// GitBranch is nil when the cwd is in no repository or has no branch.
	GitBranch *string
	// CostSample is what the next tick takes as the burn rate's base.
	CostSample *model.CostSample
	// BurnUSDPerHour is nil when it can't be told.
	BurnUSDPerHour *float64
}

// steps are the five steps recording makes, each run under its own recover.
// Tests replace them to inject a panic.
type steps struct {
	previous func(Tick, *session) *model.StatuslineFile
	claude   func(Tick) proc.Claude
	branch   func(Tick) *string
	burn     func(Tick, *model.StatuslineFile) burnResult
	write    func(Tick, *session, Found)
}

type burnResult struct {
	sample *model.CostSample
	rate   *float64
}

func defaultSteps() steps {
	return steps{previous: readPrevious, claude: findClaude, branch: gitBranch, burn: computeBurn, write: writeFile}
}

// Run is the statusline's tick (hooks-spec.md, statusline, steps 1–6): it
// collects what steps 1–4 find, renders the line (step 5), and writes
// statusline.json (step 6), in that order, and returns the line to print. The
// line is built into a buffer before step 6 writes, as the spec orders them.
// Each step recovers its own panic and logs it, so a panic costs that step's
// result, and the line falls back to the one with the context segment alone
// when step 5 panics. loc is the zone times of day are shown in.
//
// The session directory is opened once, and lifecycle.json read once, for all
// of them.
func Run(t Tick, loc *time.Location) []byte {
	return run(t, loc, defaultSteps(), Render)
}

func run(t Tick, loc *time.Location, s steps, render func(View) []byte) []byte {
	ses := openSession(t)
	defer ses.close()
	found := collect(t, ses, s)
	buf := renderTick(t, ses, found, loc, render)
	write(t, ses, found, s)
	return buf
}

func collect(t Tick, ses *session, s steps) Found {
	var f Found
	f.Previous = guard(t, "statusline:1", "step 1 (previous statusline.json)", nil, func() *model.StatuslineFile { return s.previous(t, ses) })
	f.Claude = guard(t, "statusline:2", "step 2 (process lookup)", proc.Claude{}, func() proc.Claude { return s.claude(t) })
	f.GitBranch = guard(t, "statusline:3", "step 3 (git branch)", nil, func() *string { return s.branch(t) })
	b := guard(t, "statusline:4", "step 4 (burn rate)", burnResult{}, func() burnResult { return s.burn(t, f.Previous) })
	f.CostSample, f.BurnUSDPerHour = b.sample, b.rate
	return f
}

func write(t Tick, ses *session, f Found, s steps) {
	guard(t, "statusline:6", "step 6 (write)", struct{}{}, func() struct{} { s.write(t, ses, f); return struct{}{} })
}

// guard runs fn, and returns what it returns, or zero when it panics, after
// logging the panic (hooks-spec.md, statusline). The step's test hook,
// statusline:<n>, fires inside it.
func guard[T any](t Tick, point, step string, zero T, fn func() T) (v T) {
	defer func() {
		if r := recover(); r != nil {
			v = zero
			t.Log("statusline " + step + ": panic: " + describe(r))
		}
	}()
	testhook.At(point)
	return fn()
}

// describe is a recovered value as a log message, without fmt.
func describe(r any) string {
	switch v := r.(type) {
	case error:
		return v.Error()
	case string:
		return v
	case interface{ String() string }:
		return v.String()
	}
	return "unknown value"
}

// session is what the steps read of the session directory: its root, opened
// once, and lifecycle.json, read once.
type session struct {
	// root is nil when the directory doesn't exist or can't be opened.
	root fsys.Root
	// life is lifecycle.json, meaningful when lifeStatus is usable; lifeLog
	// is the log line for one that is unreadable or unusable, which step 6
	// logs.
	life       model.LifecycleFile
	lifeStatus model.FileStatus
	lifeLog    string
}

// openSession opens the session directory and reads lifecycle.json. A
// directory that is missing is not logged; one that can't be opened is.
func openSession(t Tick) *session {
	ses := &session{}
	root, err := t.FS.OpenRoot(t.SessionDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			t.Log("open session directory: " + fsys.Describe(err))
		}
		return ses
	}
	ses.root = root
	ses.life, ses.lifeStatus, ses.lifeLog = readFile(root, model.LifecycleName, func(data []byte) (model.LifecycleFile, model.FileResult) {
		return model.ReadLifecycle(data, t.Payload.SessionID)
	})
	return ses
}

func (ses *session) close() {
	if ses.root != nil {
		ses.root.Close()
	}
}

// readFile reads name from root and validates it with read (model.ReadIn).
// It returns the file (meaningful only when usable), how the read ended, and,
// for a file that is unreadable or unusable, the line to log. Whether to log
// it is the caller's.
func readFile[T any](root fsys.Root, name string, read func([]byte) (T, model.FileResult)) (v T, st model.FileStatus, msg string) {
	v, _, st, err := model.ReadIn(root, name, read)
	if err != nil {
		msg = err.Error()
	}
	return v, st, msg
}

// readPrevious is step 1. A missing file is no previous tick; an unreadable
// or unusable one is logged, and is no previous tick either.
func readPrevious(t Tick, ses *session) *model.StatuslineFile {
	if ses.root == nil {
		return nil
	}
	s, st, msg := readFile(ses.root, model.StatuslineName, model.ReadStatusline)
	if msg != "" {
		t.Log(msg)
	}
	if st != model.FileUsable {
		return nil
	}
	return &s
}

// findClaude is step 2, on every tick: the previous tick's process may be
// alive but no longer this session's (hooks-spec.md, statusline step 2).
func findClaude(t Tick) proc.Claude {
	return proc.FindWith(t.Lookup, t.FS, t.ClaudePID)
}

// writeFile is step 6, with no lock: it writes the temp file, re-reads the
// stored received_ns, and renames only if the stored one isn't newer than
// this tick's; a stored file that is unusable or missing counts as older, and
// so does one more than clockStepBack ahead, written before the wall clock
// stepped back. A session directory with no usable lifecycle.json is not
// written to: an unreadable or unusable one is logged.
func writeFile(t Tick, ses *session, f Found) {
	if ses.root == nil {
		return
	}
	switch ses.lifeStatus {
	case model.FileMissing:
		return
	case model.FileUnreadable, model.FileUnusable:
		t.Log(ses.lifeLog)
		return
	}
	raw, err := jsonio.Payload(t.Stdin)
	if err != nil {
		// A payload that didn't decode has been logged by the hook.
		if t.Payload.Err == nil {
			t.Log("payload not stored: " + err.Error())
		}
		return
	}
	file := model.StatuslineFile{
		ReceivedAt:     model.FormatTimestamp(t.Now),
		ReceivedNS:     t.Now.UnixNano(),
		Payload:        model.Payload{Raw: raw},
		GitBranch:      f.GitBranch,
		CostSample:     f.CostSample,
		BurnUSDPerHour: f.BurnUSDPerHour,
	}
	if f.Claude.PID > 0 {
		pid, started := f.Claude.PID, f.Claude.StartedAt
		file.PID, file.PIDStartedAt = &pid, &started
	}
	out, err := jsonio.MarshalFile(file)
	if err != nil {
		t.Log("write " + model.StatuslineName + ": " + fsys.Describe(err))
		return
	}
	p, err := fsys.Prepare(ses.root, model.StatuslineName, out)
	if err != nil {
		t.Log("write " + model.StatuslineName + ": " + fsys.Describe(err))
		return
	}
	// Exactly one of Commit and Abort must follow Prepare, a panic between
	// them included.
	committed := false
	defer func() {
		if !committed {
			p.Abort()
		}
	}()
	if stored, ok := storedNS(ses.root); ok && stored > file.ReceivedNS && time.Duration(stored-file.ReceivedNS) <= clockStepBack {
		return
	}
	committed = true
	if err := p.Commit(); err != nil {
		t.Log("write " + model.StatuslineName + ": " + fsys.Describe(err))
	}
}

// storedNS is the received_ns of the stored statusline.json, false when it
// is missing or unusable.
func storedNS(root fsys.Root) (int64, bool) {
	s, st, _ := readFile(root, model.StatuslineName, model.ReadStatusline)
	return s.ReceivedNS, st == model.FileUsable
}
