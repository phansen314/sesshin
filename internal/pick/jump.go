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
	keys, e := pickJump(env, fzf, in.Query, opts, views)
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

// pickJump shows views, sorted, in fzf and returns the keys it printed. The
// lines and the preview files are rendered with the one now, so a file and
// its line can't disagree; the directory is removed as soon as fzf returns,
// before focus runs (picker-spec.md, Jump preview).
func pickJump(env JumpEnv, fzf, query string, opts []string, views []ops.SessionView) ([]string, *ops.Error) {
	base, err := tempBase(env.Getenv)
	if err != nil {
		return nil, ops.IOError(".", err)
	}
	now, home := env.Now(), env.Getenv("HOME")
	lines := renderJumpLines(views, now, home)
	dir, e := newPreviewDir(env.FS, base, "jump", views, func(v ops.SessionView) string { return jumpPreview(v, now, home, env.TabTitle) })
	if e != nil {
		return nil, e
	}
	defer dir.Remove()
	stdin := []byte(strings.Join(lines, "\n") + "\n")
	return runSelection(env.Sys, fzf, jumpArgs(dir.Path, query, opts), stdin, "nothing was focused")
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
// focus failed, it shows the error's kind and message on the terminal and waits for a
// key. It does nothing for a success, even an unverified focus, and for a
// cancel. A terminal that can't be opened or read is not an error: the
// envelope was delivered.
func ShowFailure(env ops.Envelope, sys System) {
	if !WillShowFailure(env, sys) {
		return
	}
	msg, _ := failureMessage(env)
	_ = sys.ShowFailure(msg)
}

// WillShowFailure reports whether ShowFailure will show env: a failure the
// terminal is asked to show, so the command leaves it out of stderr's note.
func WillShowFailure(env ops.Envelope, sys System) bool {
	_, ok := failureMessage(env)
	return ok && sys.ShowFailure != nil
}

// failureMessage is the line to show for env, "<kind>: <message>" as the
// stderr note has it.
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
	return scrub(e.Kind + ": " + e.Message), true
}
