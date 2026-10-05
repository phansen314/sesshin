package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/ops"
)

// sendCommand is the real send command with an operation that echoes the
// input it was given.
func sendCommand(t *testing.T) []Command {
	return echoCommand(t, "send", ops.DecodeSendInput, func(in ops.SendInput) map[string]any {
		return map[string]any{"session": in.Selector.Raw, "text": in.Text, "submit": in.Submit, "force": in.Force}
	})
}

func runSend(t *testing.T, stdin string, args ...string) (argResult, int) {
	t.Helper()
	return runEcho(t, sendCommand(t), "send", stdin, args)
}

func TestSendArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"defaults", []string{"api", "--text", "run $(it) 'now'"},
			map[string]any{"session": "api", "text": "run $(it) 'now'", "submit": true, "force": false}},
		{"an ID, no submit", []string{"12", "--text", "x", "--submit=false"},
			map[string]any{"session": "12", "text": "x", "submit": false, "force": false}},
		{"force, options first", []string{"--force", "--text", "yes", "job:api"},
			map[string]any{"session": "job:api", "text": "yes", "submit": true, "force": true}},
		{"a text that looks like an option", []string{"api", "--text", "--force"},
			map[string]any{"session": "api", "text": "--force", "submit": true, "force": false}},
		{"submit true", []string{"api", "--text", "x", "--submit"},
			map[string]any{"session": "api", "text": "x", "submit": true, "force": false}},
	} {
		r, code := runSend(t, "", tc.args...)
		if code != ExitOK || !r.OK || !reflect.DeepEqual(r.Result, tc.want) {
			t.Errorf("%s: %d %+v", tc.name, code, r)
		}
	}
}

func TestSendUsage(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "t")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"no session", "", []string{"--text", "x"}},
		{"neither text", "", []string{"api"}},
		{"both texts", "", []string{"api", "--text", "x", "--text-file", file}},
		{"both texts, the other order", "", []string{"api", "--text-file", file, "--text", "x"}},
		{"a second session", "", []string{"api", "web", "--text", "x"}},
		{"a bad boolean", "", []string{"api", "--text", "x", "--submit=maybe"}},
		{"--input with the session", `{"session":"1","text":"x"}`, []string{"-i", "-", "1"}},
		{"--input with --text", `{"session":"1","text":"x"}`, []string{"-i", "-", "--text", "y"}},
		{"--input with --force", `{"session":"1","text":"x"}`, []string{"-i", "-", "--force"}},
		{"an unknown option", "", []string{"api", "--text", "x", "--job", "j"}},
	} {
		r, code := runSend(t, tc.stdin, tc.args...)
		if code != ExitUsage || r.OK || r.Error.Kind != "usage" {
			t.Errorf("%s: %d %+v", tc.name, code, r)
		}
	}
}

func TestSendTextFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The file's content exactly: no trimming, a trailing newline kept.
	body := "line one\n\n\tline two  \n"
	r, _ := runSend(t, "", "api", "--text-file", write("t.txt", body))
	if !r.OK || r.Result["text"] != body {
		t.Errorf("--text-file: %q (%+v)", r.Result["text"], r)
	}
	r, _ = runSend(t, body, "api", "--text-file", "-", "--submit=false")
	if !r.OK || r.Result["text"] != body || r.Result["submit"] != false {
		t.Errorf("--text-file -: %q (%+v)", r.Result["text"], r)
	}
	// Empty: the schema's minLength.
	r, _ = runSend(t, "", "api", "--text-file", write("empty", ""))
	if r.OK || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/text"}) {
		t.Errorf("empty file: %+v", r)
	}
	// Unreadable: io. A directory too.
	for _, p := range []string{filepath.Join(dir, "missing"), dir} {
		r, code := runSend(t, "", "api", "--text-file", p)
		if r.OK || r.Error.Kind != "io" || code != ExitError {
			t.Errorf("%s: %d %+v", p, code, r)
		}
	}
	// Not UTF-8, and a control character: invalid-input at /text.
	for _, content := range []string{"a\xffb", "a\x1bb", "a\x00b"} {
		r, _ = runSend(t, "", "api", "--text-file", write("bad", content))
		if r.OK || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/text"}) {
			t.Errorf("%q: %+v", content, r)
		}
	}
	r, _ = runSend(t, "a\xffb", "api", "--text-file", "-")
	if r.OK || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/text"}) {
		t.Errorf("stdin: %+v", r)
	}
	// --text-file with --input is combined input.
	r, _ = runSend(t, `{"session":"1","text":"x"}`, "-i", "-", "--text-file", write("c", "y"))
	if r.OK || r.Error.Kind != "usage" {
		t.Errorf("%+v", r)
	}
}

func TestSendInput(t *testing.T) {
	r, code := runSend(t, `{"session":"job:api","text":"hi\nthere","submit":false,"force":true}`, "-i", "-")
	want := map[string]any{"session": "job:api", "text": "hi\nthere", "submit": false, "force": true}
	if code != ExitOK || !reflect.DeepEqual(r.Result, want) {
		t.Errorf("%d %+v", code, r)
	}
	// The same checks as the command line's.
	r, code = runSend(t, `{"session":"Bad Selector","text":"a\u001bb"}`, "-i", "-")
	if code != ExitError || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/session", "/text"}) {
		t.Errorf("%d %+v", code, r)
	}
	r, code = runSend(t, "", "Bad Selector", "--text", "a\x7fb")
	if code != ExitError || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/session", "/text"}) {
		t.Errorf("%d %+v", code, r)
	}
}

func TestSendHelp(t *testing.T) {
	var out bytes.Buffer
	env := Env{Stdout: &out, Stderr: &bytes.Buffer{}}
	o, code, _ := execute(commands, []string{"send", "--help"}, env)
	if code != ExitOK || !strings.Contains(string(o), "sesshin send <session> [flags]") {
		t.Errorf("%d: %s", code, o)
	}
}
