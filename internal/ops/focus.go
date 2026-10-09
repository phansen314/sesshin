package ops

import (
	"fmt"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
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

// FocusEnv is what focus reads and does outside: a ReadEnv, whose backend
// finds the window (as send's) and focuses it.
type FocusEnv struct {
	ReadEnv
}

// OSFocusEnv is the real environment.
func OSFocusEnv() FocusEnv {
	return FocusEnv{ReadEnv: OSReadEnv()}
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
	in.Selector = in.Selector.resolveSelf(env.ReadEnv, set.recs)
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
	var (
		b      placement.Backend
		stored *jsonio.Object
		ok     bool
	)
	if rec.Sesshin != nil {
		stored = rec.Sesshin.Placement
		b, ok = env.valid(stored)
	}
	if !ok {
		return conflict(ruleNoPlace, "sesshin does not know the window of session "+v.Name+": it has no kitty placement")
	}
	focuser, canFocus := b.(placement.Focuser)
	if !canFocus {
		return fail(unsupported(b, "focus a window"))
	}

	window, verified := stored, false
	var lookup error
	if locator, ok := b.(placement.Locator); ok && v.PID != nil {
		var found *jsonio.Object
		if found, lookup = locator.Locate(stored, *v.PID, env.Getenv); lookup == nil {
			window, verified = found, true
		}
	}
	if err := focuser.Focus(window); err != nil {
		msg := err.Error()
		if lookup != nil && lookup.Error() != "" {
			msg = fmt.Sprintf("%s (no window running pid %d was found: %s)", msg, *v.PID, lookup)
		}
		return fail(backendError(b, reasonFocus, msg))
	}
	res := Succeeded(FocusOutput{
		Session:   v.ref(),
		Placement: b.Address(window),
		Verified:  verified,
		Attention: v.Attention,
	})
	res.Warnings = append(res.Warnings, warnings...)
	return res
}
