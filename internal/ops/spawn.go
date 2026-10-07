package ops

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// The spawn types (spawn-input).
var spawnTypes = []string{typeTab, "split", "os-window"}

const (
	typeTab             = "tab"
	defaultSpawnType    = typeTab
	defaultStartTimeout = 15
	maxStartTimeout     = 120
	reasonJob           = "must match ^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$ and not be all digits"
)

// SpawnInput is spawn's input (spawn-input), with its defaults filled in.
type SpawnInput struct {
	// Job is the job to reserve; "" for none.
	Job string
	Cwd string
	// Type is tab, split, or os-window.
	Type string
	// Name is the session's name, for claude --name and the tab title; ""
	// for the job, else neither.
	Name string
	// Prompt is the first prompt; "" for none.
	Prompt string
	Args   []string
	// Vars are the new window's user variables, in the order given.
	Vars []kitty.Var
	// Extra is the session's user-owned extra, passed as SESSHIN_EXTRA; nil
	// for none.
	Extra            *jsonio.Object
	StartTimeoutSecs int64
}

// DecodeSpawnInput is spawn's own checks: the schema's, then its Additional
// validation: cwd absolute, no NUL in any string (which no argument vector
// can carry), each vars key a name. Every string must be UTF-8 too, which
// --input's reader has already ensured and the command line does not.
func DecodeSpawnInput(f *model.Fields, p *model.Problems) SpawnInput {
	in := SpawnInput{Type: defaultSpawnType, Args: []string{}, StartTimeoutSecs: defaultStartTimeout}
	in.Job = decodeJob(f, p)
	if v, ok := f.Required("cwd"); ok {
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
		in.Prompt, _ = checkedText(p, v, f.Ptr("prompt"))
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
				if !validVarKey(m.Key) {
					p.AddAdditional(mptr, "must match ^[A-Za-z_][A-Za-z0-9_]{0,63}$")
				}
				if s, ok := checkedText(p, m.Value, mptr); ok {
					in.Vars = append(in.Vars, kitty.Var{Name: m.Key, Value: s})
				}
			}
		}
	}
	if v, ok := f.Optional("extra"); ok {
		in.Extra = decodeExtra(p, v, f.Ptr("extra"))
	}
	in.StartTimeoutSecs = decodeStartTimeout(f, p)
	return in
}

// decodeExtra checks that v, at ptr, is an object within extra's limits
// (design-spec.md, User-owned extra), its numbers kept as given, and holds no
// NUL, which SESSHIN_EXTRA can't carry. It returns nil on a problem.
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
	if at, found := nulIn(o, ptr); found {
		p.AddAdditional(at, "must not contain a NUL")
		return nil
	}
	return o
}

// nulIn returns the pointer of the first key or string in v, a tree at ptr,
// that holds a NUL.
func nulIn(v any, ptr string) (string, bool) {
	switch v := v.(type) {
	case string:
		return ptr, strings.ContainsRune(v, 0)
	case []any:
		for i, item := range v {
			if at, ok := nulIn(item, fmt.Sprintf("%s/%d", ptr, i)); ok {
				return at, true
			}
		}
	case *jsonio.Object:
		for _, m := range v.Members {
			at := jsonio.Pointer(ptr, m.Key)
			if strings.ContainsRune(m.Key, 0) {
				return at, true
			}
			if at, ok := nulIn(m.Value, at); ok {
				return at, true
			}
		}
	}
	return "", false
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

// decodeStartTimeout is start_timeout_secs, 15 when absent.
func decodeStartTimeout(f *model.Fields, p *model.Problems) int64 {
	n := int64(defaultStartTimeout)
	if v, ok := f.Optional("start_timeout_secs"); ok {
		if m, ok := p.Int(v, f.Ptr("start_timeout_secs"), 0, maxStartTimeout); ok {
			n = m
		}
	}
	return n
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

// validVarKey is vars' key rule: ^[A-Za-z_][A-Za-z0-9_]{0,63}$.
func validVarKey(k string) bool {
	if k == "" || len(k) > 64 {
		return false
	}
	for i := range len(k) {
		c := k[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
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

// SpawnEnv is what spawn reads and does outside: a ReadEnv, plus the
// backend's launch, the token, and the wait's pause. Nothing in ops runs a
// process itself.
type SpawnEnv struct {
	ReadEnv
	// Launch opens the window and returns its ID; its error is a
	// *kitty.LaunchError, which says whether the outcome is unknown.
	Launch func(kitty.LaunchSpec) (int64, error)
	// Token is a new reservation token: 32 lowercase hex characters.
	Token func() string
	// Sleep pauses between the wait's reads. Tests advance the fake clock.
	Sleep func(time.Duration)
}

// OSSpawnEnv is the real environment.
func OSSpawnEnv() SpawnEnv {
	return SpawnEnv{ReadEnv: OSReadEnv(), Launch: kitty.Launch, Token: randomToken, Sleep: time.Sleep}
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

// spawner is one run of spawn.
type spawner struct {
	launcher
	in SpawnInput
}

// Spawn launches claude in a new window of the caller's terminal, under a
// job reserved first when there is one, and waits for it to start
// (operations.md, spawn; design-spec.md, Reservations). It holds the state
// lock only to claim the job and to record the window.
func Spawn(in SpawnInput, env SpawnEnv) Envelope {
	l, cfg, e := loadSetup(env.ReadEnv)
	if e != nil {
		return Failed(e)
	}
	s := &spawner{in: in, launcher: launcher{env: env, l: l, cfg: cfg, job: in.Job}}
	if in.Extra != nil {
		b, err := jsonio.MarshalLine(in.Extra)
		if err != nil {
			return Failed(&Error{Kind: KindInternal, Message: "encode extra: " + err.Error()})
		}
		s.extra = strings.TrimSuffix(string(b), "\n")
	}
	if e := s.preflight(); e != nil {
		return Failed(e)
	}
	if e := s.claimJob(); e != nil {
		return Failed(e)
	}
	return s.launchSpawn()
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
	return s.reserved()
}

// launchSpawn builds the launch and runs it.
func (s *spawner) launchSpawn() Envelope {
	in := s.in
	name := cmp.Or(in.Name, in.Job)
	args := in.Args
	if name != "" {
		args = append([]string{"--name", name}, in.Args...)
	}
	return s.launch(launchPlan{
		spec: kitty.LaunchSpec{
			Socket: s.sock.Socket,
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
func (s *spawner) read(window int64) (*sessionSet, *sessionRec, *Error) {
	set, _, e := readSessions(s.env.ReadEnv)
	if e != nil {
		return nil, nil, e
	}
	for _, r := range set.recs {
		if s.started(r, window) {
			return set, r, nil
		}
	}
	return set, nil, nil
}

// started reports whether the session is the one spawn launched.
func (s *spawner) started(r *sessionRec, window int64) bool {
	if s.in.Job != "" {
		return r.res.State != live.Ended && r.job != nil && *r.job == s.in.Job
	}
	if r.Sesshin == nil {
		return false
	}
	pl, ok := kitty.Parse(r.Sesshin.Placement)
	return ok && pl.Socket == s.sock.Socket && pl.WindowID == window
}
