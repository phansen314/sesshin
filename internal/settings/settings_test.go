package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

const hookBin = "/home/me/go/bin/sesshin-hook"

// ours is install's and uninstall's predicate: this sesshin's sesshin-hook, or
// any path whose base name is sesshin-hook.
func ours(p string) bool { return p == hookBin || SesshinHookName(p) }

// wiredHooks is every registration of hooks-spec.md's table, written out by
// hand as the install of hookBin writes it.
const wiredHooks = `{
"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook session-start","timeout":10}]}],
"UserPromptSubmit":[
 {"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook user-prompt","timeout":10,"async":true}]},
 {"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook terminal-sync","timeout":10,"async":true}]}],
"PostToolUse":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook post-tool-use","timeout":10,"async":true}]}],
"PostToolUseFailure":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook post-tool-use","timeout":10,"async":true}]}],
"Stop":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook stop","timeout":10,"async":true}]}],
"StopFailure":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook stop","timeout":10,"async":true}]}],
"Notification":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook notification","timeout":10,"async":true}]}],
"PreCompact":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook compact","timeout":10,"async":true}]}],
"PostCompact":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook compact","timeout":10,"async":true}]}],
"CwdChanged":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook cwd-changed","timeout":10,"async":true}]}],
"SessionEnd":[{"matcher":"","hooks":[{"type":"command","command":"/home/me/go/bin/sesshin-hook session-end","timeout":10}]}]
}`

const wiredStatusLine = `{"type":"command","command":"/home/me/go/bin/sesshin-hook statusline"}`

// wiredPermissions is the permissions object install writes into an empty
// settings.json.
const wiredPermissions = `{"allow":["Bash(sesshin:*)"],"ask":["Bash(sesshin install:*)","Bash(sesshin uninstall:*)","Bash(sesshin prune:*)","Bash(sesshin resume:*)"]}`

// wired is a whole settings.json that wires sesshin, with the given extra
// top-level members (leading comma included) before hooks.
func wired(before string) string {
	return `{` + before + `"hooks":` + wiredHooks + `,"statusLine":` + wiredStatusLine + `,"permissions":` + wiredPermissions + `}`
}

// registeredWhats are the registration table's items, in order.
func registeredWhats() []string {
	var out []string
	for _, r := range Registrations() {
		out = append(out, what(r.Event, r.Verb))
	}
	return out
}

func compact(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		t.Fatalf("compact %q: %v", s, err)
	}
	return b.String()
}

func TestRegistrations(t *testing.T) {
	regs := Registrations()
	if len(regs) != 12 {
		t.Fatalf("%d registrations, the table has 12", len(regs))
	}
	seen := map[string]bool{}
	for _, r := range regs {
		k := r.Event + ":" + r.Verb
		if seen[k] {
			t.Errorf("%s twice", k)
		}
		seen[k] = true
		wantAsync := r.Event != "SessionStart" && r.Event != "SessionEnd"
		if r.Async != wantAsync {
			t.Errorf("%s: async %v", k, r.Async)
		}
	}
	regs[0].Verb = "changed"
	if Registrations()[0].Verb != "session-start" {
		t.Error("Registrations returns its own storage")
	}
}

func TestCorruptErrorText(t *testing.T) {
	e := &CorruptError{Detail: "x"}
	if e.Error() != "x" {
		t.Error(e.Error())
	}
	e.Path = "/p"
	if e.Error() != "/p: x" {
		t.Error(e.Error())
	}
}

func nested(n int) string {
	return strings.Repeat(`{"a":`, n-1) + `{}` + strings.Repeat(`}`, n-1)
}

func nestedArrays(n int) string {
	return `{"a":` + strings.Repeat(`[`, n-1) + strings.Repeat(`]`, n-1) + `}`
}

func TestParseCorrupt(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		detail string
	}{
		{"empty", ``, "empty"},
		{"blank", " \n", "empty"},
		{"invalid UTF-8", "{\"a\":\"\xff\"}", "not valid UTF-8"},
		{"BOM", "\xEF\xBB\xBF{}", "byte-order mark"},
		{"syntax", `{"a":}`, "not valid JSON"},
		{"truncated", `{"a":1`, "not valid JSON"},
		{"trailing", `{} {}`, "unexpected data"},
		{"trailing junk", `{}x`, "unexpected data"},
		{"lone surrogate", `{"a":"\ud83d"}`, "unpaired surrogate"},
		{"array", `[]`, "expected a JSON object"},
		{"string", `"x"`, "expected a JSON object"},
		{"null", `null`, "expected a JSON object"},
		{"depth 65", nested(65), "nested deeper than 64"},
		{"depth 65 arrays", nestedArrays(65), "nested deeper than 64"},
		{"depth 10000", nested(10000), "nested deeper than"},

		{"hooks null", `{"hooks":null}`, "hooks is not an object"},
		{"hooks array", `{"hooks":[]}`, "hooks is not an object"},
		{"hooks string", `{"hooks":"x"}`, "hooks is not an object"},
		{"event object", `{"hooks":{"Stop":{}}}`, "hooks.Stop is not an array"},
		{"event null", `{"hooks":{"Stop":[],"Other":null}}`, "hooks.Other is not an array"},
		{"group string", `{"hooks":{"Stop":[{"hooks":[]},"x"]}}`, "hooks.Stop[1] is not an object"},
		{"group null", `{"hooks":{"Stop":[null]}}`, "hooks.Stop[0] is not an object"},
		{"group array", `{"hooks":{"Stop":[[]]}}`, "hooks.Stop[0] is not an object"},
		{"group hooks object", `{"hooks":{"Stop":[{},{},{"hooks":{}}]}}`, "hooks.Stop[2].hooks is not an array"},
		{"group hooks null", `{"hooks":{"Stop":[{"hooks":null}]}}`, "hooks.Stop[0].hooks is not an array"},
		{"statusLine string", `{"statusLine":"x"}`, "statusLine is not an object"},
		{"statusLine null", `{"statusLine":null}`, "statusLine is not an object"},
		{"statusLine array", `{"statusLine":[]}`, "statusLine is not an object"},
		{"permissions null", `{"permissions":null}`, "permissions is not an object"},
		{"permissions array", `{"permissions":[]}`, "permissions is not an object"},
		{"permissions string", `{"permissions":"x"}`, "permissions is not an object"},
		{"allow object", `{"permissions":{"allow":{}}}`, "permissions.allow is not an array"},
		{"allow null", `{"permissions":{"allow":null}}`, "permissions.allow is not an array"},
		{"allow string", `{"permissions":{"deny":[],"allow":"Bash(sesshin:*)"}}`, "permissions.allow is not an array"},
		{"ask object", `{"permissions":{"allow":[],"ask":{}}}`, "permissions.ask is not an array"},
		{"ask string", `{"permissions":{"ask":"x"}}`, "permissions.ask is not an array"},

		{"repeated hooks", `{"hooks":{},"hooks":{}}`, "repeated key hooks"},
		{"repeated statusLine", `{"statusLine":{},"statusLine":{}}`, "repeated key statusLine"},
		{"repeated statusLine command", `{"statusLine":{"command":"a","command":"b"}}`, "repeated key statusLine.command"},
		{"repeated event", `{"hooks":{"Stop":[],"Stop":[]}}`, "repeated key hooks.Stop"},
		{"repeated group hooks", `{"hooks":{"Stop":[{},{"hooks":[],"hooks":[]}]}}`, "repeated key hooks.Stop[1].hooks"},
		{"repeated matcher", `{"hooks":{"Stop":[{"matcher":"","matcher":"x"}]}}`, "repeated key hooks.Stop[0].matcher"},
		{"repeated command", `{"hooks":{"Stop":[{"hooks":[{},{"command":"a","command":"b"}]}]}}`, "repeated key hooks.Stop[0].hooks[1].command"},
		{"repeated escaped key", `{"hooks":{"a/b":[],"a/b":[]}}`, "repeated key hooks.a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, err := Parse([]byte(tt.in))
			var ce *CorruptError
			if !errors.As(err, &ce) {
				t.Fatalf("got %v, %v; want a *CorruptError", tree, err)
			}
			if !strings.Contains(ce.Detail, tt.detail) {
				t.Errorf("detail %q, want it to contain %q", ce.Detail, tt.detail)
			}
		})
	}
}

func TestParseOK(t *testing.T) {
	tests := []struct{ name, in string }{
		{"empty object", `{}`},
		{"depth 64", nested(64)},
		{"depth 64 arrays", nestedArrays(64)},
		{"repeat elsewhere", `{"env":{"a":1,"a":2},"hooks":{},"x":[{"hooks":1,"hooks":2}]}`},
		{"hooks without groups", `{"hooks":{"Stop":[]}}`},
		{"group without hooks", `{"hooks":{"Stop":[{"matcher":"x"}]}}`},
		{"hook of another shape", `{"hooks":{"Stop":[{"hooks":[1,null,"x",[],{"type":"prompt","prompt":"p"},{"command":5}]}]}}`},
		{"statusLine empty", `{"statusLine":{}}`},
		{"permissions empty", `{"permissions":{}}`},
		{"permissions without sesshin's arrays", `{"permissions":{"deny":"x","defaultMode":"plan","additionalDirectories":{}}}`},
		{"permissions arrays of any items", `{"permissions":{"allow":[1,null,{},"x"],"ask":[]}}`},
		{"surrounding space", " \n{}\n "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.in)); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestEmpty(t *testing.T) {
	p, err := ProposeInstall(Empty(), hookBin, ours)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Marshal(p.Tree)
	if compact(t, string(out)) != compact(t, wired("")) {
		t.Errorf("install into Empty:\n%s", out)
	}
	if len(Empty().Members) != 0 {
		t.Error("Empty has members")
	}
}

func TestProposeCorrupt(t *testing.T) {
	// A tree built by hand, not through Parse, is checked too.
	tree, _, _ := jsonio.ParseValue([]byte(`{"hooks":{"Stop":{}}}`))
	root := tree.(*jsonio.Object)
	if _, err := ProposeInstall(root, hookBin, ours); err == nil {
		t.Error("install accepted a corrupt tree")
	}
	if _, err := ProposeUninstall(root, ours); err == nil {
		t.Error("uninstall accepted a corrupt tree")
	}
}
