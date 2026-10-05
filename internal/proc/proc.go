package proc

import (
	"bytes"
	"path"
	"strconv"
	"strings"
	"syscall"

	"github.com/phansen314/sesshin/internal/fsys"
)

// Claude is what a lookup found. PID is 0 when it found no Claude, and then
// StartedAt is "" and Nested nil: pid_started_at is null exactly when pid
// is (design-spec.md, lifecycle.json).
type Claude struct {
	PID int64
	// StartedAt is pid_started_at: linux:<boot_id>:<starttime>, compared
	// only for equality.
	StartedAt string
	// Nested is whether another session started this Claude, nil when that
	// couldn't be told.
	Nested *bool
}

// maxSteps bounds a walk up the ancestry: the table is read while it
// changes, and a cycle in what was read must not hang a blocking hook.
const maxSteps = 256

// procfs is a process table in Linux's /proc layout, under root.
type procfs struct {
	fs   fsys.FS
	root string
}

// find looks up Claude from process self, which has CLAUDE_PID claudePID
// in its environment ("" when unset). CLAUDE_PID is taken when it names
// self's parent or its parent's parent: the shell Claude runs the command
// through may exec it, or not. A further ancestor's is inherited from an
// outer session. Otherwise the first claude up the ancestry, from self's
// parent, is Claude.
func (p procfs) find(self int64, claudePID string) Claude {
	st, err := p.stat(self)
	if err != nil {
		return Claude{}
	}
	var pid int64
	if n, ok := parsePID(claudePID); ok {
		if n == st.ppid {
			pid = n
		} else if pst, err := p.stat(st.ppid); err == nil && pst.ppid == n {
			pid = n
		}
	}
	if pid == 0 {
		if pid, _ = p.walk(st.ppid); pid == 0 {
			return Claude{}
		}
	}
	cst, err := p.stat(pid)
	if err != nil {
		return Claude{}
	}
	boot, err := p.bootID()
	if err != nil {
		return Claude{}
	}
	return Claude{
		PID:       pid,
		StartedAt: startedAt(boot, cst.starttime),
		Nested:    p.nested(pid, cst.ppid),
	}
}

// startedAt is the process's pid_started_at. Its stat missing (ENOENT) or
// vanishing as it is read (ESRCH) is ErrNoProcess; any other error is the
// check failing, a missing boot ID included, which a reader must not take
// for the process being gone.
func (p procfs) startedAt(pid int64) (string, error) {
	st, err := p.stat(pid)
	if err != nil {
		if e, ok := fsys.ErrnoOf(err); ok && (e == syscall.ENOENT || e == syscall.ESRCH) {
			return "", ErrNoProcess
		}
		return "", err
	}
	boot, err := p.bootID()
	if err != nil {
		return "", err
	}
	return startedAt(boot, st.starttime), nil
}

func startedAt(boot, starttime string) string {
	return "linux:" + boot + ":" + starttime
}

// walk returns the first claude at or above pid, or 0. ok is false when it
// stopped on a read that failed, rather than at pid 1 or the step bound,
// so no answer can be drawn from its 0.
func (p procfs) walk(pid int64) (claude int64, ok bool) {
	for range maxSteps {
		if pid <= 1 {
			return 0, true
		}
		st, err := p.stat(pid)
		if err != nil {
			return 0, false
		}
		if p.isClaude(pid, st.comm) {
			return pid, true
		}
		pid = st.ppid
	}
	return 0, false
}

// isClaude reports whether the process is Claude by the walk's name rule
// (design-spec.md, Liveness): its name is claude, or its executable is a
// versioned binary under Claude Code's versions/ directory, as when an IDE
// or the updater starts it by that path and it is named by its version.
// Never node: an npm or Agent SDK launch is found by CLAUDE_PID alone.
func (p procfs) isClaude(pid int64, comm string) bool {
	if comm == "claude" {
		return true
	}
	exe, err := p.fs.Readlink(p.path(pid, "exe"))
	return err == nil && versioned(exe)
}

// versioned reports whether exe is <…>/claude/versions/<version>. The
// kernel marks an executable replaced since it started, as Claude Code's
// updater replaces them, with " (deleted)".
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
func (p procfs) nested(pid, ppid int64) *bool {
	var yes bool
	if env, err := p.fs.ReadFile(p.path(pid, "environ")); err == nil {
		yes = hasVar(env, "CLAUDECODE")
		return &yes
	}
	outer, ok := p.walk(ppid)
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

// stat is what sesshin reads of /proc/<pid>/stat.
type stat struct {
	comm      string
	ppid      int64
	starttime string
}

// stat reads /proc/<pid>/stat. The name, field 2, is in parentheses and
// can hold spaces and parentheses itself, so the fields after it are read
// after the last ")": the state (field 3), the ppid (4), and the start time
// in clock ticks since boot (22).
func (p procfs) stat(pid int64) (stat, error) {
	data, err := p.fs.ReadFile(p.path(pid, "stat"))
	if err != nil {
		return stat{}, err
	}
	open, end := bytes.IndexByte(data, '('), bytes.LastIndexByte(data, ')')
	if open < 0 || end < open {
		return stat{}, errMalformed
	}
	f := strings.Fields(string(data[end+1:]))
	if len(f) < 20 {
		return stat{}, errMalformed
	}
	ppid, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || ppid < 0 {
		return stat{}, errMalformed
	}
	if !digits(f[19]) {
		return stat{}, errMalformed
	}
	return stat{comm: string(data[open+1 : end]), ppid: ppid, starttime: f[19]}, nil
}

// bootID reads the boot's UUID, which the kernel makes anew at each boot.
func (p procfs) bootID() (string, error) {
	data, err := p.fs.ReadFile(p.root + "/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := string(bytes.TrimSuffix(data, []byte("\n")))
	if !isBootID(id) {
		return "", errMalformed
	}
	return id, nil
}

// isBootID reports whether s is a UUID as the kernel writes it: 36
// characters, lowercase hex and hyphens.
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

func (p procfs) path(pid int64, name string) string {
	return p.root + "/" + strconv.FormatInt(pid, 10) + "/" + name
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
)

// FindWith looks up Claude's process with lookup, or with Find when lookup is
// nil, the seam tests use (hooks-spec.md, Late adoption; statusline step 2).
func FindWith(lookup func(fsys.FS, string) Claude, files fsys.FS, pid string) Claude {
	if lookup == nil {
		lookup = Find
	}
	return lookup(files, pid)
}
