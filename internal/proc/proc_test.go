package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/sesshin/internal/fsys"
)

const bootID = "100669df-91d3-407e-bbf2-db8e8be44c90"

// proc is one process in a fake table.
type proc struct {
	pid, ppid int64
	comm      string
	exe       string // "" for none
	environ   string // NUL-separated; "-" for an unreadable one
}

// table writes a fake /proc holding procs and returns it.
func table(t *testing.T, procs ...proc) procfs {
	t.Helper()
	root := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("sys/kernel/random/boot_id", bootID+"\n")
	for _, p := range procs {
		dir := strconv.FormatInt(p.pid, 10)
		// Fields 1-22 of a real stat line, then the rest; starttime is 1000+pid.
		write(dir+"/stat", strconv.FormatInt(p.pid, 10)+" ("+p.comm+") S "+strconv.FormatInt(p.ppid, 10)+
			" 1 1 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 12 0 "+strconv.FormatInt(1000+p.pid, 10)+" 1000 200 18446744073709551615 0 0 0\n")
		if p.environ != "-" {
			write(dir+"/environ", p.environ)
		}
		if p.exe != "" {
			if err := os.Symlink(p.exe, filepath.Join(root, dir, "exe")); err != nil {
				t.Fatal(err)
			}
		}
	}
	return procfs{fsys.OS{}, root}
}

const versions = "/home/me/.local/share/claude/versions/"

func TestFind(t *testing.T) {
	top := "PATH=/bin\x00TERM=xterm-kitty\x00"
	inner := "PATH=/bin\x00CLAUDECODE=1\x00"
	for _, tc := range []struct {
		name      string
		procs     []proc
		claudePID string
		want      int64
		nested    string // "true", "false", or "nil"
	}{
		{"CLAUDE_PID is the parent", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 5, "claude", "", top}}, "9", 9, "false"},
		{"CLAUDE_PID is the parent's parent", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "", ""}, {8, 5, "node", "", top}}, "8", 8, "false"},
		{"CLAUDE_PID further up is an outer session's", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "", ""}, {8, 7, "sh", "", ""}, {7, 5, "node", "", top}}, "7", 0, "nil"},
		{"CLAUDE_PID further up, a claude below it", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "", ""}, {8, 7, "claude", "", inner}, {7, 5, "claude", "", top}}, "7", 8, "true"},
		{"no CLAUDE_PID: walk by name", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "", ""}, {8, 7, "claude", "", top}, {7, 1, "bash", "", ""}}, "", 8, "false"},
		{"bad CLAUDE_PID: walk", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "claude", "", top}}, "x9", 9, "false"},
		{"CLAUDE_PID 1: walk", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "claude", "", top}}, "1", 9, "false"},
		{"CLAUDE_PID signed: walk", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "claude", "", top}}, "+9", 9, "false"},
		{"versioned executable", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "2.1.288", versions + "2.1.288", top}}, "", 9, "false"},
		{"versioned executable, replaced", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "2.1.288", versions + "2.1.288 (deleted)", top}}, "", 9, "false"},
		{"node is never claude by name", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "node", "/usr/bin/node", top}}, "", 0, "nil"},
		{"not quite claude", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "claude-x", "/opt/claude/bin/2.1", ""}, {8, 1, "claude ", versions + "x", ""}}, "", 0, "nil"},
		{"no Claude", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "bash", "", ""}}, "", 0, "nil"},
		{"a cycle", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "sh", "", ""}, {8, 9, "sh", "", ""}}, "", 0, "nil"},
		{"a gap in the ancestry", []proc{{10, 9, "sesshin-hook", "", ""}}, "", 0, "nil"},
		{"nested", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "claude", "", inner}}, "9", 9, "true"},
		{"CLAUDECODE by name only", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "claude", "", "XCLAUDECODE=1\x00CLAUDECODE_X=1\x00CLAUDECODE"}}, "9", 9, "false"},
		{"CLAUDECODE empty", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "claude", "", "CLAUDECODE="}}, "9", 9, "true"},
		{"environ unreadable, claude above", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "claude", "", "-"}, {8, 7, "bash", "", ""}, {7, 1, "claude", "", ""}}, "9", 9, "true"},
		{"environ unreadable, none above", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "claude", "", "-"}, {8, 1, "bash", "", ""}}, "9", 9, "false"},
		{"environ unreadable, ancestry unreadable", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "claude", "", "-"}}, "9", 9, "nil"},
		{"a name with parentheses and spaces", []proc{{10, 9, "sesshin-hook", "", ""}, {9, 8, "a) (b", "", ""}, {8, 1, "claude", "", top}}, "", 8, "false"},
	} {
		got := table(t, tc.procs...).find(10, tc.claudePID)
		nested := "nil"
		if got.Nested != nil {
			nested = strconv.FormatBool(*got.Nested)
		}
		if got.PID != tc.want || nested != tc.nested {
			t.Errorf("%s: pid %d, nested %s; want %d, %s", tc.name, got.PID, nested, tc.want, tc.nested)
		}
		wantStarted := ""
		if tc.want != 0 {
			wantStarted = "linux:" + bootID + ":" + strconv.FormatInt(1000+tc.want, 10)
		}
		if got.StartedAt != wantStarted {
			t.Errorf("%s: started at %q, want %q", tc.name, got.StartedAt, wantStarted)
		}
	}
}

// Without a start time there is no pid: pid_started_at is null exactly when
// pid is.
func TestFindNeedsStartTime(t *testing.T) {
	procs := []proc{{10, 9, "sesshin-hook", "", ""}, {9, 1, "claude", "", ""}}
	p := table(t, procs...)
	for name, boot := range map[string]string{"malformed": "not-a-boot-id\n", "uppercase": strings.ToUpper(bootID)} {
		if err := os.WriteFile(filepath.Join(p.root, "sys/kernel/random/boot_id"), []byte(boot), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := p.find(10, "9"); got != (Claude{}) {
			t.Errorf("boot_id %s: %+v", name, got)
		}
	}
	p = table(t, procs...)
	if err := os.WriteFile(filepath.Join(p.root, "9/stat"), []byte("9 (claude) S 1 1 1 0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := p.find(10, "9"); got != (Claude{}) {
		t.Errorf("short stat: %+v", got)
	}
	p = table(t, procs...)
	p.fs = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, p.root+"/sys/kernel/random/boot_id", 1, syscall.EACCES)}
	if got := p.find(10, "9"); got != (Claude{}) {
		t.Errorf("boot_id unreadable: %+v", got)
	}
}

func TestStat(t *testing.T) {
	p := table(t)
	for _, tc := range []struct {
		line string
		want stat
		ok   bool
	}{
		{"9 (claude) S 4 1 1 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 12 0 777 1000", stat{"claude", 4, "777"}, true},
		{"9 ()) (x) S 4 1 1 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 12 0 777 1000", stat{")) (x", 4, "777"}, true},
		{"9 (a\nb) S 4 1 1 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 12 0 777", stat{"a\nb", 4, "777"}, true},
		{"9 (claude) S 4 1 1 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 12 0", stat{}, false},
		{"9 (claude) S x 1 1 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 12 0 777", stat{}, false},
		{"9 (claude) S 4 1 1 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 12 0 -7", stat{}, false},
		{"9 claude S 4", stat{}, false},
		{"", stat{}, false},
	} {
		if err := os.MkdirAll(filepath.Join(p.root, "9"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.root, "9/stat"), []byte(tc.line), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := p.stat(9)
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("%q: %+v, %v; want %+v, ok %v", tc.line, got, err, tc.want, tc.ok)
		}
	}
}

func TestVersioned(t *testing.T) {
	for exe, want := range map[string]bool{
		versions + "2.1.288":             true,
		versions + "2.1.288 (deleted)":   true,
		"/opt/claude/versions/3.0.0-rc1": true,
		versions:                         false,
		versions + "latest":              false,
		"/home/me/claude-versions/2.1.0": false,
		"/home/me/versions/2.1.0":        false,
		"/usr/bin/node":                  false,
		"2.1.288":                        false,
	} {
		if got := versioned(exe); got != want {
			t.Errorf("versioned(%q) = %v", exe, got)
		}
	}
}

// No such process is ErrNoProcess; a missing boot ID is the check failing,
// though its errno is ENOENT too.
func TestStartedAtErrors(t *testing.T) {
	p := table(t, proc{9, 1, "claude", "", ""})
	if got, err := p.startedAt(9); err != nil || got != "linux:"+bootID+":1009" {
		t.Errorf("startedAt(9) = %q, %v", got, err)
	}
	if _, err := p.startedAt(8); err != ErrNoProcess {
		t.Errorf("no process: %v", err)
	}
	p.fs = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, p.root+"/9/stat", 1, syscall.ESRCH)}
	if _, err := p.startedAt(9); err != ErrNoProcess {
		t.Errorf("vanished: %v", err)
	}
	p.fs = fsys.OS{}
	if err := os.Remove(filepath.Join(p.root, "sys/kernel/random/boot_id")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.startedAt(9); err == nil || err == ErrNoProcess {
		t.Errorf("boot ID missing: %v", err)
	}
}
