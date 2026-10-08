package pick

import (
	"encoding/json"
	"strings"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
)

// JumpInput is jump's input (jump-input), with its defaults filled in.
type JumpInput struct {
	// Query is the initial search text; "" for none.
	Query string
}

// DecodeJumpInput is jump's own checks: query a string.
func DecodeJumpInput(f *model.Fields, p *model.Problems) JumpInput {
	var in JumpInput
	if v, ok := f.Optional("query"); ok {
		in.Query, _ = p.String(v, f.Ptr("query"))
	}
	return in
}

// JumpEnv is what jump runs against: what focus does, and the process.
type JumpEnv struct {
	ops.FocusEnv
	Sys System
}

// OSJumpEnv is the real environment.
func OSJumpEnv() JumpEnv {
	return JumpEnv{FocusEnv: ops.OSFocusEnv(), Sys: OSSystem()}
}

// JumpOutput is jump's result (jump-output).
type JumpOutput struct {
	Actions []JumpAction `json:"actions"`
}

// JumpAction is the focus jump ran.
type JumpAction struct {
	Operation string       `json:"operation"`
	Input     FocusInput   `json:"input"`
	Output    ops.Envelope `json:"output"`
}

// FocusInput is the input of a focus, as jump passed it.
type FocusInput struct {
	Session string `json:"session"`
}

// Jump lists the live sessions, has the person pick one in fzf, in jump
// order, and focuses it (picker-spec.md, jump). Its checks come in the
// Errors order; once fzf has accepted, it raises nothing more.
func Jump(in JumpInput, env JumpEnv) ops.Envelope {
	if e := ops.CheckSetup(env.ReadEnv); e != nil {
		return ops.Failed(e)
	}
	fzf, e := findFzf(env.Sys, "jump")
	if e != nil {
		return ops.Failed(e)
	}
	opts, e := userOpts(env.Sys.Environ())
	if e != nil {
		return ops.Failed(e)
	}

	loaded := ops.List(ops.ListInput{Liveness: "live"}, env.ReadEnv)
	if !loaded.OK {
		return loaded
	}
	var views []ops.SessionView
	for _, s := range loaded.Result.(ops.ListOutput).Sessions {
		views = append(views, s.(ops.SessionView))
	}
	out := JumpOutput{Actions: []JumpAction{}}
	res := ops.Succeeded(out)
	res.Warnings = loaded.Warnings
	if len(views) == 0 {
		return res
	}

	if err := env.Sys.OpenTTY(); err != nil {
		return ops.FailedWith(unavailableErr("no terminal: /dev/tty does not open; jump is for a person at a terminal", noTerminal), loaded.Warnings)
	}
	// Sorted once, here: a cache that expires while fzf is open doesn't move
	// its line.
	sortJump(views)
	lines := renderJumpLines(views, env.Now(), env.Getenv("HOME"))
	stdin := []byte(strings.Join(lines, "\n") + "\n")
	keys, e := runSelection(env.Sys, fzf, jumpArgs(in.Query, opts), stdin, "cancelled: nothing was focused")
	if e != nil {
		return ops.FailedWith(e, loaded.Warnings)
	}
	// The line under the cursor: the first key printed. One not offered is
	// ignored.
	for _, v := range views {
		if len(keys) > 0 && keys[0] == v.SessionID {
			out.Actions = append(out.Actions, focus(v.SessionID, env))
			break
		}
	}
	res.Result = out
	return res
}

// focus runs focus on a session and records it, whatever came of it.
func focus(session string, env JumpEnv) JumpAction {
	in := FocusInput{Session: session}
	a := JumpAction{Operation: "focus", Input: in}
	data, err := json.Marshal(in)
	if err != nil {
		a.Output = ops.Failed(&ops.Error{Kind: ops.KindInternal, Message: "encoding focus's input: " + err.Error()})
		return a
	}
	fi, e := ops.DecodeInput(data, ops.DecodeFocusInput)
	if e != nil {
		a.Output = ops.Failed(e)
		return a
	}
	a.Output = ops.Focus(fi, env.FocusEnv)
	return a
}

// ShowFailure is jump's step 6, to run after its envelope has been written
// to stdout: when the envelope is a failure other than cancelled, or the
// focus failed, it shows the error's message on the terminal and waits for a
// key. It does nothing for a success, even an unverified focus, and for a
// cancel. A terminal that can't be opened or read is not an error: the
// envelope was delivered.
func ShowFailure(env ops.Envelope, sys System) {
	msg, ok := failureMessage(env)
	if !ok || sys.ShowFailure == nil {
		return
	}
	_ = sys.ShowFailure(msg)
}

// failureMessage is the message to show for env, as one line.
func failureMessage(env ops.Envelope) (string, bool) {
	var e *ops.Error
	switch {
	case !env.OK:
		e = env.Error
	default:
		if out, ok := env.Result.(JumpOutput); ok {
			for _, a := range out.Actions {
				if !a.Output.OK {
					e = a.Output.Error
				}
			}
		}
	}
	if e == nil || e.Kind == KindCancelled {
		return "", false
	}
	return scrub(e.Message), true
}
