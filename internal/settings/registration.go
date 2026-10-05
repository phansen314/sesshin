package settings

import (
	"encoding/json"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Registration is one row of hooks-spec.md's Registration table.
type Registration struct {
	Event string
	Verb  string
	Async bool
}

// hookTimeout is every entry's timeout, in seconds.
const hookTimeout = "10"

// Registrations returns the hooks sesshin registers, in the table's order.
func Registrations() []Registration {
	return []Registration{
		{"SessionStart", "session-start", false},
		{"UserPromptSubmit", "user-prompt", true},
		{"UserPromptSubmit", "terminal-sync", true},
		{"PostToolUse", "post-tool-use", true},
		{"PostToolUseFailure", "post-tool-use", true},
		{"Stop", "stop", true},
		{"StopFailure", "stop", true},
		{"Notification", "notification", true},
		{"PreCompact", "compact", true},
		{"PostCompact", "compact", true},
		{"CwdChanged", "cwd-changed", true},
		{"SessionEnd", "session-end", false},
	}
}

// StatusLineVerb is the verb of sesshin's statusLine command.
const StatusLineVerb = "statusline"

// command is the command sesshin writes: the quoted path, then the verb.
func command(hookBinary, verb string) string { return Quote(hookBinary) + " " + verb }

// sesshinGroup is the matcher group sesshin writes for r, keys in sesshin's order.
func sesshinGroup(hookBinary string, r Registration) *jsonio.Object {
	hook := &jsonio.Object{Members: []jsonio.Member{
		{Key: "type", Value: "command"},
		{Key: "command", Value: command(hookBinary, r.Verb)},
		{Key: "timeout", Value: json.Number(hookTimeout)},
	}}
	if r.Async {
		hook.Members = append(hook.Members, jsonio.Member{Key: "async", Value: true})
	}
	return &jsonio.Object{Members: []jsonio.Member{
		{Key: "matcher", Value: ""},
		{Key: "hooks", Value: []any{hook}},
	}}
}

// sesshinStatusLine is the statusLine object sesshin writes.
func sesshinStatusLine(hookBinary string) *jsonio.Object {
	return &jsonio.Object{Members: []jsonio.Member{
		{Key: "type", Value: "command"},
		{Key: "command", Value: command(hookBinary, StatusLineVerb)},
	}}
}

// PermissionRule is one row of hooks-spec.md's permission rules table: a rule
// and the permissions array it belongs in.
type PermissionRule struct {
	Array string // "allow" or "ask"
	Rule  string
	Ours  bool // sesshin's alone: uninstall removes it
}

// PermissionRules returns the rules install proposes, in the table's order.
// Bash(jq:*) is shared with koan, so it is not sesshin's.
func PermissionRules() []PermissionRule {
	return []PermissionRule{
		{"allow", "Bash(sesshin:*)", true},
		{"allow", "Bash(jq:*)", false},
		{"ask", "Bash(sesshin install:*)", true},
		{"ask", "Bash(sesshin uninstall:*)", true},
		{"ask", "Bash(sesshin prune:*)", true},
	}
}

// permissionWhat is a rule's item in changes.
func permissionWhat(r PermissionRule) string { return "permissions." + r.Array + ":" + r.Rule }
