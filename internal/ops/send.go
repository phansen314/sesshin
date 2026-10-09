package ops

import (
	"fmt"
	"unicode/utf8"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
)

// Send's rules and reasons (operations.md, send and Error kinds).
const (
	maxSendText   = 1 << 20
	ruleNotLive   = "not-live"
	ruleMidTurn   = "mid-turn"
	ruleNoPlace   = "no-placement"
	reasonUnreach = "unreachable"
	reasonSendBad = "send-failed"
	reasonSubmit  = "submit-failed"
)

// SendInput is send's input (send-input), with its defaults filled in.
type SendInput struct {
	Selector Selector
	Text     string
	Submit   bool
	Force    bool
}

// DecodeSendInput is send's own checks: session required, and a selector by
// the rules of Selecting a session; text required and not empty, then its
// Additional validation (at most 1 MiB of UTF-8, no control character but
// tab, line feed, and carriage return); submit and force booleans.
func DecodeSendInput(f *model.Fields, p *model.Problems) SendInput {
	in := SendInput{Selector: decodeSelector(f, p), Submit: true}
	if v, ok := f.Required("text"); ok {
		ptr := f.Ptr("text")
		if s, ok := p.String(v, ptr); ok {
			switch {
			case s == "":
				p.Add(ptr, "must not be empty")
			case len(s) > maxSendText:
				p.AddAdditional(ptr, fmt.Sprintf("at most %d bytes of UTF-8", maxSendText))
			case !utf8.ValidString(s):
				p.AddAdditional(ptr, "not valid UTF-8")
			default:
				if reason := controlIn(s); reason != "" {
					p.AddAdditional(ptr, reason)
				}
			}
			in.Text = s
		}
	}
	if v, ok := f.Optional("submit"); ok {
		in.Submit, _ = p.Bool(v, f.Ptr("submit"))
	}
	if v, ok := f.Optional("force"); ok {
		in.Force, _ = p.Bool(v, f.Ptr("force"))
	}
	return in
}

// controlIn is the reason for the first character of s that a paste cannot
// carry, naming its class; "" for none. Tab, line feed, and carriage return
// are allowed.
func controlIn(s string) string {
	for _, r := range s {
		switch {
		case r == '\t', r == '\n', r == '\r':
		case r == 0x1b:
			return "must not contain ESC (U+001B), which could end the paste early"
		case r < 0x20:
			return fmt.Sprintf("must not contain a C0 control character (U+%04X) other than tab, line feed, and carriage return", r)
		case r == 0x7f:
			return "must not contain DEL (U+007F)"
		case r >= 0x80 && r <= 0x9f:
			return fmt.Sprintf("must not contain a C1 control character (U+%04X)", r)
		}
	}
	return ""
}

// SendOutput is send's result (send-output).
type SendOutput struct {
	Session        SessionRef     `json:"session"`
	Placement      *jsonio.Object `json:"placement"`
	Submitted      bool           `json:"submitted"`
	Status         string         `json:"status"`
	PermissionMode *string        `json:"permission_mode"`
	EventSeq       int64          `json:"event_seq"`
}

// SendEnv is what send reads and does outside: a ReadEnv, whose backend finds
// the window and pastes into it. Nothing in ops runs a process itself.
type SendEnv struct {
	ReadEnv
}

// OSSendEnv is the real environment.
func OSSendEnv() SendEnv {
	return SendEnv{ReadEnv: OSReadEnv()}
}

// Send types text into a live session's window (operations.md, send). It
// takes no lock and writes no file: the window is found afresh by the
// session's pid, never taken from the stored placement.
func sendOp(in SendInput, env SendEnv) Envelope {
	set, _, e := readSessions(env.ReadEnv)
	if e != nil {
		return Failed(e)
	}
	warnings := issueWarnings(set)
	fail := func(e *Error) Envelope { return FailedWith(e, warnings) }

	// A job selects among the live sessions; the other forms among all, so
	// an ended session is named in the refusal.
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
	switch {
	case rec.res.State == live.Ended:
		return conflict(ruleNotLive, "session "+v.Name+" has ended")
	case !in.Force && v.Status != model.StatusWaiting && v.Status != model.StatusIdle:
		return conflict(ruleMidTurn, "session "+v.Name+" is "+v.Status+", not at the end of a turn; force sends anyway")
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
	locator, canLocate := b.(placement.Locator)
	sender, canSend := b.(placement.Sender)
	switch {
	case !canLocate:
		return fail(unsupported(b, "find a window by pid"))
	case !canSend:
		return fail(unsupported(b, "paste text into a window"))
	case v.PID == nil:
		return fail(backendError(b, reasonUnreach, "the pid of session "+v.Name+" is unknown, so its window cannot be verified"))
	}

	window, err := locator.Locate(stored, *v.PID, env.Getenv)
	if err == nil {
		if err := sender.Send(window, in.Text, in.Submit); err != nil {
			reason := reasonSendBad
			if placement.IsSubmit(err) {
				reason = reasonSubmit
			}
			return fail(backendError(b, reason, err.Error()))
		}
		res := Succeeded(SendOutput{
			Session:        v.ref(),
			Placement:      b.Address(window),
			Submitted:      in.Submit,
			Status:         v.Status,
			PermissionMode: v.PermissionMode,
			EventSeq:       v.EventSeq,
		})
		res.Warnings = append(res.Warnings, warnings...)
		return res
	}
	return fail(backendError(b, reasonUnreach, fmt.Sprintf("no window running pid %d was found (%s)", *v.PID, err)))
}
