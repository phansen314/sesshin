package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/ops"
)

// argCommands run list's and show's real decoders and echo what they made of
// the command line, so the tests see the input the operation would get.
var argCommands = []Command{
	{
		Name:    "test-list",
		Summary: "test",
		Options: []Option{
			{Name: "liveness", Field: "/liveness", Type: String},
			{Name: "include-headless", Field: "/include_headless", Type: Bool},
			{Name: "fields", Field: "/fields", Type: List},
			{Name: "limit", Field: "/limit", Type: Int},
		},
		Run: operation(ops.DecodeListInput, func(in ops.ListInput, _ Env) ops.Envelope {
			var fields any // nil: every field
			if in.Fields != nil {
				fields = in.Fields
			}
			return ops.Succeeded(map[string]any{"liveness": in.Liveness, "fields": fields, "limit": in.Limit, "headless": in.IncludeHeadless})
		}),
	},
	{
		Name:      "test-show",
		Summary:   "test",
		Arguments: []Argument{{Name: "session", Field: "/session"}},
		Options:   []Option{{Name: "include-payload", Field: "/include_payload", Type: Bool}},
		Run: operation(ops.DecodeShowInput, func(in ops.ShowInput, _ Env) ops.Envelope {
			return ops.Succeeded(map[string]any{"raw": in.Selector.Raw, "id": in.Selector.ID, "prefix": in.Selector.Prefix, "payload": in.IncludePayload})
		}),
	},
}

type argResult struct {
	OK     bool
	Result map[string]any
	Error  struct {
		Kind    string
		Message string
		Details struct {
			Problems []struct {
				Field    string
				Reason   string
				Argument *string
			}
		}
	}
}

func runArgs(t *testing.T, stdin string, args ...string) argResult {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{
		Stdin:     strings.NewReader(stdin),
		Stdout:    &out,
		Stderr:    &errOut,
		BuildInfo: func() buildinfo.Info { return buildinfo.Info{Version: buildinfo.Devel, Go: "go1.26.8"} },
	}
	o, code, note := execute(argCommands, args, env)
	deliver(env, o, code, note)
	p := parse(t, out.String())
	var r argResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	_ = p
	return r
}

func TestPositionalArgument(t *testing.T) {
	r := runArgs(t, "", "test-show", "12")
	if !r.OK || r.Result["raw"] != "12" || r.Result["id"] != float64(12) || r.Result["payload"] != false {
		t.Errorf("%+v", r)
	}
	r = runArgs(t, "", "test-show", "--include-payload", "0B6C5A3E")
	if !r.OK || r.Result["prefix"] != "0b6c5a3e" || r.Result["payload"] != true {
		t.Errorf("%+v", r)
	}
	// A lone - is an ordinary argument, and -- ends options: both reach the
	// operation, which judges them.
	r = runArgs(t, "", "test-show", "--", "-")
	if r.OK || r.Error.Kind != "invalid-input" || r.Error.Details.Problems[0].Field != "/session" {
		t.Errorf("%+v", r)
	}
	// Judged by the operation, not the parser.
	for _, tok := range []string{"012", "#12", "9007199254740992", "Abc", "job:12"} {
		r = runArgs(t, "", "test-show", tok)
		if r.OK || r.Error.Kind != "invalid-input" || r.Error.Details.Problems[0].Field != "/session" {
			t.Errorf("%q: %+v", tok, r)
		}
	}
}

func TestPositionalArgumentUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		arg  string // the problem's argument, "" for none
	}{
		{"missing", []string{"test-show"}, ""},
		{"missing with an option", []string{"test-show", "--include-payload"}, ""},
		{"extra", []string{"test-show", "12", "13"}, "13"},
		{"extra after --", []string{"test-show", "12", "--", "x"}, "x"},
		{"input with an argument", []string{"test-show", "12", "-i", "-"}, "12"},
		{"extra on a command with none", []string{"test-list", "x"}, "x"},
	} {
		r := runArgs(t, `{"session":"12"}`, tc.args...)
		if r.OK || r.Error.Kind != "usage" {
			t.Errorf("%s: %+v", tc.name, r)
			continue
		}
		p := r.Error.Details.Problems[0]
		if tc.arg == "" && p.Argument != nil || tc.arg != "" && (p.Argument == nil || *p.Argument != tc.arg) {
			t.Errorf("%s: %+v", tc.name, r.Error)
		}
	}
}

func TestInputInPlaceOfArgument(t *testing.T) {
	r := runArgs(t, `{"session":"0b6c5a3e","include_payload":true}`, "test-show", "-i", "-")
	if !r.OK || r.Result["prefix"] != "0b6c5a3e" || r.Result["payload"] != true {
		t.Errorf("%+v", r)
	}
	r = runArgs(t, `{}`, "test-show", "-i", "-")
	if r.OK || r.Error.Kind != "invalid-input" || r.Error.Details.Problems[0].Field != "/session" {
		t.Errorf("%+v", r)
	}
}

func TestListOption(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []any
	}{
		{[]string{"--fields", "id,name,status"}, []any{"id", "name", "status"}},
		{[]string{"--fields=name"}, []any{"name"}},
		{[]string{"--fields", "name", "--fields", "status,cwd"}, []any{"name", "status", "cwd"}},
		{[]string{"--fields", ""}, []any{}},
		{[]string{"--fields", "", "--fields", "name"}, []any{"name"}},
		{[]string{"--fields", `"name"`}, nil}, // not parsed as CSV: a quote is part of the item
	} {
		r := runArgs(t, "", append([]string{"test-list"}, tc.args...)...)
		if tc.want == nil {
			if r.OK || r.Error.Details.Problems[0].Field != "/fields/0" {
				t.Errorf("%v: %+v", tc.args, r)
			}
			continue
		}
		got, _ := r.Result["fields"].([]any)
		if !r.OK || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: fields %#v, want %#v (%+v)", tc.args, r.Result["fields"], tc.want, r)
		}
	}
}

func TestListOptionAbsentAndBad(t *testing.T) {
	r := runArgs(t, "", "test-list")
	if !r.OK || r.Result["fields"] != nil || r.Result["limit"] != nil {
		t.Errorf("%+v", r)
	}
	r = runArgs(t, "", "test-list", "--liveness", "ended", "--limit", "0", "--include-headless")
	if !r.OK || r.Result["liveness"] != "ended" || r.Result["limit"] != float64(0) || r.Result["headless"] != true {
		t.Errorf("%+v", r)
	}
	// An empty item beside others, and a repeated or unknown field, are
	// invalid-input at the item.
	for _, tc := range []struct {
		args  []string
		field string
	}{
		{[]string{"--fields", "name,,status"}, "/fields/1"},
		{[]string{"--fields", "name,name"}, "/fields/1"},
		{[]string{"--fields", "nope"}, "/fields/0"},
		{[]string{"--liveness", "dead"}, "/liveness"},
		{[]string{"--limit", "-1"}, "/limit"},
		{[]string{"--limit", "x"}, "/limit"},
	} {
		r := runArgs(t, "", append([]string{"test-list"}, tc.args...)...)
		if r.OK || r.Error.Kind != "invalid-input" || r.Error.Details.Problems[0].Field != tc.field {
			t.Errorf("%v: %+v", tc.args, r)
		}
	}
	r = runArgs(t, `{}`, "test-list", "--fields", "name", "-i", "-")
	if r.OK || r.Error.Kind != "usage" {
		t.Errorf("%+v", r)
	}
}
