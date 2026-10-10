package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A wrong argument count, or an option, is a plain message and a nonzero exit,
// never an envelope.
func TestLaunchExecArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"none":   {"launch-exec"},
		"two":    {"launch-exec", launchNonce, launchNonce},
		"option": {"launch-exec", "--help"},
		"flag":   {"launch-exec", "--nope", launchNonce},
	} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			called := false
			le := LaunchEnv{
				Exec:        func(string, []string, []string) error { called = true; return nil },
				Interactive: func() bool { return false },
			}
			code := Run(args, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Launch: &le})
			if code == 0 || called || out.Len() != 0 || !strings.Contains(errOut.String(), "nonce") || strings.Contains(errOut.String(), `"ok"`) {
				t.Errorf("code %d, exec %v, stdout %q, stderr %q", code, called, out.String(), errOut.String())
			}
		})
	}
}
