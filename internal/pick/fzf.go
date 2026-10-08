package pick

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/phansen314/sesshin/internal/ops"
)

// CLI-only error kinds (picker-spec.md, Errors). No operation raises them.
const (
	KindUnavailable = "unavailable"
	KindCancelled   = "cancelled"
)

// MinFzf is the oldest fzf the pickers run with (picker-spec.md,
// Requirements).
var MinFzf = version{0, 63, 0}

// System is what the pickers need from the process besides ops.SpawnEnv:
// finding and running programs. Tests replace it.
type System struct {
	LookPath func(file string) (string, error)
	// Output runs path with args and env (a list of KEY=value), and returns
	// its stdout, its stderr, and its exit status; err only when it could
	// not be started or did not exit normally.
	Output  func(path string, args, env []string) (stdout, stderr []byte, status int, err error)
	Environ func() []string
	// OpenTTY checks that /dev/tty opens for reading and writing.
	OpenTTY func() error
	// RunFzf runs the picker: path with args and env, stdin from stdin, and
	// stderr to this process's, where fzf reports its own problems. It
	// returns fzf's stdout, which is the selection, and its exit status; err
	// only when fzf could not be started or did not exit normally.
	RunFzf func(path string, args, env []string, stdin []byte) (stdout []byte, status int, err error)
	// CatchInterrupts catches SIGINT and SIGQUIT and discards them, until
	// the function it returns restores default handling (picker-spec.md,
	// Errors).
	CatchInterrupts func() (restore func())
	// ShowFailure writes msg as one line to /dev/tty and waits for one key,
	// read in raw mode (jump's step 6). It returns an error when /dev/tty
	// does not open or cannot be read, in which case nothing is waited for.
	ShowFailure func(msg string) error
}

// OSSystem is the process's own.
func OSSystem() System {
	return System{
		LookPath:        exec.LookPath,
		Output:          output,
		Environ:         os.Environ,
		OpenTTY:         openTTY,
		RunFzf:          runFzf,
		CatchInterrupts: catchInterrupts,
		ShowFailure:     showFailure,
	}
}

// catchInterrupts catches the signals rather than ignoring them: an ignored
// signal stays ignored in every program fzf starts (the preview), while a
// caught one is reset to default by exec. Nothing reads the channel: it
// holds one signal and drops the rest, so every one is discarded, with no
// goroutine (the guard tests forbid go statements in sesshin's code).
func catchInterrupts() func() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGQUIT)
	return func() { signal.Stop(c) }
}

func openTTY() error {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

func runFzf(path string, args, env []string, stdin []byte) ([]byte, int, error) {
	return run(path, args, env, stdin, os.Stderr)
}

func output(path string, args, env []string) ([]byte, []byte, int, error) {
	var errOut bytes.Buffer
	out, status, err := run(path, args, env, nil, &errOut)
	return out, errOut.Bytes(), status, err
}

// run runs path with args and env, stdin when there is any, and stderr to
// stderr. It returns the stdout and the exit status; err only when the
// program could not be started or did not exit normally.
func run(path string, args, env []string, stdin []byte, stderr io.Writer) ([]byte, int, error) {
	var out bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, &out, stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return out.Bytes(), exit.ExitCode(), nil
	}
	return out.Bytes(), 0, err
}

// The reasons of unavailable's details (picker-spec.md, Errors).
const (
	noTerminal = "no-terminal"
	fzfMissing = "fzf-missing"
	fzfTooOld  = "fzf-too-old"
	fzfFailed  = "fzf-failed"
)

// unavailableErr is the error of a picker that cannot run; details are reason
// and what goes with it, as key and value pairs.
func unavailableErr(msg, reason string, extra ...any) *ops.Error {
	details := map[string]any{"reason": reason}
	for i := 0; i+1 < len(extra); i += 2 {
		details[extra[i].(string)] = extra[i+1]
	}
	return &ops.Error{Kind: KindUnavailable, Message: msg, Details: details}
}

// findFzf returns the path of an fzf on PATH whose version is MinFzf or
// later, or unavailable. The version is asked for without the person's
// FZF_DEFAULT_OPTS and FZF_DEFAULT_OPTS_FILE, since a bad option in either
// makes fzf --version fail: a bad option is reported where fzf really
// starts, as fzf-failed.
func findFzf(sys System, picker string) (string, *ops.Error) {
	need := MinFzf.String()
	path, err := sys.LookPath("fzf")
	if err != nil {
		return "", unavailableErr("fzf not found on PATH: "+picker+" needs fzf "+need+" or later", fzfMissing)
	}
	out, errOut, status, err := sys.Output(path, []string{"--version"}, withoutOpts(sys.Environ()))
	switch {
	case err != nil:
		return "", unavailableErr(fmt.Sprintf("fzf --version failed: %v", err), fzfFailed)
	case status != 0:
		msg := fmt.Sprintf("fzf --version exited with status %d", status)
		if line := firstLine(errOut); line != "" {
			msg += ": " + line
		}
		return "", unavailableErr(msg, fzfFailed, "status", status)
	}
	found, v, ok := parseVersion(out)
	switch {
	case !ok:
		return "", unavailableErr(fmt.Sprintf("fzf --version printed %q, not a version: %s needs fzf %s or later", found, picker, need), fzfTooOld, "found", found, "required", need)
	case v.less(MinFzf):
		return "", unavailableErr(fmt.Sprintf("fzf %s is too old: %s needs fzf %s or later", found, picker, need), fzfTooOld, "found", found, "required", need)
	}
	return path, nil
}

// withoutOpts is env without FZF_DEFAULT_OPTS and FZF_DEFAULT_OPTS_FILE.
// It is never nil, which exec would take as the whole of this process's
// environment, options included.
func withoutOpts(env []string) []string {
	out := []string{}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "FZF_DEFAULT_OPTS=") && !strings.HasPrefix(kv, "FZF_DEFAULT_OPTS_FILE=") {
			out = append(out, kv)
		}
	}
	return out
}

type version [3]int

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func (v version) less(w version) bool {
	for i := range v {
		if v[i] != w[i] {
			return v[i] < w[i]
		}
	}
	return false
}

// parseVersion reads fzf --version's output: the first whitespace-separated
// word, without any suffix from the first "-" ("0.75.0-dev" is 0.75.0), as
// three numbers. found is that word, or, when it doesn't parse, the first
// line as printed.
func parseVersion(out []byte) (found string, v version, ok bool) {
	words := strings.Fields(string(out))
	if len(words) == 0 {
		return firstLine(out), v, false
	}
	core, _, _ := strings.Cut(words[0], "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return firstLine(out), v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || p[0] == '+' {
			return firstLine(out), v, false
		}
		v[i] = n
	}
	return words[0], v, true
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSuffix(line, "\r")
}
