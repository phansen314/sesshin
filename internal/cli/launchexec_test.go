package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/placement/iterm2"
)

const launchNonce = "0123456789abcdef0123456789abcdef"

var launchNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// launchRun is one launch-exec, with the exec replaced by a record.
type launchRun struct {
	code       int
	stdout     string
	stderr     string
	chdir      string
	execPath   string
	execArgv   []string
	execEnv    []string
	stdinLeft  string
	execCalled bool
}

// runLaunch writes a launch file (content "" for a valid one) and runs
// launch-exec on nonce; interactive says whether stdin is a terminal.
func runLaunch(t *testing.T, content, nonce string, interactive bool, tweak func(*LaunchEnv)) launchRun {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "launches")
	store := iterm2.Store{Dir: dir, Now: func() time.Time { return launchNow }, Nonce: func() string { return launchNonce }}
	if content == "" {
		if _, err := store.Write(iterm2.LaunchFile{Cwd: "/work", Env: map[string]string{"SESSHIN_TOKEN": "tok", "PATH": "/new"}, Argv: []string{"/bin/zsh", "-c", "x y"}}); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, launchNonce+".json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var r launchRun
	le := LaunchEnv{
		Store:   store,
		Environ: func() []string { return []string{"HOME=/h", "PATH=/old", "TERM=x"} },
		Chdir:   func(d string) error { r.chdir = d; return nil },
		Exec: func(p string, a, e []string) error {
			r.execCalled, r.execPath, r.execArgv, r.execEnv = true, p, a, e
			return nil
		},
		Interactive: func() bool { return interactive },
	}
	if tweak != nil {
		tweak(&le)
	}
	var out, errOut bytes.Buffer
	stdin := strings.NewReader("\nrest")
	env := Env{Stdin: stdin, Stdout: &out, Stderr: &errOut, Launch: &le}
	r.code = Run([]string{"launch-exec", nonce}, env)
	r.stdout, r.stderr = out.String(), errOut.String()
	rest := make([]byte, 10)
	n, _ := stdin.Read(rest)
	r.stdinLeft = string(rest[:n])
	return r
}

func TestLaunchExec(t *testing.T) {
	r := runLaunch(t, "", launchNonce, true, nil)
	if r.code != 0 || !r.execCalled || r.chdir != "/work" || r.execPath != "/bin/zsh" || !slices.Equal(r.execArgv, []string{"/bin/zsh", "-c", "x y"}) {
		t.Fatalf("%+v", r)
	}
	// The variables go on top of the process's own environment.
	if want := []string{"HOME=/h", "TERM=x", "PATH=/new", "SESSHIN_TOKEN=tok"}; !slices.Equal(r.execEnv, want) {
		t.Errorf("env %v, want %v", r.execEnv, want)
	}
	if r.stdout != "" || r.stderr != "" || r.stdinLeft != "\nrest" {
		t.Errorf("output %q, %q; stdin read: %q", r.stdout, r.stderr, r.stdinLeft)
	}
}

func TestLaunchExecRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		nonce   string
		tweak   func(*LaunchEnv)
		want    string
	}{
		"bad nonce":    {"", "xyz", nil, "32 lowercase"},
		"no file":      {"", strings.Repeat("a", 32), nil, "missing"},
		"not a launch": {`{"hello":1}`, launchNonce, nil, "not a launch file"},
		"no directory": {"", launchNonce, func(le *LaunchEnv) { le.Chdir = func(string) error { return errors.New("no such directory") } }, "changing to /work"},
		"exec fails": {"", launchNonce, func(le *LaunchEnv) {
			le.Exec = func(string, []string, []string) error { return errors.New("exec format error") }
		}, "running /bin/zsh"},
	} {
		for _, interactive := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				r := runLaunch(t, tc.content, tc.nonce, interactive, tc.tweak)
				if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, tc.want) {
					t.Fatalf("%+v", r)
				}
				if r.execCalled && name != "exec fails" {
					t.Error("exec ran")
				}
				// It waits for Enter only on a terminal.
				if waited := r.stdinLeft != "\nrest"; waited != interactive {
					t.Errorf("interactive %v, stdin left %q", interactive, r.stdinLeft)
				}
				if strings.Contains(r.stderr, "Press Enter") != interactive {
					t.Errorf("prompt %q", r.stderr)
				}
			})
		}
	}
}

// The command is hidden: help lists it nowhere.
func TestLaunchExecHidden(t *testing.T) {
	out, _, _ := execute(commands, []string{"--help"}, Env{Stdout: &bytes.Buffer{}})
	if strings.Contains(string(out), "launch-exec") {
		t.Errorf("help lists launch-exec:\n%s", out)
	}
}

func TestWithVars(t *testing.T) {
	got := withVars([]string{"A=1", "B=2", "NOEQ"}, map[string]string{"B": "x", "C": "y=z"})
	if want := []string{"A=1", "NOEQ", "B=x", "C=y=z"}; !slices.Equal(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
}
