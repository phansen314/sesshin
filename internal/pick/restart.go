package pick

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
)

// Input is restart's input (restart-input), with its defaults filled in.
type Input struct {
	// Query is the initial search text; "" for none.
	Query string
	// Args are passed to every resume, as its args; never nil.
	Args []string
}

// DecodeInput is restart's own checks: query a string, and args as resume's.
func DecodeInput(f *model.Fields, p *model.Problems) Input {
	in := Input{Args: []string{}}
	if v, ok := f.Optional("query"); ok {
		in.Query, _ = p.String(v, f.Ptr("query"))
	}
	in.Args = ops.DecodeArgs(f, p)
	return in
}

// Env is what restart runs against: what spawn and resume do, and the
// process.
type Env struct {
	ops.SpawnEnv
	Sys System
}

// OSEnv is the real environment.
func OSEnv() Env {
	return Env{SpawnEnv: ops.OSSpawnEnv(), Sys: OSSystem()}
}

// Output is restart's result (restart-output).
type Output struct {
	Actions []Action `json:"actions"`
}

// Action is one operation restart ran.
type Action struct {
	Operation string       `json:"operation"`
	Input     ResumeInput  `json:"input"`
	Output    ops.Envelope `json:"output"`
}

// ResumeInput is the input of a resume, as restart passed it.
type ResumeInput struct {
	Session          string   `json:"session"`
	Args             []string `json:"args"`
	StartTimeoutSecs int64    `json:"start_timeout_secs"`
}

// Restart lists the ended sessions, has the person pick among them in fzf,
// and resumes each pick in a new tab (picker-spec.md, restart). Its checks
// come in the Errors order; once fzf has accepted, it raises nothing more.
func Restart(in Input, env Env) ops.Envelope {
	if e := ops.CheckSetup(env.ReadEnv); e != nil {
		return ops.Failed(e)
	}
	fzf, e := findFzf(env.Sys, "restart")
	if e != nil {
		return ops.Failed(e)
	}
	environ := env.Sys.Environ()
	opts, e := userOpts(environ)
	if e != nil {
		return ops.Failed(e)
	}
	if e := ops.CheckTerminal(env.SpawnEnv); e != nil {
		return ops.Failed(e)
	}

	loaded := ops.List(ops.ListInput{Liveness: "ended"}, env.ReadEnv)
	if !loaded.OK {
		return loaded
	}
	var views []ops.SessionView
	for _, s := range loaded.Result.(ops.ListOutput).Sessions {
		views = append(views, s.(ops.SessionView))
	}
	out := Output{Actions: []Action{}}
	res := ops.Succeeded(out)
	res.Warnings = loaded.Warnings
	if len(views) == 0 {
		return res
	}

	if err := env.Sys.OpenTTY(); err != nil {
		return ops.FailedWith(unavailableErr("no terminal: /dev/tty does not open; restart is for a person at a terminal", noTerminal), loaded.Warnings)
	}
	picked, e := pick(env, fzf, in.Query, opts, views)
	if e != nil {
		return ops.FailedWith(e, loaded.Warnings)
	}

	for _, v := range picked {
		out.Actions = append(out.Actions, resume(v.SessionID, in.Args, env))
	}
	res.Result = out
	return res
}

// pick shows views in fzf and returns the ones picked, in line order (picker-spec.md,
// Outcomes).
func pick(env Env, fzf, query string, opts []string, views []ops.SessionView) ([]ops.SessionView, *ops.Error) {
	base, err := tempBase(env.Getenv)
	if err != nil {
		return nil, ops.IOError(".", err)
	}
	now := env.Now()
	dir, e := newPreviewDir(env.FS, base, "restart", views, func(v ops.SessionView) string { return preview(v, now) })
	if e != nil {
		return nil, e
	}
	defer dir.Remove()

	lines := renderLines(views, now, env.Getenv("HOME"))
	stdin := []byte(strings.Join(lines, "\n") + "\n")
	keyList, e := runSelection(env.Sys, fzf, args(dir.Path, query, opts), stdin, "nothing was resumed")
	if e != nil || keyList == nil {
		return nil, e
	}
	// The keys fzf printed. One not offered is ignored.
	keys := map[string]bool{}
	for _, key := range keyList {
		keys[key] = true
	}
	var picked []ops.SessionView
	for _, v := range views {
		if keys[v.SessionID] {
			picked = append(picked, v)
		}
	}
	return picked, nil
}

// resume runs resume on a session and records it, whatever came of it.
func resume(session string, args []string, env Env) Action {
	in := ResumeInput{Session: session, Args: slices.Clone(args), StartTimeoutSecs: 0}
	a := Action{Operation: "resume", Input: in}
	data, err := json.Marshal(in)
	if err != nil {
		a.Output = ops.Failed(&ops.Error{Kind: ops.KindInternal, Message: "encoding resume's input: " + err.Error()})
		return a
	}
	ri, e := ops.DecodeInput(data, ops.DecodeResumeInput)
	if e != nil {
		a.Output = ops.Failed(e)
		return a
	}
	a.Output = ops.Resume(ri, env.SpawnEnv)
	return a
}
