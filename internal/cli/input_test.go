package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/schematest"
)

// testInput is shaped like prune-input: a boolean and a minimum-1 integer.
type testInput struct {
	DryRun     bool
	RetainDays *int64
}

func decodeTest(f *model.Fields, p *model.Problems) testInput {
	var in testInput
	if v, ok := f.Optional("dry_run"); ok {
		in.DryRun, _ = p.Bool(v, f.Ptr("dry_run"))
	}
	if v, ok := f.Optional("retain_days"); ok {
		if n, ok := p.Int(v, f.Ptr("retain_days"), 1, 1<<31-1); ok {
			in.RetainDays = &n
		}
	}
	return in
}

// testCommands has test-prune, which echoes the input it was given, and
// test-required, whose --retain-days is required.
var testCommands = []Command{
	{
		Name:    "test-prune",
		Summary: "test",
		Options: []Option{
			{Name: "dry-run", Field: "/dry_run", Type: Bool},
			{Name: "retain-days", Field: "/retain_days", Type: Int},
		},
		Run: operation(decodeTest, echo),
	},
	{
		Name:    "test-required",
		Summary: "test",
		Options: []Option{{Name: "retain-days", Field: "/retain_days", Type: Int, Required: true}},
		Run:     operation(decodeTest, echo),
	},
}

func echo(in testInput, _ Env) ops.Envelope {
	return ops.Succeeded(map[string]any{"dry_run": in.DryRun, "retain_days": in.RetainDays})
}

func runTest(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{
		Stdin:     strings.NewReader(stdin),
		Stdout:    &out,
		Stderr:    &errOut,
		BuildInfo: func() buildinfo.Info { return buildinfo.Info{Version: buildinfo.Devel, Go: "go1.26.8"} },
	}
	o, code, note := execute(testCommands, args, env)
	code = deliver(env, o, code, note)
	return out.String(), errOut.String(), code
}

type parsed struct {
	OK     bool
	Result struct {
		DryRun     bool `json:"dry_run"`
		RetainDays *int `json:"retain_days"`
	}
	Error struct {
		Kind    string
		Message string
		Details struct {
			Problems []struct {
				Field    string
				Reason   string
				Argument *string
			}
			Path string
			Code string
		}
	}
}

func parse(t *testing.T, out string) parsed {
	t.Helper()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("stdout is not one line: %q", out)
	}
	if ok, f := schematest.Check(t, "envelope", []byte(out)); !ok {
		t.Errorf("envelope rejects %s at %s", out, f)
	}
	var p parsed
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatal(err)
	}
	if !p.OK {
		var env struct{ Error map[string]any }
		_ = json.Unmarshal([]byte(out), &env)
		for _, id := range []string{"error"} {
			b, _ := json.Marshal(env.Error)
			if ok, f := schematest.Check(t, id, b); !ok {
				t.Errorf("%s rejects %s at %s", id, b, f)
			}
		}
		if p.Error.Kind == "usage" {
			b, _ := json.Marshal(env.Error["details"])
			if ok, f := schematest.Check(t, "usage-details", b); !ok {
				t.Errorf("usage-details rejects %s at %s", b, f)
			}
		}
	}
	return p
}

func wantInvalid(t *testing.T, p parsed, field, reason string) {
	t.Helper()
	ps := p.Error.Details.Problems
	if p.OK || p.Error.Kind != "invalid-input" || len(ps) != 1 || ps[0].Field != field || ps[0].Reason != reason {
		t.Errorf("got %+v, want invalid-input at %q: %s", p, field, reason)
	}
}

func TestBooleans(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--dry-run"}, true},
		{[]string{"--dry-run=true"}, true},
		{[]string{"--dry-run=false"}, false},
		{[]string{"--dry-run=true", "--dry-run=false"}, false},
	} {
		out, errOut, code := runTest(t, "", append([]string{"test-prune"}, tc.args...)...)
		p := parse(t, out)
		if code != ExitOK || !p.OK || p.Result.DryRun != tc.want || errOut != "" {
			t.Errorf("%v: code %d, stdout %q, stderr %q", tc.args, code, out, errOut)
		}
	}
}

func TestBooleanNeverTakesTheNextToken(t *testing.T) {
	out, _, code := runTest(t, "", "test-prune", "--dry-run", "false")
	p := parse(t, out)
	if code != ExitUsage || p.Error.Kind != "usage" || *p.Error.Details.Problems[0].Argument != "false" {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestBadBooleanIsUsage(t *testing.T) {
	out, errOut, code := runTest(t, "", "test-prune", "--dry-run=maybe")
	p := parse(t, out)
	if code != ExitUsage || p.Error.Kind != "usage" || *p.Error.Details.Problems[0].Argument != "--dry-run" {
		t.Errorf("code %d, stdout %q", code, out)
	}
	if !strings.HasPrefix(errOut, "sesshin: usage: ") {
		t.Errorf("stderr %q", errOut)
	}
}

func TestIntegers(t *testing.T) {
	for _, tc := range []struct {
		value  string
		want   int
		field  string // "" when valid
		reason string
	}{
		{"7", 7, "", ""},
		{"-0", 0, "/retain_days", "must be between 1 and 2147483647"},
		{"0", 0, "/retain_days", "must be between 1 and 2147483647"},
		{"+1", 0, "/retain_days", "expected an integer"},
		{"01", 0, "/retain_days", "expected an integer"},
		{"1.0", 0, "/retain_days", "expected an integer"},
		{"abc", 0, "/retain_days", "expected an integer"},
		{"", 0, "/retain_days", "expected an integer"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			out, errOut, code := runTest(t, "", "test-prune", "--retain-days", tc.value)
			p := parse(t, out)
			if tc.field == "" {
				if code != ExitOK || !p.OK || *p.Result.RetainDays != tc.want {
					t.Errorf("code %d, stdout %q", code, out)
				}
				return
			}
			wantInvalid(t, p, tc.field, tc.reason)
			if code != ExitError || !strings.HasPrefix(errOut, "sesshin: invalid-input: ") {
				t.Errorf("code %d, stderr %q", code, errOut)
			}
		})
	}
}

func TestRepeatedOptionLastWins(t *testing.T) {
	out, _, _ := runTest(t, "", "test-prune", "--retain-days", "3", "--retain-days=9")
	if p := parse(t, out); !p.OK || *p.Result.RetainDays != 9 {
		t.Errorf("stdout %q", out)
	}
}

func TestMissingOptionValue(t *testing.T) {
	out, _, code := runTest(t, "", "test-prune", "--retain-days")
	p := parse(t, out)
	if code != ExitUsage || p.Error.Kind != "usage" || *p.Error.Details.Problems[0].Argument != "--retain-days" {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestOptionTakesTheNextToken(t *testing.T) {
	out, _, _ := runTest(t, "", "test-prune", "--retain-days", "--dry-run")
	wantInvalid(t, parse(t, out), "/retain_days", "expected an integer")
}

func TestRequiredOption(t *testing.T) {
	out, errOut, code := runTest(t, "", "test-required")
	p := parse(t, out)
	if code != ExitUsage || p.Error.Kind != "usage" || p.Error.Details.Problems[0].Argument != nil ||
		p.Error.Details.Problems[0].Reason != "missing required option --retain-days" {
		t.Errorf("code %d, stdout %q", code, out)
	}
	if errOut != "sesshin: usage: missing required option --retain-days\n" {
		t.Errorf("stderr %q", errOut)
	}
	// --input satisfies it.
	out, _, code = runTest(t, `{"retain_days": 2}`, "test-required", "-i", "-")
	if p := parse(t, out); code != ExitOK || !p.OK || *p.Result.RetainDays != 2 {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestInputWithFieldOptionIsUsage(t *testing.T) {
	for _, args := range [][]string{
		{"test-prune", "-i", "-", "--dry-run"},
		{"test-prune", "--retain-days=1", "--input", "-"},
	} {
		out, _, code := runTest(t, "{}", args...)
		p := parse(t, out)
		if code != ExitUsage || p.Error.Kind != "usage" || !strings.Contains(p.Error.Details.Problems[0].Reason, "--input cannot be combined") {
			t.Errorf("%v: code %d, stdout %q", args, code, out)
		}
	}
	// An argument is unexpected, whether or not --input is given.
	out, _, code := runTest(t, "{}", "test-prune", "-i", "-", "x")
	if p := parse(t, out); code != ExitUsage || *p.Error.Details.Problems[0].Argument != "x" {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestCommandNameAfterDashDash(t *testing.T) {
	for _, args := range [][]string{{"--", "test-prune"}, {"--", "nope"}} {
		out, _, code := runTest(t, "", args...)
		if p := parse(t, out); code != ExitUsage || p.Error.Kind != "usage" || *p.Error.Details.Problems[0].Argument != args[1] {
			t.Errorf("%v: code %d, stdout %q", args, code, out)
		}
	}
	out, _, code := runTest(t, "", "test-prune", "--", "x")
	if p := parse(t, out); code != ExitUsage || *p.Error.Details.Problems[0].Argument != "x" {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestInputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(path, []byte(`{"dry_run": true, "retain_days": 4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runTest(t, "", "test-prune", "--input", path)
	if p := parse(t, out); code != ExitOK || !p.Result.DryRun || *p.Result.RetainDays != 4 || errOut != "" {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestInputStdin(t *testing.T) {
	out, _, code := runTest(t, `{"retain_days": 5}`, "test-prune", "-i", "-")
	if p := parse(t, out); code != ExitOK || *p.Result.RetainDays != 5 {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestInputUnreadable(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked.json")
	if err := os.WriteFile(locked, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ path, code string }{
		{filepath.Join(dir, "missing.json"), "ENOENT"},
		{dir, "EISDIR"},
	}
	if f, err := os.Open(locked); err != nil { // not root: the mode applies
		cases = append(cases, struct{ path, code string }{locked, "EACCES"})
	} else {
		f.Close()
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			out, errOut, code := runTest(t, "", "test-prune", "-i", tc.path)
			p := parse(t, out)
			if code != ExitError || p.Error.Kind != "io" || p.Error.Details.Path != tc.path || p.Error.Details.Code != tc.code {
				t.Errorf("code %d, stdout %q", code, out)
			}
			if want := "sesshin: io: " + tc.path + ": " + tc.code + "\n"; errOut != want {
				t.Errorf("stderr %q, want %q", errOut, want)
			}
		})
	}
}

func TestInputInvalidJSON(t *testing.T) {
	for _, tc := range []struct {
		name, in, field, reason string
	}{
		{"empty", ``, "", "empty"},
		{"syntax", `{`, "", ""},
		{"not an object", `[]`, "", "expected a JSON object"},
		{"trailing data", `{} {}`, "", "unexpected data after the JSON value"},
		{"bom", "\xef\xbb\xbf{}", "", "starts with a byte-order mark"},
		{"invalid utf-8", "{\"x\": \"\xff\"}", "", "not valid UTF-8"},
		{"repeated key", `{"dry_run": true, "dry_run": false}`, "/dry_run", "repeated key"},
		{"unknown field", `{"DRY_RUN": true}`, "/DRY_RUN", "unknown field"},
		{"wrong type", `{"dry_run": 1}`, "/dry_run", "expected a boolean"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := runTest(t, tc.in, "test-prune", "-i", "-")
			p := parse(t, out)
			if code != ExitError || p.OK || p.Error.Kind != "invalid-input" || len(p.Error.Details.Problems) != 1 {
				t.Fatalf("code %d, stdout %q", code, out)
			}
			got := p.Error.Details.Problems[0]
			if got.Field != tc.field || (tc.reason != "" && got.Reason != tc.reason) {
				t.Errorf("problem %+v", got)
			}
			if !strings.HasPrefix(errOut, "sesshin: invalid-input: ") {
				t.Errorf("stderr %q", errOut)
			}
		})
	}
}

func TestInputCollectsEveryProblem(t *testing.T) {
	out, _, _ := runTest(t, `{"dry_run": 1, "retain_days": 0, "x": 1}`, "test-prune", "-i", "-")
	p := parse(t, out)
	if p.Error.Kind != "invalid-input" || len(p.Error.Details.Problems) != 3 {
		t.Errorf("stdout %q", out)
	}
}

func TestVersionInput(t *testing.T) {
	out, _, code := run(t, "version", "-i", "/dev/null")
	if code != ExitError {
		t.Errorf("empty file: code %d, stdout %q", code, out)
	}
	wantInvalid(t, parse(t, out), "", "empty")

	path := filepath.Join(t.TempDir(), "in.json")
	for _, tc := range []struct {
		in    string
		field string // "" when accepted
	}{{`{}`, ""}, {` {} `, ""}, {`{"x":1}`, "/x"}} {
		if err := os.WriteFile(path, []byte(tc.in), 0o600); err != nil {
			t.Fatal(err)
		}
		out, _, code := run(t, "version", "--input", path)
		checkLine(t, out, "version-output")
		switch {
		case tc.field == "" && code != ExitOK:
			t.Errorf("%s: code %d, stdout %q", tc.in, code, out)
		case tc.field != "":
			wantInvalid(t, parse(t, out), tc.field, "unknown field")
		}
	}
}

func TestVersionStdin(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"version", "-i", "-"}, Env{
		Stdin:     strings.NewReader("{}"),
		Stdout:    &out,
		Stderr:    &errOut,
		BuildInfo: func() buildinfo.Info { return buildinfo.Info{Version: buildinfo.Devel, Go: "go1.26.8"} },
	})
	if code != ExitOK {
		t.Errorf("code %d, stdout %q", code, out.String())
	}
	checkLine(t, out.String(), "version-output")
}

// Stdin is read only for --input -.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Error("stdin was read")
	return 0, nil
}

func TestStdinNotReadUnlessNamed(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"version"}, Env{Stdin: failReader{t}, Stdout: &out, Stderr: &errOut, BuildInfo: buildinfo.Read})
	if code != ExitOK {
		t.Errorf("code %d", code)
	}
}

// One stderr line per exit code (cli-spec.md, Output): none for a clean 0,
// the count for warnings, the kind and message for failures.
func TestStderrLines(t *testing.T) {
	w := ops.Warning{Kind: "w", Details: map[string]any{}}
	for _, tc := range []struct {
		name string
		env  ops.Envelope
		code int
		note string
	}{
		{"ok", ops.Succeeded(1), ExitOK, ""},
		{"one warning", ops.Envelope{OK: true, Result: 1, Warnings: []ops.Warning{w}}, ExitOK, "sesshin: 1 warning (see .warnings in the output)"},
		{"two warnings", ops.Envelope{OK: true, Result: 1, Warnings: []ops.Warning{w, w}}, ExitOK, "sesshin: 2 warnings (see .warnings in the output)"},
		{"error", ops.Failed(&ops.Error{Kind: "io", Message: "a\nb"}), ExitError, `sesshin: io: a\u000ab`},
		{"usage", ops.Failed(&ops.Error{Kind: "usage", Message: "m"}), ExitUsage, "sesshin: usage: m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, code, note := envelopeLine(tc.env)
			if code != tc.code || note != tc.note {
				t.Errorf("code %d, note %q", code, note)
			}
		})
	}
}
