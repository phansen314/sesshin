// Command sesshin is the sesshin CLI. main sets up the process and exits; all
// behavior is in internal/cli (cli-spec.md).
package main

import (
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/cli"
)

func main() {
	os.Exit(run())
}

// run is everything but the exit, so deferred cleanup always runs first.
func run() int {
	// A panic or fatal error ends by SIGABRT (exit 134, outcome unknown),
	// not Go's default exit 2, which would read as a usage error.
	debug.SetTraceback("crash")
	// The traceback on stderr is what a bug report needs; no core is saved
	// (implementation-spec.md, The CLI).
	_ = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{})
	// Writing to a closed pipe then returns EPIPE, reported as exit 3,
	// instead of killing the process (cli-spec.md, Exit codes).
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	return cli.Run(os.Args[1:], cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, BuildInfo: buildinfo.Read})
}
