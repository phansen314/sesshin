package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/ops"
)

// install and uninstall are commands with --dry-run and --input, running
// against the Setup the Env gives them.
func TestInstallUninstall(t *testing.T) {
	home := t.TempDir()
	setup := ops.Setup{
		FS:         fsys.OS{},
		GOOS:       "linux",
		Getenv:     func(k string) string { return map[string]string{"HOME": home}[k] },
		Executable: func() (string, error) { return "/opt/sesshin/sesshin", nil },
		Now:        func() time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) },
		Build:      buildinfo.Info{Version: "v1.0.0"},
		SelfTest:   func(string, buildinfo.Info) *ops.Error { return nil },
	}
	runSetup := func(stdin string, args ...string) (string, int) {
		var out, errOut bytes.Buffer
		code := Run(args, Env{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut, Setup: &setup,
			BuildInfo: func() buildinfo.Info { return setup.Build }})
		return out.String(), code
	}
	state := stateDir(t, home)

	out, code := runSetup("", "install", "--dry-run")
	checkLine(t, out, "install-output")
	if code != ExitOK || !strings.Contains(out, `"dry_run":true`) || !strings.Contains(out, `"proposal_path":null,"apply":null`) {
		t.Errorf("code %d: %s", code, out)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote %v", err)
	}
	// --input is the same input.
	out2, _ := runSetup(`{"dry_run": true}`, "install", "-i", "-")
	if out2 != out {
		t.Errorf("--input: %s", out2)
	}
	out, code = runSetup(`{"dry_run": "yes"}`, "uninstall", "-i", "-")
	checkLine(t, out, "")
	if code != ExitError || !strings.Contains(out, `"kind":"invalid-input"`) {
		t.Errorf("code %d: %s", code, out)
	}

	out, code = runSetup("", "install")
	checkLine(t, out, "install-output")
	var env struct {
		Result struct {
			Apply []string `json:"apply"`
		}
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || code != ExitOK || len(env.Result.Apply) != 2 {
		t.Fatalf("code %d, %v: %s", code, err, out)
	}
	if _, err := os.Stat(filepath.Join(state, "settings.proposed.json")); err != nil {
		t.Error(err)
	}
	out, code = runSetup("", "uninstall")
	checkLine(t, out, "uninstall-output")
	if code != ExitOK || !strings.Contains(out, `"changes":[]`) {
		t.Errorf("code %d: %s", code, out)
	}
	out, code = runSetup("", "uninstall", "extra")
	if code != ExitUsage {
		t.Errorf("code %d: %s", code, out)
	}
}
