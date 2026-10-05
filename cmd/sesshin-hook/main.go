// Command sesshin-hook is Claude Code's entry point into sesshin: sesshin-hook <verb>,
// with the event's payload on stdin. It always exits 0 and is silent
// (hooks-spec.md, The contract); all behavior is in internal/hook.
//
// It links the standard library and sesshin's own internal packages only
// (implementation-spec.md, The hook binary), which guard_test.go checks.
package main

import (
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/hook"
	"github.com/phansen314/sesshin/internal/statusline"
	"github.com/phansen314/sesshin/internal/testhook"
)

func main() {
	// H1, H2: recover before anything else, and return normally, so a panic
	// still exits 0. A panic that reaches here never entered the statusline's
	// own frame, so its line hasn't been printed: print the fallback line, as
	// the statusline does on every path (hooks-spec.md, Rendering).
	defer func() {
		if recover() != nil && len(os.Args) > 1 && os.Args[1] == "statusline" {
			_, _ = os.Stdout.WriteString(statusline.FallbackLine)
		}
	}()
	// A write to a stdout Claude has closed returns EPIPE instead of killing
	// the process with exit 141.
	signal.Ignore(syscall.SIGPIPE)
	// A fatal runtime error can't be recovered, and by default exits 2, the
	// blocking code; crash ends it by SIGABRT (exit 134) instead, with no core.
	debug.SetTraceback("crash")
	_ = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{})
	// H3: nothing reaches Claude on stderr, whatever writes to it.
	silenceStderr()
	testhook.At("start")
	hook.Run(os.Args[1:], hook.Process{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		FS:     fsys.OS{},
		Getenv: os.Getenv,
		GOOS:   runtime.GOOS,
		Now:    time.Now,
	})
}
