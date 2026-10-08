package ops

import (
	"fmt"
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// reasonFocus is focus's terminal reason (operations.md, Error kinds).
const reasonFocus = "focus-failed"

// FocusInput is focus's input (focus-input).
type FocusInput struct {
	Selector Selector
}

// DecodeFocusInput is focus's own checks: session required, and a selector by
// the rules of Selecting a session. It has no Additional validation.
func DecodeFocusInput(f *model.Fields, p *model.Problems) FocusInput {
	return FocusInput{Selector: decodeSelector(f, p)}
}

// FocusOutput is focus's result (focus-output).
type FocusOutput struct {
	Session   SessionRef     `json:"session"`
	Placement *jsonio.Object `json:"placement"`
	Verified  bool           `json:"verified"`
	Attention *string        `json:"attention"`
}

// FocusEnv is what focus reads and does outside: a ReadEnv, the backend's
// window lookup (as send's), and its focus-window call.
type FocusEnv struct {
	ReadEnv
	// FindWindow returns the ID of the window on socket whose foreground
	// processes include pid; any error is that socket's no.
	FindWindow func(socket string, pid int64) (int64, error)
	// Focus brings the window to the front.
	Focus func(socket string, window int64) error
}

// OSFocusEnv is the real environment.
func OSFocusEnv() FocusEnv {
	return FocusEnv{ReadEnv: OSReadEnv(), FindWindow: kitty.WindowForPID, Focus: kitty.FocusWindow}
}

// focusOp brings a live session's window to the front (operations.md,
// focus). It takes no lock and writes no file. The window is found as send
// finds it; with none verified, the stored one is focused.
func focusOp(in FocusInput, env FocusEnv) Envelope {
	set, _, e := readSessions(env.ReadEnv)
	if e != nil {
		return Failed(e)
	}
	warnings := issueWarnings(set)
	fail := func(e *Error) Envelope { return FailedWith(e, warnings) }

	vw := viewer{fs: env.FS, now: set.now}
	pool := in.Selector.pool(set.recs, func(r *sessionRec) bool { return r.res.State != live.Ended })
	rec, e := selectOne(in.Selector, pool, vw)
	if e != nil {
		return fail(e)
	}
	v := vw.view(rec)
	conflict := func(rule, msg string) Envelope {
		return fail(&Error{
			Kind:    KindConflict,
			Message: msg,
			Details: map[string]any{"rule": rule, "sessions": []SessionRef{v.ref()}},
		})
	}
	if rec.res.State == live.Ended {
		return conflict(ruleNotLive, "session "+v.Name+" has ended")
	}
	var stored kitty.Parsed
	if rec.Sesshin != nil {
		stored, _ = kitty.Parse(rec.Sesshin.Placement)
	}
	if stored.Socket == "" {
		return conflict(ruleNoPlace, "sesshin does not know the window of session "+v.Name+": it has no kitty placement")
	}

	socket, window, verified := stored.Socket, stored.WindowID, false
	var tried []string
	if v.PID != nil {
		var found int64
		var s string
		if s, found, tried = findWindow(env.FindWindow, env.Getenv, stored.Socket, *v.PID); found != 0 {
			socket, window, verified = s, found, true
		}
	}
	if err := env.Focus(socket, window); err != nil {
		msg := err.Error()
		if !verified && len(tried) > 0 {
			msg = fmt.Sprintf("%s (no window running pid %d was found: %s)", msg, *v.PID, strings.Join(tried, "; "))
		}
		return fail(kittyError(reasonFocus, msg))
	}
	res := Succeeded(FocusOutput{
		Session:   v.ref(),
		Placement: kitty.PlacementOf(socket, window),
		Verified:  verified,
		Attention: v.Attention,
	})
	res.Warnings = append(res.Warnings, warnings...)
	return res
}
