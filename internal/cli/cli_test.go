package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/schematest"
)

func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, Env{
		Stdout:    &out,
		Stderr:    &errOut,
		BuildInfo: func() buildinfo.Info { return buildinfo.Info{Version: buildinfo.Devel, Go: "go1.26.8"} },
	})
	return out.String(), errOut.String(), code
}

func TestVersion(t *testing.T) {
	out, errOut, code := run(t, "version")
	want := `{"ok":true,"result":{"version":"(devel)","commit":null,"modified":false,"go":"go1.26.8","formats":{"state":2,"lifecycle":1,"statusline":1,"sesshin":2,"reservation":1,"install":1},"migration":1},"warnings":[]}` + "\n"
	if code != ExitOK || out != want || errOut != "" {
		t.Errorf("code %d\nstdout %q\nstderr %q", code, out, errOut)
	}
	checkLine(t, out, "version-output")
}

func TestUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		argument any // nil when the problem names no token
	}{
		{nil, nil},
		{[]string{"nope"}, "nope"},
		{[]string{"version", "extra"}, "extra"},
		{[]string{"version", "--bogus"}, "--bogus"},
		{[]string{"version", "-x"}, "-x"},
		{[]string{"--bogus"}, "--bogus"},
		{[]string{"completion"}, "completion"},
		{[]string{"help"}, "help"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, errOut, code := run(t, tc.args...)
			if code != ExitUsage {
				t.Fatalf("code %d, stdout %q", code, out)
			}
			if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
				t.Errorf("stdout is not one line: %q", out)
			}
			checkLine(t, out, "")
			var env struct {
				OK    bool
				Error struct {
					Kind    string
					Details struct {
						Problems []map[string]any
					}
				}
				Warnings []any
			}
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatal(err)
			}
			if env.OK || env.Error.Kind != "usage" || env.Warnings == nil || len(env.Error.Details.Problems) != 1 {
				t.Fatalf("envelope %s", out)
			}
			if got := env.Error.Details.Problems[0]["argument"]; got != tc.argument {
				t.Errorf("argument %v, want %v", got, tc.argument)
			}
			if !strings.HasPrefix(errOut, "sesshin: usage: ") || strings.Count(errOut, "\n") != 1 {
				t.Errorf("stderr %q", errOut)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"version", "--help"}} {
		out, errOut, code := run(t, args...)
		if code != ExitOK || !strings.Contains(out, "Usage:") || errOut != "" {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestNotDelivered(t *testing.T) {
	var errOut bytes.Buffer
	code := Run([]string{"version"}, Env{Stdout: failWriter{}, Stderr: &errOut, BuildInfo: buildinfo.Read})
	if code != ExitNotDelivered || errOut.String() != "sesshin: result not delivered: broken pipe\n" {
		t.Errorf("code %d, stderr %q", code, errOut.String())
	}
}

// checkLine validates an envelope line the CLI printed against envelope, and
// its result against output (an operation's output schema), or its error
// against error and, for a usage error, its details against usage-details;
// and each warning against warning (implementation-spec.md, Schemas in
// tests).
func checkLine(t *testing.T, line, output string) {
	t.Helper()
	check := func(id string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if ok, f := schematest.Check(t, id, b); !ok {
			t.Errorf("%s rejects %s at %s", id, b, f)
		}
	}
	var env struct {
		OK       bool              `json:"ok"`
		Result   json.RawMessage   `json:"result"`
		Error    map[string]any    `json:"error"`
		Warnings []json.RawMessage `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatal(err)
	}
	if ok, f := schematest.Check(t, "envelope", []byte(line)); !ok {
		t.Errorf("envelope rejects %s at %s", line, f)
	}
	switch {
	case env.OK:
		if ok, f := schematest.Check(t, output, env.Result); !ok {
			t.Errorf("%s rejects %s at %s", output, env.Result, f)
		}
	default:
		check("error", env.Error)
		if env.Error["kind"] == "usage" {
			check("usage-details", env.Error["details"])
		}
	}
	for _, w := range env.Warnings {
		if ok, f := schematest.Check(t, "warning", w); !ok {
			t.Errorf("warning rejects %s at %s", w, f)
		}
	}
}

// echoCommand is the real command called name, with an operation that
// answers with what echo makes of the input it decoded.
func echoCommand[In any](t *testing.T, name string, decode func(*model.Fields, *model.Problems) In, echo func(In) map[string]any) []Command {
	t.Helper()
	for _, c := range commands {
		if c.Name == name {
			c.Run = operation(decode, func(in In, _ Env) ops.Envelope { return ops.Succeeded(echo(in)) })
			return []Command{c}
		}
	}
	t.Fatalf("no %s command", name)
	return nil
}

// runEcho runs the command called name from cmds with args and stdin, and
// returns what it echoed and the exit code.
func runEcho(t *testing.T, cmds []Command, name, stdin string, args []string) (argResult, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut,
		BuildInfo: func() buildinfo.Info { return buildinfo.Info{Version: buildinfo.Devel, Go: "go1.26.8"} }}
	o, code, note := execute(cmds, append([]string{name}, args...), env)
	deliver(env, o, code, note)
	parse(t, out.String())
	var r argResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r, code
}
