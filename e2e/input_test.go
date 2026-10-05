package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// --input reads any path that can be read to the end, a named pipe included
// (cli-spec.md, Input): the same as a process substitution, which sh lacks.
func TestInputFromFIFO(t *testing.T) {
	h := New(t)
	fifo := filepath.Join(t.TempDir(), "in")
	cmd := "mkfifo " + shQuote(fifo) + " && { printf '{}' > " + shQuote(fifo) + " & } && " + shQuote(h.SesshinPath) + " version -i " + shQuote(fifo)
	res := h.Run(cmd, "")
	if res.Exit != 0 || !strings.HasPrefix(res.Stdout, `{"ok":true,"result":{"version":`) || res.Stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	cmd = "mkfifo " + shQuote(fifo) + ".2 && { printf '{\"x\":1}' > " + shQuote(fifo) + ".2 & } && " + shQuote(h.SesshinPath) + " version -i " + shQuote(fifo) + ".2"
	res = h.Run(cmd, "")
	if res.Exit != 1 || !strings.Contains(res.Stdout, `"kind":"invalid-input"`) || !strings.HasPrefix(res.Stderr, "sesshin: invalid-input: ") {
		t.Errorf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
}

func TestInputFromStdin(t *testing.T) {
	h := New(t)
	res := h.Run(shQuote(h.SesshinPath)+" version -i -", "{}")
	if res.Exit != 0 || !strings.HasPrefix(res.Stdout, `{"ok":true,"result":{"version":`) {
		t.Errorf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
	res = h.Run(shQuote(h.SesshinPath)+" version -i -", "")
	if res.Exit != 1 || !strings.Contains(res.Stdout, `"reason":"empty"`) {
		t.Errorf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
}

func TestInputMissingFile(t *testing.T) {
	res := New(t).Sesshin("version", "-i", "/nonexistent/in.json")
	want := `"error":{"kind":"io","message":"/nonexistent/in.json: ENOENT","details":{"code":"ENOENT","path":"/nonexistent/in.json"}}`
	if res.Exit != 1 || !strings.Contains(res.Stdout, want) || res.Stderr != "sesshin: io: /nonexistent/in.json: ENOENT\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", res.Exit, res.Stdout, res.Stderr)
	}
}
