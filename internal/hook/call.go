package hook

import (
	"io"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/hookconf"
	"github.com/phansen314/sesshin/internal/hooklog"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/backends"
	"github.com/phansen314/sesshin/internal/record"
)

// Process is what Run takes from the process: main passes the real ones, and
// tests their own.
type Process struct {
	Stdin  io.Reader
	Stdout io.Writer
	FS     fsys.FS
	Getenv func(string) string
	GOOS   string
	// Now is called once, when the hook starts.
	Now func() time.Time
}

// Call is what a verb's function takes: one hook run, after the checks every
// verb passes (see Run).
type Call struct {
	// Verb is the first argument, as given.
	Verb string
	// Payload is the decoded stdin; its SessionID is a lowercased UUID.
	Payload payload.Payload
	// Stdin is the raw stdin, which the statusline stores.
	Stdin    []byte
	FS       fsys.FS
	Loc      loc.Locations
	Settings hookconf.Settings
	// Now is when the hook started, by Process.Now.
	Now time.Time
	// Deadline is the hook's lock deadline (hooks-spec.md, The contract, H4):
	// every lock wait draws on it. It is a point on the monotonic clock, set
	// when the hook starts, apart from Now, which Process.Now may fake.
	Deadline time.Time
	Getenv   func(string) string
}

// lockDeadline is the hook's lock deadline, counted from began: twice
// hook_lock_wait_ms, or min(hook_lock_wait_ms, 1 second) for session-end,
// which must give up inside Claude Code's exit budget (H4).
func lockDeadline(verb string, began time.Time, s hookconf.Settings) time.Time {
	wait := s.LockWait()
	if verb == "session-end" {
		return began.Add(min(wait, time.Second))
	}
	return began.Add(2 * wait)
}

// RecordEnv is what record needs from this call: the lifecycle verbs build
// their record.Event and pass it, with this, to record.Record. The terminal
// backend is the one this call's environment names (Backend); every verb
// gets it, so a session adopted late is placed too, and record lets only
// SessionStart replace a placement that exists.
func (c *Call) RecordEnv() record.Env {
	return record.Env{
		FS:        c.FS,
		Loc:       c.Loc,
		SessionID: c.Payload.SessionID,
		Now:       c.Now,
		LockWait:  c.Settings.LockWait(),
		Deadline:  c.Deadline,
		Getenv:    c.Getenv,
		Log:       c.Log,
		Placement: c.placement,
	}
}

// Backend is the terminal backend this call's environment names, and the
// placement it recognized; nil for none (design-spec.md, Terminal backends).
// It reads the environment and starts no process.
func (c *Call) Backend() (placement.Backend, *jsonio.Object) {
	return placement.Detect(backends.All, c.Getenv)
}

// placement is record.Env.Placement: the detected backend's placement, which
// keeps the sync-only keys of old (Backend.Replace); resumed says the session
// is being resumed. Outside every backend's terminal it is nil.
func (c *Call) placement(old *jsonio.Object, resumed bool) *jsonio.Object {
	b, p := c.Backend()
	if b == nil {
		return nil
	}
	return b.Replace(p, old, resumed)
}

// Log appends msg to <state>/hooks.log, under the hook's verb and session
// (hooks-spec.md, Log). A hook that has a line to log when <state> doesn't
// exist creates it, mode 0700, and hooks.log in it, and nothing else. A
// failure is dropped: H7 logs instead of failing, and there is nowhere else
// to report one.
func (c *Call) Log(msg string) {
	if c.Loc.StateDir == "" {
		return
	}
	state, err := fsys.OpenRootCreate(c.FS, c.Loc.StateDir)
	if err != nil {
		return
	}
	defer state.Close()
	_ = hooklog.Append(state, c.Now, c.Verb, c.Payload.SessionID, msg)
}
