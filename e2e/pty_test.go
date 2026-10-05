package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// The picker's end-to-end tests drive a real fzf in a pseudo-terminal
// (picker-spec.md, Testing): keys go in through the terminal, and the screen
// is rendered from what fzf draws.

// fzfVar lists the fzf binaries to run the picker tests against, separated
// as PATH is, e.g. SESSHIN_E2E_FZF=/opt/fzf-0.63.0/fzf:/usr/bin/fzf (see
// scripts/fetch-fzf.sh). Unset, they run against fzf on PATH, if any.
const fzfVar = "SESSHIN_E2E_FZF"

// Keys as the terminal sends them.
const (
	keyEnter = "\r"
	keyEsc   = "\x1b"
	keyCtrlA = "\x01"
)

// waitTimeout bounds every wait for the picker; fzf usually answers in
// milliseconds.
const waitTimeout = 10 * time.Second

// eachFzf runs f as a subtest for each fzf binary under test, named by its
// version, with a directory holding it as fzf, to put first on PATH. With
// none, the test skips.
func eachFzf(t *testing.T, f func(t *testing.T, fzfDir string)) {
	t.Helper()
	var bins []string
	if v := os.Getenv(fzfVar); v != "" {
		bins = filepath.SplitList(v)
	} else if p, err := exec.LookPath("fzf"); err == nil {
		bins = []string{p}
	} else {
		t.Skip("no fzf installed; set " + fzfVar)
	}
	for _, bin := range bins {
		out, err := exec.Command(bin, "--version").Output()
		if err != nil {
			t.Fatalf("%s --version: %v", bin, err)
		}
		version, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		t.Run("fzf-"+version, func(t *testing.T) {
			bin, err := filepath.Abs(bin)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.Symlink(bin, filepath.Join(dir, "fzf")); err != nil {
				t.Fatal(err)
			}
			f(t, dir)
		})
	}
}

// term is a process running with a pseudo-terminal as its controlling
// terminal, and the screen it draws there.
type term struct {
	t      *testing.T
	cmd    *exec.Cmd
	ptmx   *os.File
	vt     vt10x.Terminal
	exited chan struct{}
}

// startTerm starts cmd in a session of its own whose controlling terminal
// is a new pseudo-terminal of rows by cols. Its stdin, stdout and stderr
// are the terminal unless cmd already sets them. The process group is
// killed when the test ends.
func startTerm(t *testing.T, cmd *exec.Cmd, rows, cols int) *term {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		t.Fatal(err)
	}
	// The controlling terminal is set from one of the child's descriptors:
	// a standard one that is the terminal, else one passed for the purpose.
	ctty := -1
	if cmd.Stderr == nil {
		cmd.Stderr, ctty = tty, 2
	}
	if cmd.Stdout == nil {
		cmd.Stdout, ctty = tty, 1
	}
	if cmd.Stdin == nil {
		cmd.Stdin, ctty = tty, 0
	}
	if ctty < 0 {
		cmd.ExtraFiles = append(cmd.ExtraFiles, tty)
		ctty = 2 + len(cmd.ExtraFiles)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: ctty}
	if !slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "TERM=") }) {
		cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tty.Close()

	tm := &term{t: t, cmd: cmd, ptmx: ptmx, exited: make(chan struct{})}
	// The emulator answers the terminal's queries, e.g. the cursor
	// position, back through the terminal.
	tm.vt = vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(ptmx))
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 32*1024)
		var pending []byte
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				// The emulator takes whole characters only, and leaves
				// a split one for the next read.
				pending = append(pending, buf[:n]...)
				w, _ := tm.vt.Write(pending)
				pending = append(pending[:0], pending[w:]...)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		cmd.Wait()
		close(tm.exited)
	}()
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-tm.exited
		ptmx.Close()
		<-read
	})
	return tm
}

// send writes keys to the terminal, as typed.
func (tm *term) send(keys string) {
	tm.t.Helper()
	if _, err := tm.ptmx.WriteString(keys); err != nil {
		tm.t.Fatal(err)
	}
}

// screen is the terminal's text, one line per row, trailing spaces
// trimmed, with no trailing empty rows.
func (tm *term) screen() string {
	lines := strings.Split(tm.vt.String(), "\n") // String locks the emulator itself
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// waitScreen waits until the screen contains s, failing the test, with the
// screen, if it doesn't within waitTimeout.
func (tm *term) waitScreen(s string) {
	tm.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !strings.Contains(tm.screen(), s) {
		if time.Now().After(deadline) {
			tm.t.Fatalf("timed out waiting for %q on screen; screen:\n%s", s, tm.screen())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// wait waits for the process to exit and returns its exit code.
func (tm *term) wait() int {
	tm.t.Helper()
	select {
	case <-tm.exited:
	case <-time.After(waitTimeout):
		tm.t.Fatalf("timed out waiting for exit; screen:\n%s", tm.screen())
	}
	return tm.cmd.ProcessState.ExitCode()
}

// withFzf is env with the fzf in fzfDir first on PATH.
func withFzf(env []string, fzfDir string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			kv = "PATH=" + fzfDir + string(filepath.ListSeparator) + v
		}
		out = append(out, kv)
	}
	return out
}
