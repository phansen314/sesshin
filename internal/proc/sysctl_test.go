package proc

import (
	"encoding/binary"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// The macOS table's parsing and rules, over a fake sysctl: what can be
// tested without a Mac.

const sessionUUID = "6A1F2B3C-4D5E-4F60-8172-93A4B5C6D7E8"

// procargs2 builds a KERN_PROCARGS2 buffer: argc in this machine's byte
// order, the executable path, pad NULs, the arguments, the environment, the
// empty string that ends it, and Apple's strings.
func procargs2(exe string, pad int, args, env, apple []string) []byte {
	b := binary.NativeEndian.AppendUint32(nil, uint32(len(args)))
	b = append(b, exe...)
	b = append(b, make([]byte, 1+pad)...)
	for _, s := range args {
		b = append(append(b, s...), 0)
	}
	for _, s := range env {
		b = append(append(b, s...), 0)
	}
	b = append(b, 0)
	for _, s := range apple {
		b = append(append(b, s...), 0)
	}
	return b
}

func TestParseProcargs(t *testing.T) {
	// A buffer as macOS lays it out on amd64 and arm64 (little-endian):
	// argc 2, the path, padding to a word, the arguments, the environment,
	// the empty string, then Apple's strings, which are not the environment.
	literal := []byte("\x02\x00\x00\x00/Users/me/.local/bin/claude\x00\x00\x00\x00\x00" +
		"claude\x00--resume\x00PATH=/usr/bin\x00CLAUDECODE=1\x00\x00" +
		"executable_path=/Users/me/.local/bin/claude\x00ptr_munge=\x00\x00\x00")
	if binary.NativeEndian.Uint32([]byte{2, 0, 0, 0}) != 2 {
		t.Skip("the literal fixture is little-endian")
	}
	exe, env, err := parseProcargs(literal)
	if err != nil || exe != "/Users/me/.local/bin/claude" || string(env) != "PATH=/usr/bin\x00CLAUDECODE=1\x00" {
		t.Errorf("literal: %q, %q, %v", exe, env, err)
	}

	for _, tc := range []struct {
		name string
		buf  []byte
		exe  string
		env  string
		ok   bool
	}{
		{"plain", procargs2("/bin/claude", 0, []string{"claude"}, []string{"A=1", "B="}, nil), "/bin/claude", "A=1\x00B=\x00", true},
		{"padded", procargs2("/bin/claude", 7, []string{"claude", "-p"}, []string{"A=1"}, []string{"x=y"}), "/bin/claude", "A=1\x00", true},
		{"no arguments", procargs2("/bin/claude", 3, nil, []string{"A=1"}, nil), "/bin/claude", "A=1\x00", true},
		{"no environment", procargs2("/bin/claude", 2, []string{"claude"}, nil, []string{"CLAUDECODE=1"}), "/bin/claude", "", true},
		{"Apple's strings are not the environment", procargs2("/bin/claude", 0, []string{"claude"}, []string{"A=1"}, []string{"CLAUDECODE=1"}), "/bin/claude", "A=1\x00", true},
		{"empty path", procargs2("", 0, []string{"claude"}, []string{"A=1"}, nil), "", "A=1\x00", true},
		{"empty", nil, "", "", false},
		{"short", []byte{1, 0}, "", "", false},
		{"only argc", binary.NativeEndian.AppendUint32(nil, 0), "", "", false},
		{"path unterminated", append(binary.NativeEndian.AppendUint32(nil, 0), "/bin/claude"...), "", "", false},
		{"fewer arguments than argc", append(binary.NativeEndian.AppendUint32(nil, 3), "/bin/claude\x00\x00claude\x00"...), "", "", false},
		{"environment cut short", append(binary.NativeEndian.AppendUint32(nil, 1), "/bin/claude\x00\x00claude\x00A=1\x00B=2"...), "", "", false},
		{"environment without its end", append(binary.NativeEndian.AppendUint32(nil, 1), "/bin/claude\x00\x00claude\x00A=1\x00"...), "", "", false},
		{"negative argc", append(binary.NativeEndian.AppendUint32(nil, 0xffffffff), "/bin/claude\x00\x00\x00"...), "", "", false},
		{"huge argc", append(binary.NativeEndian.AppendUint32(nil, 1<<30), "/bin/claude\x00\x00a\x00\x00"...), "", "", false},
	} {
		exe, env, err := parseProcargs(tc.buf)
		if (err == nil) != tc.ok || exe != tc.exe || string(env) != tc.env {
			t.Errorf("%s: %q, %q, %v; want %q, %q, ok %v", tc.name, exe, env, err, tc.exe, tc.env, tc.ok)
		}
	}
}

func TestKinfoStat(t *testing.T) {
	comm := func(s string) []byte { b := make([]byte, 17); copy(b, s); return b }
	for _, tc := range []struct {
		name string
		k    kinfo
		want stat
		ok   bool
	}{
		{"plain", kinfo{comm("claude"), 4, 1759900000, 123456}, stat{"claude", 4, "1759900000.123456"}, true},
		{"microseconds padded", kinfo{comm("2.1.288"), 1, 1759900000, 42}, stat{"2.1.288", 1, "1759900000.000042"}, true},
		{"zero", kinfo{comm(""), 0, 0, 0}, stat{"", 0, "0.000000"}, true},
		{"a full name, no NUL", kinfo{[]byte("0123456789abcdefg"), 1, 1, 1}, stat{"0123456789abcdefg", 1, "1.000001"}, true},
		{"after the NUL is not the name", kinfo{[]byte("sh\x00claude\x00"), 1, 1, 1}, stat{"sh", 1, "1.000001"}, true},
		{"negative ppid", kinfo{comm("claude"), -1, 1, 1}, stat{}, false},
		{"negative seconds", kinfo{comm("claude"), 1, -1, 1}, stat{}, false},
		{"negative microseconds", kinfo{comm("claude"), 1, 1, -1}, stat{}, false},
		{"a whole second of microseconds", kinfo{comm("claude"), 1, 1, 1000000}, stat{}, false},
	} {
		got, err := tc.k.stat()
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("%s: %+v, %v; want %+v, ok %v", tc.name, got, err, tc.want, tc.ok)
		}
	}
}

func TestKinfoErr(t *testing.T) {
	for _, err := range []error{syscall.ESRCH, syscall.EIO, os.NewSyscallError("sysctl", syscall.ESRCH)} {
		if got := kinfoErr(err); got != ErrNoProcess {
			t.Errorf("kinfoErr(%v) = %v", err, got)
		}
	}
	for _, err := range []error{syscall.EPERM, syscall.ENOMEM, errors.New("other")} {
		if got := kinfoErr(err); got != err {
			t.Errorf("kinfoErr(%v) = %v", err, got)
		}
	}
}

// fakeSysctl is a sysctl table holding procs: exe is the path each was
// started by; an environ of "-" can't be read. Start times are
// 1759900000+pid seconds and pid microseconds.
func fakeSysctl(procs ...proc) sysctlTable {
	byPID := map[int64]proc{}
	for _, p := range procs {
		byPID[p.pid] = p
	}
	return sysctlTable{
		kinfo: func(pid int64) (kinfo, error) {
			p, ok := byPID[pid]
			if !ok {
				return kinfo{}, syscall.EIO // as SysctlKinfoProc reports no entry
			}
			comm := make([]byte, 17)
			copy(comm, p.comm)
			return kinfo{comm, int32(p.ppid), 1759900000 + pid, int32(pid)}, nil
		},
		procargs: func(pid int64) ([]byte, error) {
			p, ok := byPID[pid]
			if !ok || p.environ == "-" {
				return nil, syscall.EINVAL
			}
			var env []string
			if p.environ != "" {
				env = strings.Split(strings.TrimSuffix(p.environ, "\x00"), "\x00")
			}
			return procargs2(p.exe, 4, []string{p.comm}, env, []string{"executable_path=" + p.exe}), nil
		},
		bootSession: func() (string, error) { return sessionUUID, nil },
	}
}

func darwinStarted(pid int64) string {
	return "darwin:" + strings.ToLower(sessionUUID) + ":" + strconv.FormatInt(1759900000+pid, 10) + "." + strings.Repeat("0", 6-len(strconv.FormatInt(pid, 10))) + strconv.FormatInt(pid, 10)
}

// The shared rules over the macOS table, with its own name rule: an
// executable path ending in /claude counts, as a versioned one does.
func TestFindSysctl(t *testing.T) {
	top := "PATH=/bin\x00TERM=xterm-kitty\x00"
	inner := "PATH=/bin\x00CLAUDECODE=1\x00"
	const app = "/Users/me/.local/share/claude/versions/"
	for _, tc := range []struct {
		name      string
		procs     []proc
		claudePID string
		want      int64
		nested    string
	}{
		{"CLAUDE_PID is the parent", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 5, "claude", "/bin/claude", top}}, "9", 9, "false"},
		{"CLAUDE_PID is the parent's parent", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "/bin/sh", ""}, {8, 5, "node", "/usr/bin/node", top}}, "8", 8, "false"},
		{"CLAUDE_PID further up", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "", ""}, {8, 7, "sh", "", ""}, {7, 5, "node", "", top}}, "7", 0, "nil"},
		{"walk by name", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "/bin/sh", ""}, {8, 1, "claude", "/opt/x", top}}, "", 8, "false"},
		{"walk by path", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "zsh", "/bin/zsh", ""}, {8, 1, "2.1.288", "/Users/me/.local/bin/claude", top}}, "", 8, "false"},
		{"walk by versioned path", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "2.1.288", app + "2.1.288", top}}, "", 9, "false"},
		{"not quite claude", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "claude-x", "/opt/claude-x", ""}, {8, 1, "x", app + "latest", ""}}, "", 0, "nil"},
		{"node is never claude by name", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "node", "/usr/local/bin/node", top}}, "", 0, "nil"},
		{"nested", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "claude", "/bin/claude", inner}}, "9", 9, "true"},
		{"arguments unreadable, claude above", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "claude", "", "-"}, {8, 7, "bash", "", ""}, {7, 1, "claude", "", ""}}, "9", 9, "true"},
		{"arguments unreadable, none above", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "claude", "", "-"}, {8, 1, "bash", "", ""}}, "9", 9, "false"},
		{"arguments unreadable, ancestry unreadable", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "claude", "", "-"}}, "9", 9, "nil"},
		{"a cycle", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "", ""}, {8, 9, "sh", "", ""}}, "", 0, "nil"},
		{"a gap in the ancestry", []proc{{10, 9, "sesshin-hook", "", ""}}, "", 0, "nil"},
	} {
		got := find(fakeSysctl(tc.procs...), 10, tc.claudePID)
		nested := "nil"
		if got.Nested != nil {
			nested = strconv.FormatBool(*got.Nested)
		}
		if got.PID != tc.want || nested != tc.nested {
			t.Errorf("%s: pid %d, nested %s; want %d, %s", tc.name, got.PID, nested, tc.want, tc.nested)
		}
		wantStarted := ""
		if tc.want != 0 {
			wantStarted = darwinStarted(tc.want)
		}
		if got.StartedAt != wantStarted {
			t.Errorf("%s: started at %q, want %q", tc.name, got.StartedAt, wantStarted)
		}
	}
	caller := []proc{{10, 9, "sesshin", "", ""}, {9, 8, "bash", "", ""}, {8, 7, "xargs", "", ""}, {7, 1, "node", "", ""}}
	if got := findCaller(fakeSysctl(caller...), 10, "7"); got.PID != 7 || got.StartedAt != darwinStarted(7) {
		t.Errorf("findCaller: %+v", got)
	}
}

func TestStartedAtSysctl(t *testing.T) {
	s := fakeSysctl(proc{9, 1, "claude", "/bin/claude", ""})
	if got, err := startedAt(s, 9); err != nil || got != darwinStarted(9) {
		t.Errorf("startedAt(9) = %q, %v", got, err)
	}
	if got := darwinStarted(9); got != "darwin:6a1f2b3c-4d5e-4f60-8172-93a4b5c6d7e8:1759900009.000009" {
		t.Errorf("format: %q", got)
	}
	for _, pid := range []int64{8, 0, -1, maxPID + 1, 1<<32 + 9} {
		if _, err := startedAt(s, pid); err != ErrNoProcess {
			t.Errorf("no process %d: %v", pid, err)
		}
	}
	// A pid past a C int is never asked about: it would wrap to another.
	asked := false
	s.kinfo = func(int64) (kinfo, error) { asked = true; return kinfo{}, nil }
	if _, err := startedAt(s, 1<<32+9); err != ErrNoProcess || asked {
		t.Errorf("wrapped pid: %v, asked %v", err, asked)
	}

	s = fakeSysctl(proc{9, 1, "claude", "/bin/claude", ""})
	s.kinfo = func(int64) (kinfo, error) { return kinfo{}, syscall.EPERM }
	if _, err := startedAt(s, 9); err == nil || err == ErrNoProcess {
		t.Errorf("unreadable: %v", err)
	}
	s.kinfo = func(int64) (kinfo, error) { return kinfo{ppid: -1}, nil }
	if _, err := startedAt(s, 9); err == nil || err == ErrNoProcess {
		t.Errorf("malformed: %v", err)
	}

	// The boot session: lowercased, then a UUID; anything else is the check
	// failing, and a lookup finds nothing.
	for _, boot := range []string{"", "not-a-uuid", sessionUUID + "\n", strings.ReplaceAll(sessionUUID, "-", "")} {
		s = fakeSysctl(proc{10, 9, "sesshin-hook", "", ""}, proc{9, 1, "claude", "", ""})
		s.bootSession = func() (string, error) { return boot, nil }
		if _, err := startedAt(s, 9); err == nil || err == ErrNoProcess {
			t.Errorf("boot %q: %v", boot, err)
		}
		if got := find(s, 10, "9"); got != (Claude{}) {
			t.Errorf("boot %q: found %+v", boot, got)
		}
	}
	s.bootSession = func() (string, error) { return "", syscall.ENOENT }
	if _, err := startedAt(s, 9); err == nil || err == ErrNoProcess {
		t.Errorf("boot unreadable: %v", err)
	}
}
