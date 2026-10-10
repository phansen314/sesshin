package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path"
	"strconv"
	"strings"
	"syscall"
)

// sysctlTable is macOS's process table, read through sysctl. Its three
// reads are functions, so everything it makes of them (the kinfo_proc
// fields, KERN_PROCARGS2's layout, the boot session, the errors) builds and
// is tested everywhere; proc_darwin.go supplies the real ones.
type sysctlTable struct {
	// kinfo reads kern.proc.pid.<pid>.
	kinfo func(pid int64) (kinfo, error)
	// procargs reads kern.procargs2.<pid>.
	procargs func(pid int64) ([]byte, error)
	// bootSession reads kern.bootsessionuuid.
	bootSession func() (string, error)
}

// kinfo is what sesshin takes from a kinfo_proc.
type kinfo struct {
	comm []byte // kp_proc.p_comm, NUL-padded
	ppid int32  // kp_eproc.e_ppid
	sec  int64  // kp_proc.p_starttime.tv_sec
	usec int32  // kp_proc.p_starttime.tv_usec
}

// kinfo_proc as macOS lays it out on amd64 and arm64: 648 bytes, with the
// controlling terminal's device (kp_eproc.e_tdev, a 32-bit dev_t) at byte 572,
// in the machine's byte order. A darwin test checks both against
// x/sys/unix's KinfoProc.
const (
	kinfoSize    = 648
	kinfoTdevOff = 572
)

// noDev is e_tdev's NODEV: the process has no controlling terminal.
const noDev = 0xffffffff

// parseTdev reads the controlling terminal's device number from a
// kern.proc.pid buffer. An empty buffer is no such process, which is how the
// kernel answers for a pid that does not exist; a buffer of any other size
// is malformed; NODEV is no terminal.
func parseTdev(b []byte) (uint64, error) {
	switch {
	case len(b) == 0:
		return 0, ErrNoProcess
	case len(b) != kinfoSize:
		return 0, errMalformed
	}
	dev := binary.NativeEndian.Uint32(b[kinfoTdevOff:])
	if dev == noDev {
		return 0, ErrNoTTY
	}
	return uint64(dev), nil
}

// maxPID is the largest pid sysctl can be asked about: its name is an
// array of C ints, so a larger one would wrap to another process's.
const maxPID = 1<<31 - 1

func (sysctlTable) system() string { return "darwin" }

func (sysctlTable) gone(err error) bool { return err == ErrNoProcess }

func (s sysctlTable) stat(pid int64) (stat, error) {
	if pid <= 0 || pid > maxPID {
		return stat{}, ErrNoProcess
	}
	k, err := s.kinfo(pid)
	if err != nil {
		return stat{}, kinfoErr(err)
	}
	return k.stat()
}

// kinfoErr is an error from reading kern.proc.pid: ESRCH is no such
// process, and so is EIO, which is how x/sys/unix's SysctlKinfoProc
// reports the kernel answering with no entry, as it does for a pid that
// doesn't exist. Anything else is the read failing.
func kinfoErr(err error) error {
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EIO) {
		return ErrNoProcess
	}
	return err
}

// stat is k as a lookup reads it: the name up to its first NUL, the parent,
// and the start time as <sec>.<usec>, the microseconds as six digits.
func (k kinfo) stat() (stat, error) {
	if k.ppid < 0 || k.sec < 0 || k.usec < 0 || k.usec >= 1e6 {
		return stat{}, errMalformed
	}
	comm, _, _ := bytes.Cut(k.comm, []byte{0})
	usec := strconv.Itoa(int(k.usec))
	return stat{
		comm:      string(comm),
		ppid:      int64(k.ppid),
		starttime: strconv.FormatInt(k.sec, 10) + "." + strings.Repeat("0", 6-len(usec)) + usec,
	}, nil
}

// claudeExe: the path the process was started by is <…>/claude or a
// versioned binary under Claude Code's versions/ directory.
func (s sysctlTable) claudeExe(pid int64) bool {
	exe, _, err := s.args(pid)
	return err == nil && (path.Base(exe) == "claude" || versioned(exe))
}

func (s sysctlTable) environ(pid int64) ([]byte, error) {
	_, env, err := s.args(pid)
	return env, err
}

// args reads and parses the process's KERN_PROCARGS2.
func (s sysctlTable) args(pid int64) (exe string, env []byte, err error) {
	if pid <= 0 || pid > maxPID {
		return "", nil, ErrNoProcess
	}
	b, err := s.procargs(pid)
	if err != nil {
		return "", nil, err
	}
	return parseProcargs(b)
}

// bootID is kern.bootsessionuuid, which macOS makes anew at each boot,
// lowercased; it must then be a UUID.
func (s sysctlTable) bootID() (string, error) {
	id, err := s.bootSession()
	if err != nil {
		return "", err
	}
	id = strings.ToLower(id)
	if !isBootID(id) {
		return "", errMalformed
	}
	return id, nil
}

// parseProcargs splits a KERN_PROCARGS2 buffer into the executable path and
// the environment, NUL-separated. Its layout: argc, a 32-bit integer in the
// machine's byte order; the executable path and its NUL; NUL padding; argc
// argument strings, each with its NUL; then the environment's strings, each
// with its NUL, until an empty string, after which come Apple's own
// strings, which are not the environment. A buffer that ends before the
// empty string is malformed: the environment may have been cut short.
//
// An empty first argument can't be told from the padding, which makes the
// environment start one string late; ps and every other reader share that.
func parseProcargs(b []byte) (exe string, env []byte, err error) {
	if len(b) < 4 {
		return "", nil, errMalformed
	}
	argc := int32(binary.NativeEndian.Uint32(b))
	if argc < 0 {
		return "", nil, errMalformed
	}
	rest := b[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return "", nil, errMalformed
	}
	exe, rest = string(rest[:i]), rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for range argc {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			return "", nil, errMalformed
		}
		rest = rest[i+1:]
	}
	n := 0
	for {
		i := bytes.IndexByte(rest[n:], 0)
		if i < 0 {
			return "", nil, errMalformed
		}
		if i == 0 {
			return exe, rest[:n], nil
		}
		n += i + 1
	}
}
