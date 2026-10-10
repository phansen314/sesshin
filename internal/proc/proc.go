package proc

import (
	"bytes"
	"path"
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/fsys"
)

// Claude is what a lookup found. PID is 0 when it found no Claude, and then
// StartedAt is "" and Nested nil: pid_started_at is null exactly when pid
// is (design-spec.md, lifecycle.json).
type Claude struct {
	PID int64
	// StartedAt is pid_started_at: linux:<boot_id>:<starttime> or
	// darwin:<boot session>:<sec>.<usec>, compared only for equality.
	StartedAt string
	// Nested is whether another session started this Claude, nil when that
	// couldn't be told.
	Nested *bool
}

// maxSteps bounds a walk up the ancestry: the table is read while it
// changes, and a cycle in what was read must not hang a blocking hook.
const maxSteps = 256

// procTable is a process table: the one part of a lookup that differs by
// system. procfs reads Linux's /proc, sysctlTable macOS's sysctl.
type procTable interface {
	// stat reads the process's name, parent, and start time.
	stat(pid int64) (stat, error)
	// gone reports whether an error from stat says there is no such
	// process, rather than that the read failed.
	gone(err error) bool
	// claudeExe reports whether the process's executable makes it a claude
	// by the walk's name rule, whatever its name.
	claudeExe(pid int64) bool
	// environ reads the process's initial environment, NUL-separated.
	environ(pid int64) ([]byte, error)
	// bootID reads the ID of the boot, which changes at each one.
	bootID() (string, error)
	// system is pid_started_at's prefix.
	system() string
}

// stat is what a lookup reads of one process.
type stat struct {
	comm string
	ppid int64
	// starttime is the start time as pid_started_at holds it, after the
	// boot.
	starttime string
}

// find looks up Claude from process self, which has CLAUDE_PID claudePID
// in its environment ("" when unset). CLAUDE_PID is taken when it names
// self's parent or its parent's parent: the shell Claude runs the command
// through may exec it, or not. A further ancestor's is inherited from an
// outer session. Otherwise the first claude up the ancestry, from self's
// parent, is Claude.
func find(t procTable, self int64, claudePID string) Claude {
	st, err := t.stat(self)
	if err != nil {
		return Claude{}
	}
	var pid int64
	if n, ok := parsePID(claudePID); ok {
		if n == st.ppid {
			pid = n
		} else if pst, err := t.stat(st.ppid); err == nil && pst.ppid == n {
			pid = n
		}
	}
	if pid == 0 {
		if pid, _ = walk(t, st.ppid); pid == 0 {
			return Claude{}
		}
	}
	return claudeAt(t, pid)
}

// findCaller looks up the Claude a command runs under, for the selector self
// (operations.md, Selecting a session): the nearest ancestor of process self
// that is CLAUDE_PID claudePID or a claude by the walk's name rule. Unlike
// find, CLAUDE_PID may name any ancestor, since a command in a pipeline or
// under xargs sits deeper than a hook; a claude below it is nearer, and wins.
func findCaller(t procTable, self int64, claudePID string) Claude {
	st, err := t.stat(self)
	if err != nil {
		return Claude{}
	}
	want, _ := parsePID(claudePID)
	pid := st.ppid
	for range maxSteps {
		if pid <= 1 {
			return Claude{}
		}
		pst, err := t.stat(pid)
		if err != nil {
			return Claude{}
		}
		if pid == want || isClaude(t, pid, pst.comm) {
			return claudeAt(t, pid)
		}
		pid = pst.ppid
	}
	return Claude{}
}

// claudeAt is what a lookup that settled on pid found: its start time and
// whether another session started it. No start time is no Claude.
func claudeAt(t procTable, pid int64) Claude {
	cst, err := t.stat(pid)
	if err != nil {
		return Claude{}
	}
	boot, err := t.bootID()
	if err != nil {
		return Claude{}
	}
	return Claude{
		PID:       pid,
		StartedAt: t.system() + ":" + boot + ":" + cst.starttime,
		Nested:    nested(t, pid, cst.ppid),
	}
}

// startedAt is the process's pid_started_at. No such process is
// ErrNoProcess; any other error is the check failing, a missing boot ID
// included, which a reader must not take for the process being gone.
func startedAt(t procTable, pid int64) (string, error) {
	st, err := t.stat(pid)
	if err != nil {
		if t.gone(err) {
			return "", ErrNoProcess
		}
		return "", err
	}
	boot, err := t.bootID()
	if err != nil {
		return "", err
	}
	return t.system() + ":" + boot + ":" + st.starttime, nil
}

// walk returns the first claude at or above pid, or 0. ok is false when it
// stopped on a read that failed, rather than at pid 1 or the step bound,
// so no answer can be drawn from its 0.
func walk(t procTable, pid int64) (claude int64, ok bool) {
	for range maxSteps {
		if pid <= 1 {
			return 0, true
		}
		st, err := t.stat(pid)
		if err != nil {
			return 0, false
		}
		if isClaude(t, pid, st.comm) {
			return pid, true
		}
		pid = st.ppid
	}
	return 0, false
}

// isClaude reports whether the process is Claude by the walk's name rule
// (design-spec.md, Liveness): its name is claude, or its executable says so
// (claudeExe), as when an IDE or the updater starts a versioned binary by
// its path and it is named by its version. Never node: an npm or Agent SDK
// launch is found by CLAUDE_PID alone.
func isClaude(t procTable, pid int64, comm string) bool {
	return comm == "claude" || t.claudeExe(pid)
}

// versioned reports whether exe is <…>/claude/versions/<version>. The
// Linux kernel marks an executable replaced since it started, as Claude
// Code's updater replaces them, with " (deleted)".
func versioned(exe string) bool {
	exe = strings.TrimSuffix(exe, " (deleted)")
	dir, v := path.Split(exe)
	dir = strings.TrimSuffix(dir, "/")
	return path.Base(dir) == "versions" && path.Base(path.Dir(dir)) == "claude" &&
		v != "" && v[0] >= '0' && v[0] <= '9'
}

// nested reports whether Claude, process pid with parent ppid, was started
// by another session: CLAUDECODE in its initial environment, which Claude
// sets for everything it starts. When the environment can't be read,
// another claude above it in the ancestry says so; nil when neither can
// tell.
func nested(t procTable, pid, ppid int64) *bool {
	var yes bool
	if env, err := t.environ(pid); err == nil {
		yes = hasVar(env, "CLAUDECODE")
		return &yes
	}
	outer, ok := walk(t, ppid)
	if !ok {
		return nil
	}
	yes = outer != 0
	return &yes
}

// hasVar reports whether NUL-separated environ sets name, to any value.
func hasVar(environ []byte, name string) bool {
	for len(environ) > 0 {
		var kv []byte
		kv, environ, _ = bytes.Cut(environ, []byte{0})
		if k, _, ok := bytes.Cut(kv, []byte("=")); ok && string(k) == name {
			return true
		}
	}
	return false
}

// isBootID reports whether s is a UUID as the Linux kernel writes it, and
// as macOS's is once lowercased: 36 characters, lowercase hex and hyphens.
func isBootID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if c != '-' && !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// parsePID reads a pid from the environment: decimal digits naming a
// process other than init.
func parsePID(s string) (int64, bool) {
	if !digits(s) || len(s) > 10 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 1
}

// digits reports whether s is one or more ASCII digits.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// constError is an error that can be a constant: sesshin-hook's packages have
// no package-level initializer that does work (implementation-spec.md, The
// hook binary).
type constError string

func (e constError) Error() string { return string(e) }

const (
	// ErrNoProcess is StartedAt's error when there is no such process.
	ErrNoProcess   constError = "no such process"
	errMalformed   constError = "malformed process table entry"
	errUnsupported constError = "no process table on this system"
	// ErrNoTTY is ControllingTTY's error when the process has no controlling
	// terminal.
	ErrNoTTY constError = "no controlling terminal"
)

// FindWith looks up Claude's process with lookup, or with Find when lookup is
// nil, the seam tests use (hooks-spec.md, Late adoption; statusline step 2).
func FindWith(lookup func(fsys.FS, string) Claude, files fsys.FS, pid string) Claude {
	if lookup == nil {
		lookup = Find
	}
	return lookup(files, pid)
}
