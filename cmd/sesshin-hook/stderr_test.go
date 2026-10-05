package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// With stderr closed, the descriptor silenceStderr opens lands on 2, and it
// must not be close-on-exec: a child started afterwards would find 2 closed.
func TestSilenceStderrClosedInherited(t *testing.T) {
	if os.Getenv("SESSHIN_STDERR_HELPER") == "1" {
		// The Go runtime opens /dev/null over a closed 2 at startup, so close it again.
		syscall.Close(2)
		silenceStderr()
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, 2, syscall.F_GETFD, 0)
		if errno != 0 || flags&syscall.FD_CLOEXEC != 0 {
			os.Exit(3)
		}
		os.Exit(0)
	}
	cmd := exec.Command("sh", "-c", `exec "$0" -test.run=TestSilenceStderrClosedInherited`, os.Args[0])
	cmd.Env = append(os.Environ(), "SESSHIN_STDERR_HELPER=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stderr closed at start: fd 2 missing or close-on-exec afterwards: %v\n%s", err, out)
	}
}
