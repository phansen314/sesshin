package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/phansen314/sesshin/internal/placement/iterm2"
)

// LaunchEnv is what launch-exec reads and does outside, nil in Env being the
// running process's. Tests replace the exec.
type LaunchEnv struct {
	Store iterm2.Store
	// Environ is the environment to set variables on top of; nil is the
	// process's.
	Environ func() []string
	// Chdir changes directory; nil is os.Chdir.
	Chdir func(dir string) error
	// Exec replaces the process; nil is syscall.Exec. A real one returns
	// only on failure.
	Exec func(path string, argv, env []string) error
	// Interactive says whether stdin is a terminal; nil asks the system.
	Interactive func() bool
}

func (e Env) launch() LaunchEnv {
	if e.Launch != nil {
		return *e.Launch
	}
	return LaunchEnv{}
}

// launchExecCommand is `sesshin launch-exec <nonce>` (operations.md, The
// iTerm2 launch): hidden, outside the operation tables, printing no envelope.
// iTerm2 runs it in the new session; it reads the launch file the launching
// sesshin wrote, and replaces itself with the program. code is where its exit
// code goes.
func launchExecCommand(env Env, code **int) *cobra.Command {
	return &cobra.Command{
		Use:    "launch-exec <nonce>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := runLaunchExec(args[0], env)
			*code = &c
			return nil
		},
	}
}

// runLaunchExec does the launch and returns the exit code of a launch that
// did not replace the process: nonzero for a refusal or a failure, which it
// prints, waiting for Enter first when stdin is a terminal, since iTerm2 may
// close a session whose command has ended.
func runLaunchExec(nonce string, env Env) int {
	le := env.launch()
	if err := le.exec(nonce); err != nil {
		fmt.Fprintf(env.Stderr, "sesshin launch-exec: %v\n", err)
		if le.interactive() {
			fmt.Fprint(env.Stderr, "Press Enter to close this session. ")
			if env.Stdin != nil {
				_, _ = bufio.NewReader(env.Stdin).ReadString('\n')
			}
		}
		return ExitError
	}
	return ExitOK // an exec that returned: a test's
}

func (le LaunchEnv) interactive() bool {
	if le.Interactive != nil {
		return le.Interactive()
	}
	_, err := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ)
	return err == nil
}

// exec reads the launch file, changes to its directory, sets its variables on
// top of the environment, and replaces the process with its program.
func (le LaunchEnv) exec(nonce string) error {
	f, err := le.Store.Take(nonce)
	if err != nil {
		return err
	}
	chdir := le.Chdir
	if chdir == nil {
		chdir = os.Chdir
	}
	if err := chdir(f.Cwd); err != nil {
		return fmt.Errorf("changing to %s: %v", f.Cwd, err)
	}
	environ := os.Environ
	if le.Environ != nil {
		environ = le.Environ
	}
	env := withVars(environ(), f.Env)
	path := f.Argv[0]
	if !strings.Contains(path, "/") {
		if path, err = exec.LookPath(path); err != nil {
			return err
		}
	}
	run := le.Exec
	if run == nil {
		run = syscall.Exec
	}
	if err := run(path, f.Argv, env); err != nil {
		return fmt.Errorf("running %s: %v", f.Argv[0], err)
	}
	return nil
}

// withVars is environ with each variable of vars set, replacing the ones of
// its name, in name order.
func withVars(environ []string, vars map[string]string) []string {
	out := make([]string, 0, len(environ)+len(vars))
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); !hasKey(vars, name) {
			out = append(out, kv)
		}
	}
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		out = append(out, k+"="+vars[k])
	}
	return out
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }
