package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/ops"
)

func focusCommand(t *testing.T) []Command {
	return echoCommand(t, "focus", ops.DecodeFocusInput, func(in ops.FocusInput) map[string]any {
		return map[string]any{"session": in.Selector.Raw}
	})
}

func runFocus(t *testing.T, stdin string, args ...string) (argResult, int) {
	t.Helper()
	return runEcho(t, focusCommand(t), "focus", stdin, args)
}

func TestFocusArguments(t *testing.T) {
	for _, arg := range []string{"api", "12", "job:api"} {
		r, code := runFocus(t, "", arg)
		if code != ExitOK || !r.OK || !reflect.DeepEqual(r.Result, map[string]any{"session": arg}) {
			t.Errorf("%s: %d %+v", arg, code, r)
		}
	}
	r, code := runFocus(t, `{"session":"job:api"}`, "-i", "-")
	if code != ExitOK || !reflect.DeepEqual(r.Result, map[string]any{"session": "job:api"}) {
		t.Errorf("input: %d %+v", code, r)
	}
	r, code = runFocus(t, "", "Bad Selector")
	if code != ExitError || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), []string{"/session"}) {
		t.Errorf("%d %+v", code, r)
	}
}

func TestFocusUsage(t *testing.T) {
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"no session":        {"", nil},
		"a second session":  {"", []string{"api", "web"}},
		"an option":         {"", []string{"api", "--force"}},
		"input and session": {`{"session":"1"}`, []string{"-i", "-", "1"}},
	} {
		r, code := runFocus(t, tc.stdin, tc.args...)
		if code != ExitUsage || r.OK || r.Error.Kind != "usage" {
			t.Errorf("%s: %d %+v", name, code, r)
		}
	}
}

func TestFocusHelp(t *testing.T) {
	var out bytes.Buffer
	env := Env{Stdout: &out, Stderr: &bytes.Buffer{}}
	o, code, _ := execute(commands, []string{"focus", "--help"}, env)
	if code != ExitOK || !strings.Contains(string(o), "sesshin focus <session>") {
		t.Errorf("%d: %s", code, o)
	}
}
