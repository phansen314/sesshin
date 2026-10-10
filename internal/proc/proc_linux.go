package proc

import (
	"os"

	"github.com/phansen314/sesshin/internal/fsys"
)

// Find looks up Claude from this process, a hook or the statusline that
// Claude started, given CLAUDE_PID from its environment ("" when unset).
// It reads only the process table, and takes no lock.
func Find(fsy fsys.FS, claudePID string) Claude {
	return procfs{fsy, "/proc"}.find(int64(os.Getpid()), claudePID)
}

// FindCaller looks up the Claude this process, a command, runs under, given
// CLAUDE_PID from its environment ("" when unset), for the selector self:
// the nearest ancestor that is CLAUDE_PID or a claude by name.
func FindCaller(fsy fsys.FS, claudePID string) Claude {
	return procfs{fsy, "/proc"}.findCaller(int64(os.Getpid()), claudePID)
}

// StartedAt returns process pid's pid_started_at, for comparing with a
// stored one. It fails with ErrNoProcess when there is no such process, and
// with another error when the check itself fails (design-spec.md, Liveness,
// Unknown).
func StartedAt(fsy fsys.FS, pid int64) (string, error) {
	return procfs{fsy, "/proc"}.startedAt(pid)
}

// ControllingTTY finds nothing on Linux, where no backend asks.
func ControllingTTY(pid int64) (uint64, error) { return 0, errUnsupported }
