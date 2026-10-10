package proc

import (
	"os"

	"github.com/phansen314/sesshin/internal/fsys"
	"golang.org/x/sys/unix"
)

// Find looks up Claude from this process, a hook or the statusline that
// Claude started, given CLAUDE_PID from its environment ("" when unset).
// It reads only the process table, through sysctl, and takes no lock; fsy
// is unused here.
func Find(fsy fsys.FS, claudePID string) Claude {
	return find(sysctl(), int64(os.Getpid()), claudePID)
}

// FindCaller looks up the Claude this process, a command, runs under, given
// CLAUDE_PID from its environment ("" when unset), for the selector self:
// the nearest ancestor that is CLAUDE_PID or a claude by name.
func FindCaller(fsy fsys.FS, claudePID string) Claude {
	return findCaller(sysctl(), int64(os.Getpid()), claudePID)
}

// StartedAt returns process pid's pid_started_at, for comparing with a
// stored one. It fails with ErrNoProcess when there is no such process, and
// with another error when the check itself fails (design-spec.md, Liveness,
// Unknown).
func StartedAt(fsy fsys.FS, pid int64) (string, error) {
	return startedAt(sysctl(), pid)
}

// sysctl is the real process table, read through libc's sysctl as
// x/sys/unix calls it, without cgo.
func sysctl() sysctlTable {
	return sysctlTable{
		kinfo: func(pid int64) (kinfo, error) {
			k, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
			if err != nil {
				return kinfo{}, err
			}
			return kinfo{
				comm: k.Proc.P_comm[:],
				ppid: k.Eproc.Ppid,
				sec:  k.Proc.P_starttime.Sec,
				usec: k.Proc.P_starttime.Usec,
			}, nil
		},
		procargs: func(pid int64) ([]byte, error) {
			return unix.SysctlRaw("kern.procargs2", int(pid))
		},
		bootSession: func() (string, error) {
			return unix.Sysctl("kern.bootsessionuuid")
		},
	}
}

// ControllingTTY returns the device number of process pid's controlling
// terminal: kinfo_proc's e_tdev, which is the st_rdev of its tty file
// (design-spec.md, The iTerm2 backend). It fails with ErrNoProcess when there
// is no such process, and ErrNoTTY when it has none.
func ControllingTTY(pid int64) (uint64, error) {
	if pid <= 0 || pid > maxPID {
		return 0, ErrNoProcess
	}
	b, err := unix.SysctlRaw("kern.proc.pid", int(pid))
	if err != nil {
		return 0, kinfoErr(err)
	}
	return parseTdev(b)
}
