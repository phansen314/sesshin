package iterm2

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Every script compiles. Compiling resolves iTerm2's terminology but sends it
// nothing, and does not start it (checked with another app: compiling a
// `tell application id` script left it quit); the test is skipped all the same
// when iTerm2 is not running, rather than risk starting it.
func TestScriptsCompile(t *testing.T) {
	if err := exec.Command("pgrep", "-x", "iTerm2").Run(); err != nil {
		t.Skip("iTerm2 is not running")
	}
	if _, err := exec.LookPath("osacompile"); err != nil {
		t.Skip("no osacompile")
	}
	dir := t.TempDir()
	for name, s := range map[string]string{"list": listScript, "tty": ttyScript, "launch": launchScript, "paste": pasteScript, "enter": enterScript, "focus": focusScript} {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(dir, name+".applescript")
			if err := os.WriteFile(src, []byte(s), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("osacompile", "-o", filepath.Join(dir, name+".scpt"), src).CombinedOutput()
			if err != nil {
				t.Errorf("osacompile: %v\n%s", err, out)
			}
		})
	}
}
