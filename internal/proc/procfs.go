package proc

import (
	"bytes"
	"strconv"
	"strings"
	"syscall"

	"github.com/phansen314/sesshin/internal/fsys"
)

// procfs is a process table in Linux's /proc layout, under root. It builds
// everywhere, so its rules are tested on any system over a fake tree.
type procfs struct {
	fs   fsys.FS
	root string
}

func (p procfs) find(self int64, claudePID string) Claude { return find(p, self, claudePID) }

func (p procfs) findCaller(self int64, claudePID string) Claude {
	return findCaller(p, self, claudePID)
}

func (p procfs) startedAt(pid int64) (string, error) { return startedAt(p, pid) }

func (procfs) system() string { return "linux" }

// gone: the process's stat missing (ENOENT) or vanishing as it is read
// (ESRCH).
func (procfs) gone(err error) bool {
	e, ok := fsys.ErrnoOf(err)
	return ok && (e == syscall.ENOENT || e == syscall.ESRCH)
}

// claudeExe: /proc/<pid>/exe is a versioned binary under Claude Code's
// versions/ directory.
func (p procfs) claudeExe(pid int64) bool {
	exe, err := p.fs.Readlink(p.path(pid, "exe"))
	return err == nil && versioned(exe)
}

func (p procfs) environ(pid int64) ([]byte, error) {
	return p.fs.ReadFile(p.path(pid, "environ"))
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

func (p procfs) path(pid int64, name string) string {
	return p.root + "/" + strconv.FormatInt(pid, 10) + "/" + name
}
