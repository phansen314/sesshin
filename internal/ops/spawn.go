package ops

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/loc"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/templates"
)

// The spawn types (spawn-input).
var spawnTypes = []string{typeTab, "split", "os-window"}

const (
	typeTab             = "tab"
	defaultStartTimeout = 15
	maxStartTimeout     = 120
	reasonTemplateName  = "must match ^(?![0-9]+$)[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$"
	reasonJob           = "must match ^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$ and not be all digits"
)

// DecodeSpawnInput is spawn's own checks: the schema's, then its Additional
// validation: cwd absolute, no NUL in any string (which no argument vector
// can carry), each vars key a name. Every string must be UTF-8 too, which
// --input's reader has already ensured and the command line does not. The
// input is as given: no default is filled in, since a template's value must
// win over one (operations.md, Merging a template). cwd is not required
// here; the merged input must have one.
func DecodeSpawnInput(f *model.Fields, p *model.Problems) model.SpawnInput {
	in := model.SpawnInput{Args: []string{}}
	if v, ok := f.Optional("template"); ok {
		ptr := f.Ptr("template")
		if s, ok := p.String(v, ptr); ok {
			if model.IsTemplateName(s) {
				in.Template = s
			} else {
				p.Add(ptr, reasonTemplateName)
			}
		}
	}
	in.Job = decodeJob(f, p)
	if v, ok := f.Optional("cwd"); ok {
		ptr := f.Ptr("cwd")
		if s, ok := checkedText(p, v, ptr); ok {
			if !filepath.IsAbs(s) {
				p.AddAdditional(ptr, "must be an absolute path")
			}
			in.Cwd = s
		}
	}
	if v, ok := f.Optional("type"); ok {
		ptr := f.Ptr("type")
		if s, ok := p.String(v, ptr); ok {
			if !slices.Contains(spawnTypes, s) {
				p.Add(ptr, "must be one of "+strings.Join(spawnTypes, ", "))
			} else {
				in.Type = s
			}
		}
	}
	if v, ok := f.Optional("name"); ok {
		ptr := f.Ptr("name")
		if s, ok := checkedText(p, v, ptr); ok && s == "" {
			p.Add(ptr, "must not be empty")
		} else {
			in.Name = s
		}
	}
	if v, ok := f.Optional("prompt"); ok {
		if s, ok := checkedText(p, v, f.Ptr("prompt")); ok {
			in.Prompt = &s
		}
	}
	in.Args = DecodeArgs(f, p)
	if v, ok := f.Optional("vars"); ok {
		ptr := f.Ptr("vars")
		o, isObj := v.(*jsonio.Object)
		if !isObj {
			p.Add(ptr, "expected a JSON object")
		} else {
			for _, m := range o.Members {
				mptr := jsonio.Pointer(ptr, m.Key)
				if !model.IsVarKey(m.Key) {
					p.AddAdditional(mptr, "must match ^[A-Za-z_][A-Za-z0-9_]{0,63}$")
				}
				if s, ok := checkedText(p, m.Value, mptr); ok {
					in.Vars = append(in.Vars, placement.Var{Name: m.Key, Value: s})
				}
			}
		}
	}
	if v, ok := f.Optional("extra"); ok {
		in.Extra = decodeExtra(p, v, f.Ptr("extra"))
	}
	in.StartTimeoutSecs = optionalStartTimeout(f, p)
	return in
}

// decodeExtra checks that v, at ptr, is an object within extra's limits
// (design-spec.md, User-owned extra), its numbers kept as given. It returns
// nil on a problem.
func decodeExtra(p *model.Problems, v any, ptr string) *jsonio.Object {
	o, ok := v.(*jsonio.Object)
	if !ok {
		p.Add(ptr, "expected a JSON object")
		return nil
	}
	if why := model.ExtraProblem(o); why != "" {
		p.AddAdditional(ptr, why)
		return nil
	}
	return o
}

// decodeJob is the optional job of spawn's and resume's input: "" when
// absent or invalid.
func decodeJob(f *model.Fields, p *model.Problems) string {
	v, ok := f.Optional("job")
	if !ok {
		return ""
	}
	ptr := f.Ptr("job")
	if s, ok := p.String(v, ptr); ok {
		if model.IsJob(s) {
			return s
		}
		p.Add(ptr, reasonJob)
	}
	return ""
}

// DecodeArgs is the optional args of spawn's and resume's input, which the
// pickers pass on too: strings with no NUL, in UTF-8; never nil.
func DecodeArgs(f *model.Fields, p *model.Problems) []string {
	args := []string{}
	if v, ok := f.Optional("args"); ok {
		ptr := f.Ptr("args")
		items, isArr := v.([]any)
		if !isArr {
			p.Add(ptr, "expected an array")
		}
		for i, item := range items {
			s, _ := checkedText(p, item, fmt.Sprintf("%s/%d", ptr, i))
			args = append(args, s)
		}
	}
	return args
}

// decodeStartTimeout is start_timeout_secs, 15 when absent: resume's.
func decodeStartTimeout(f *model.Fields, p *model.Problems) int64 {
	if n := optionalStartTimeout(f, p); n != nil {
		return *n
	}
	return defaultStartTimeout
}

// optionalStartTimeout is start_timeout_secs; nil when absent or invalid,
// for spawn's merge to default.
func optionalStartTimeout(f *model.Fields, p *model.Problems) *int64 {
	if v, ok := f.Optional("start_timeout_secs"); ok {
		if n, ok := p.Int(v, f.Ptr("start_timeout_secs"), 0, maxStartTimeout); ok {
			return &n
		}
	}
	return nil
}

// checkedText checks that v, at ptr, is a string with no NUL, in UTF-8.
func checkedText(p *model.Problems, v any, ptr string) (string, bool) {
	s, ok := p.String(v, ptr)
	switch {
	case !ok:
	case strings.ContainsRune(s, 0):
		p.AddAdditional(ptr, "must not contain a NUL")
	case !utf8.ValidString(s):
		p.AddAdditional(ptr, "not valid UTF-8")
	}
	return s, ok
}

// SpawnOutput is spawn's result (spawn-output), and resume's too
// (resume-output): the same fields.
type SpawnOutput struct {
	Job       *string        `json:"job"`
	Placement *jsonio.Object `json:"placement"`
	// Session is the started session (for resume, live again), nil when
	// start_timeout_secs was 0 or it did not start in time.
	Session *SessionView `json:"session"`
}

// SpawnEnv is what spawn reads and does outside: a ReadEnv, whose backend
// launches, plus the token and the wait's pause. Nothing in ops runs a
// process itself.
type SpawnEnv struct {
	ReadEnv
	// Token is a new reservation token: 32 lowercase hex characters.
	Token func() string
	// Sleep pauses between the wait's reads. Tests advance the fake clock.
	Sleep func(time.Duration)
	// CallerCwd is the caller's working directory (operations.md, Caller
	// context), the last place spawn's cwd can come from. The CLI sets it for
	// input built from flags; nil for -i, and in tests. It is called only if
	// nothing else gives cwd.
	CallerCwd func() (string, error)
}

// OSSpawnEnv is the real environment.
func OSSpawnEnv() SpawnEnv {
	return SpawnEnv{ReadEnv: OSReadEnv(), Token: randomToken, Sleep: time.Sleep}
}

// randomToken is 16 bytes from crypto/rand, as 32 lowercase hex characters.
func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on the platforms sesshin supports
	}
	return hex.EncodeToString(b[:])
}

// ShellArgv is the program the backend launches (operations.md, Launching
// claude): the shell, then -c and a fixed script, then claude's argument
// vector, so nothing sesshin or the caller supplies is ever parsed by a shell.
// A POSIX-family shell gets exec "$@" with sesshin filling $0; fish, judged by
// the base name of the shell's first word, gets exec $argv. The prompt, when
// there is one, follows args after --.
func ShellArgv(spawnShell, args []string, prompt string) []string {
	argv := append([]string{}, spawnShell...)
	if filepath.Base(spawnShell[0]) == "fish" {
		argv = append(argv, "-c", "exec $argv", "claude")
	} else {
		argv = append(argv, "-c", `exec "$@"`, "sesshin", "claude")
	}
	argv = append(argv, args...)
	if prompt != "" {
		argv = append(argv, "--", prompt)
	}
	return argv
}

// spawner is one run of spawn, over the merged input.
type spawner struct {
	launcher
	in templates.Merged
}

// Spawn launches claude in a new window of the caller's terminal, through a
// reservation made first that hands the session its job, if any, its extra,
// and its template, and waits for it to start (operations.md, spawn;
// design-spec.md, Reservations). The template is loaded and merged before any
// lock. It holds the state lock only to reserve and to record the window.
func spawnOp(input model.SpawnInput, env SpawnEnv) Envelope {
	l, cfg, e := loadSetup(env.ReadEnv)
	if e != nil {
		return Failed(e)
	}
	in, e := resolveSpawn(input, env, l)
	if e != nil {
		return Failed(e)
	}
	s := &spawner{in: in, launcher: launcher{env: env, l: l, cfg: cfg, job: in.Job, extra: in.Extra, template: in.Template}}
	if e := s.preflight(); e != nil {
		return Failed(e)
	}
	if e := s.reserve(); e != nil {
		return Failed(e)
	}
	return s.launchSpawn()
}

// resolveSpawn is spawn's step 1, with no lock held: the template, if the
// input names one, loaded whole and merged with the input, the built-in
// defaults applied, and the merged input checked (operations.md, Errors).
func resolveSpawn(input model.SpawnInput, env SpawnEnv, l loc.Locations) (templates.Merged, *Error) {
	var t *templates.Template
	if input.Template != "" {
		home := env.Getenv("HOME") // loc.Resolve has checked it
		dir := filepath.Join(l.ConfigDir, templates.Dir)
		var err error
		if t, err = templates.Load(env.FS, dir, home, input.Template); err != nil {
			return templates.Merged{}, templateError(err, dir, input.Template)
		}
	}
	m, err := templates.Merge(input, t, env.CallerCwd)
	var wd *templates.WorkingDirError
	switch {
	case errors.Is(err, templates.ErrNoCwd):
		return m, InvalidInput([]model.Problem{{Field: "/cwd", Reason: "required: give cwd, or a template that does, or run from flags in the directory to use"}})
	case errors.As(err, &wd):
		return m, IOError(".", wd.Err)
	}
	return m, nil
}

// templateError is a failure of templates.Load as spawn reports it: the
// template missing is not-found with templates, its prompt file missing is
// not-found with paths (the resolved one, since the input never named it).
func templateError(err error, dir, name string) *Error {
	var nf *templates.NotFound
	var ioe *templates.IOError
	var c *templates.Corrupt
	switch {
	case errors.As(err, &nf) && nf.Path == templates.Path(dir, name):
		return notFound("no template named "+name+" ("+nf.Path+" is not a file)", nil, nil, []string{name})
	case errors.As(err, &nf):
		return notFound("the prompt file "+nf.Path+" of template "+name+" is not an existing file", nil, []string{nf.Path}, nil)
	case errors.As(err, &ioe):
		return IOError(ioe.Path, ioe.Err)
	case errors.As(err, &c):
		return corrupt(c.Path, c.Detail)
	}
	return internal("loading template %s: %v", name, err)
}

// preflight is what spawn checks without a lock: cwd, the caller's terminal,
// and the job's reservation and its window.
func (s *spawner) preflight() *Error {
	if e := checkDir(s.env.FS, s.in.Cwd); e != nil {
		return e
	}
	if e := s.terminal(); e != nil {
		return e
	}
	if len(s.in.Vars) > 0 && !s.ln.UserVars() {
		return unsupported(s.b, "set user variables on a window")
	}
	return s.reserved()
}

// launchSpawn builds the launch and runs it.
func (s *spawner) launchSpawn() Envelope {
	in := s.in
	name := in.Name
	args := in.Args
	if name != "" {
		args = append([]string{"--name", name}, in.Args...)
	}
	return s.launch(launchPlan{
		spec: placement.LaunchSpec{
			Caller: s.caller,
			Type:   in.Type,
			Cwd:    in.Cwd,
			Title:  name,
			Vars:   in.Vars,
			Argv:   ShellArgv(s.cfg.SpawnShell(s.env.Getenv), args, in.Prompt),
		},
		timeoutSecs: in.StartTimeoutSecs,
		read:        s.read,
		notStarted:  "the session did not start within %d seconds; it may still start",
	})
}

// read is one read of spawn's wait, with no lock: with a job, a live session
// reporting it; without one, a session whose placement names the launched
// window.
func (s *spawner) read(launched *jsonio.Object) (*sessionSet, *sessionRec, *Error) {
	set, _, e := readSessions(s.env.ReadEnv)
	if e != nil {
		return nil, nil, e
	}
	for _, r := range set.recs {
		if s.started(r, launched) {
			return set, r, nil
		}
	}
	return set, nil, nil
}

// started reports whether the session is the one spawn launched.
func (s *spawner) started(r *sessionRec, launched *jsonio.Object) bool {
	if s.in.Job != "" {
		return r.res.State != live.Ended && r.job != nil && *r.job == s.in.Job
	}
	if r.Sesshin == nil {
		return false
	}
	return s.sameWindow(r.Sesshin.Placement, launched)
}

// sameWindow reports whether two placements address the same window, as the
// caller's backend sees them.
func (s *spawner) sameWindow(a, b *jsonio.Object) bool {
	if !s.b.Valid(a) || !s.b.Valid(b) {
		return false
	}
	x, errX := jsonio.MarshalLine(s.b.Address(a))
	y, errY := jsonio.MarshalLine(s.b.Address(b))
	return errX == nil && errY == nil && bytes.Equal(x, y)
}
