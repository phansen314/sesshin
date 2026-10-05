package hook

import (
	"bytes"
	"io"
	"path/filepath"
	"time"

	"github.com/phansen314/sesshin/internal/hookconf"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/testhook"
)

// Run runs the verb named by args[0] (hooks-spec.md, Registration); the rest
// of args is ignored. It reads stdin whole and decodes it, then:
//
//   - empty stdin is no payload: it returns at once, silently and unlogged;
//   - with no usable HOME it returns, logging nothing (Recording an event,
//     step 1);
//   - a missing or unknown verb is logged and does nothing, so a settings.json
//     written by a newer sesshin can't break a session run by an older one;
//   - a session_id that is missing or not a UUID is logged, and nothing is
//     recorded: it would become a path;
//   - a payload that stopped decoding early is logged, and the verb runs
//     with what decoded;
//   - otherwise the verb's function runs.
//
// The statusline runs in its own frame, which prints its one line (see
// runStatusline).
func Run(args []string, env Process) {
	start := env.Now()
	began := time.Now() // monotonic: the lock deadline's clock, apart from Process.Now's timestamps
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	if verb == "statusline" {
		runStatusline(env, start, began)
		return
	}
	c, ok := prepare(env, verb, start, began)
	if !ok {
		return
	}
	if f := verbFunc(verb); f != nil {
		f(c)
	}
}

// prepare reads stdin, decodes it, and makes the checks every verb passes
// before its function runs. It reports false when the verb must not run.
func prepare(env Process, verb string, start, began time.Time) (*Call, bool) {
	raw, _ := io.ReadAll(env.Stdin) // what was read still counts, as for a payload cut short
	p := payload.Decode(bytes.NewReader(raw))
	testhook.At("dispatch")
	if p.Empty {
		return nil, false
	}
	l, err := loc.Resolve(env.GOOS, env.Getenv)
	if err != nil {
		return nil, false
	}
	c := &Call{Verb: verb, Payload: p, Stdin: raw, FS: env.FS, Loc: l, Settings: hookconf.Default(), Now: start, Getenv: env.Getenv}
	switch {
	case verb == "":
		c.Log("no verb")
		return nil, false
	case verbFunc(verb) == nil && verb != "statusline":
		c.Log("unknown verb")
		return nil, false
	case p.SessionID == "":
		c.Log("session_id is missing or not a UUID")
		return nil, false
	}
	// A payload cut short or malformed still runs the verb with what decoded
	// (H6), but is logged: Claude Code never sends one, so it is worth seeing.
	if p.Err != nil {
		c.Log("payload: " + p.Err.Error())
	}
	// A bad hooks.properties is not logged: every hook would log it on every
	// run (hooks-spec.md, Log). Read returns the default with its error.
	c.Settings, _ = hookconf.Read(env.FS, filepath.Join(l.ConfigDir, hookconf.FileName))
	c.Deadline = lockDeadline(verb, began, c.Settings)
	return c, true
}

// verbFunc is the function of a registered verb other than statusline, which
// has its own frame, or nil for any other name.
func verbFunc(verb string) func(*Call) {
	switch verb {
	case "session-start":
		return sessionStart
	case "user-prompt":
		return userPrompt
	case "terminal-sync":
		return terminalSync
	case "post-tool-use":
		return postToolUse
	case "stop":
		return stop
	case "notification":
		return notification
	case "compact":
		return compact
	case "cwd-changed":
		return cwdChanged
	case "session-end":
		return sessionEnd
	}
	return nil
}
