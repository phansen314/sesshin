package cli

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/iterm2"
)

// What Launch writes, launch-exec runs: byte for byte, whatever the bytes are,
// since nothing but the nonce goes through iTerm2's splitting of a command.
func TestLaunchRoundTrip(t *testing.T) {
	argv := []string{
		"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude",
		"--name", "it's \"quoted\" \\ back", "--", "line one\nline two $(touch /tmp/x) `id` ${HOME} ~ *", "héllo, 世界 🙂", "",
	}
	cwd := "/work dir/it's 'q' é"
	var cmd string
	run := iterm2.RunnerFunc(func(script string, args []string, _ time.Duration) ([]byte, error) {
		cmd = args[2]
		return []byte("AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE\n"), nil
	})
	store := iterm2.Store{Dir: filepath.Join(t.TempDir(), "launches")}
	b := iterm2.Backend{GOOS: "darwin", Runner: run, Launches: store, Executable: func() (string, error) { return "/opt/my bin/sesshin", nil }}
	_, err := b.Launch(placement.LaunchSpec{
		Caller: iterm2.PlacementOf("2E30574E-F9EE-4D62-BE94-56A54E66E0D5"), Type: "tab", Cwd: cwd,
		Env:  []placement.Var{{Name: "SESSHIN_TOKEN", Value: "t\nok'en"}, {Name: "SESSHIN_JOB", Value: "ünï"}},
		Argv: argv,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The command iTerm2 splits is two words and the nonce.
	nonce, ok := strings.CutPrefix(cmd, "'/opt/my bin/sesshin' launch-exec ")
	if !ok || !iterm2.IsNonce(nonce) {
		t.Fatalf("command %q", cmd)
	}

	var gotPath, gotDir string
	var gotArgv, gotEnv []string
	le := LaunchEnv{
		Store:       store,
		Environ:     func() []string { return []string{"HOME=/h", "SESSHIN_JOB=old"} },
		Chdir:       func(d string) error { gotDir = d; return nil },
		Exec:        func(p string, a, e []string) error { gotPath, gotArgv, gotEnv = p, a, e; return nil },
		Interactive: func() bool { return false },
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"launch-exec", nonce}, Env{Stdout: &out, Stderr: &errOut, Launch: &le}); code != 0 {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
	wantEnv := []string{"HOME=/h", "SESSHIN_JOB=ünï", "SESSHIN_TOKEN=t\nok'en"}
	if gotPath != argv[0] || !slices.Equal(gotArgv, argv) || gotDir != cwd || !slices.Equal(gotEnv, wantEnv) {
		t.Errorf("path %q dir %q\nargv %q\nenv %q", gotPath, gotDir, gotArgv, gotEnv)
	}
	// Read once: the file is gone.
	if code := Run([]string{"launch-exec", nonce}, Env{Stdout: &out, Stderr: &errOut, Launch: &le}); code == 0 {
		t.Error("a second read ran")
	}
}
