package settings

import (
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// hg is the group sesshin writes for a verb, written out by hand.
func hg(verb string, async bool) string {
	a := ""
	if async {
		a = `,"async":true`
	}
	return `{"matcher":"","hooks":[{"type":"command","command":"` + hookBin + ` ` + verb + `","timeout":10` + a + `}]}`
}

// hook is a hook object of another tool.
const other = `{"type":"command","command":"other-tool run"}`

func otherGroup(matcher string) string {
	return `{"matcher":"` + matcher + `","hooks":[` + other + `]}`
}

// rest writes the members of a hooks object for every registered event but
// the skipped ones, in the table's order, as install writes them.
func rest(skip ...string) string {
	var events []string
	groups := map[string][]string{}
	for _, r := range Registrations() {
		if _, ok := groups[r.Event]; !ok {
			events = append(events, r.Event)
		}
		groups[r.Event] = append(groups[r.Event], hg(r.Verb, r.Async))
	}
	var out []string
	for _, e := range events {
		if !slices.Contains(skip, e) {
			out = append(out, `"`+e+`":[`+strings.Join(groups[e], ",")+`]`)
		}
	}
	return strings.Join(out, ",")
}

// ev writes one event's member.
func ev(event string, groups ...string) string {
	return `"` + event + `":[` + strings.Join(groups, ",") + `]`
}

// changes writes a change list: every registered item as base, except the
// overrides (keyed by what), then the tail items ("action what"), then every
// permission rule as base, except the overrides.
func changes(base Action, overrides map[string]Action, tail ...string) []string {
	var out []string
	for _, w := range registeredWhats() {
		a := base
		if o, ok := overrides[w]; ok {
			a = o
		}
		out = append(out, string(a)+" "+w)
	}
	out = append(out, tail...)
	for _, r := range PermissionRules() {
		w := permissionWhat(r)
		a := base
		if o, ok := overrides[w]; ok {
			a = o
		}
		out = append(out, string(a)+" "+w)
	}
	return out
}

// permsMember is the permissions member install appends to a settings.json
// that has none, leading comma included.
const permsMember = `,"permissions":` + wiredPermissions

// permsUnchanged and permsAdded are overrides of changes for every rule.
func permsUnchanged() map[string]Action { return permsAs(Unchanged) }
func permsAdded() map[string]Action     { return permsAs(Added) }

func permsAs(a Action) map[string]Action {
	out := map[string]Action{}
	for _, r := range PermissionRules() {
		out[permissionWhat(r)] = a
	}
	return out
}

func (p Proposal) list() []string {
	var out []string
	for _, c := range p.Changes {
		out = append(out, string(c.Action)+" "+c.What)
	}
	return out
}

func parse(t *testing.T, s string) *jsonio.Object {
	t.Helper()
	tree, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse(%s): %v", s, err)
	}
	return tree
}

func marshal(t *testing.T, tree *jsonio.Object) string {
	t.Helper()
	b, err := Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkProposal compares a proposal to the expected JSON (whitespace aside)
// and changes, and that the input tree was not changed.
func checkProposal(t *testing.T, tree *jsonio.Object, p Proposal, wantOut string, wantChanges []string) {
	t.Helper()
	if got := compact(t, marshal(t, p.Tree)); got != compact(t, wantOut) {
		t.Errorf("proposal:\n got %s\nwant %s", got, compact(t, wantOut))
	}
	if got := p.list(); !sameList(got, wantChanges) {
		t.Errorf("changes:\n got %q\nwant %q", got, wantChanges)
	}
	_ = tree
}

func install(t *testing.T, in string) (*jsonio.Object, Proposal) {
	t.Helper()
	tree := parse(t, in)
	before := marshal(t, tree)
	p, err := ProposeInstall(tree, hookBin, ours)
	if err != nil {
		t.Fatal(err)
	}
	if marshal(t, tree) != before {
		t.Error("ProposeInstall changed its input")
	}
	return tree, p
}

func uninstall(t *testing.T, in string) (*jsonio.Object, Proposal) {
	t.Helper()
	tree := parse(t, in)
	before := marshal(t, tree)
	p, err := ProposeUninstall(tree, ours)
	if err != nil {
		t.Fatal(err)
	}
	if marshal(t, tree) != before {
		t.Error("ProposeUninstall changed its input")
	}
	return tree, p
}

// converges proposes again from the proposal: every item unchanged, and the
// same bytes.
func converges(t *testing.T, p Proposal) {
	t.Helper()
	first := marshal(t, p.Tree)
	again, err := ProposeInstall(parse(t, first), hookBin, ours)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range again.Changes {
		if c.Action != Unchanged {
			t.Errorf("proposing again: %s %s\nfrom %s", c.Action, c.What, first)
		}
	}
	if len(again.Changes) != 18 {
		t.Errorf("proposing again: %d items", len(again.Changes))
	}
	if second := marshal(t, again.Tree); second != first {
		t.Errorf("proposing again changed the bytes:\n%s\n%s", first, second)
	}
	if again.StatusLineReplaced {
		t.Error("proposing again: statusLine replaced")
	}
}

func TestInstall(t *testing.T) {
	old := "/old/bin/sesshin-hook"
	stop := hg("stop", true)
	sl := `"statusLine":` + wiredStatusLine
	tests := []struct {
		name    string
		in      string
		out     string
		changes []string
		slWarn  bool
	}{
		{
			name:    "empty settings",
			in:      `{}`,
			out:     wired(""),
			changes: changes(Added, nil, "added statusLine"),
		},
		{
			name:    "other keys first",
			in:      `{"model":"opus","env":{"A":"1"}}`,
			out:     wired(`"model":"opus","env":{"A":"1"},`),
			changes: changes(Added, nil, "added statusLine"),
		},
		{
			name:    "already wired",
			in:      wired(`"model":"opus",`),
			out:     wired(`"model":"opus",`),
			changes: changes(Unchanged, nil, "unchanged statusLine"),
		},
		{
			name:    "empty hooks object keeps its place",
			in:      `{"hooks":{},"model":"x"}`,
			out:     `{"hooks":{` + rest() + `},"model":"x",` + sl + permsMember + `}`,
			changes: changes(Added, nil, "added statusLine"),
		},
		{
			name:    "stale path: every entry rewritten in place",
			in:      strings.ReplaceAll(wired(""), hookBin, old),
			out:     wired(""),
			changes: changes(Replaced, permsUnchanged(), "replaced statusLine"),
		},
		{
			name:    "a path that needs quotes",
			in:      `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"'/my dir/sesshin-hook' stop","timeout":10,"async":true}]}]}}`,
			out:     `{"hooks":{` + ev("Stop", stop) + `,` + rest("Stop") + `},` + sl + permsMember + `}`,
			changes: changes(Added, map[string]Action{"hooks.Stop:stop": Replaced}, "added statusLine"),
		},
		{
			name: "wrong timeout and async",
			in: strings.NewReplacer(
				`sesshin-hook stop","timeout":10,"async":true`, `sesshin-hook stop","timeout":5,"async":true`,
				`sesshin-hook session-end","timeout":10`, `sesshin-hook session-end","timeout":10,"async":true`,
				`sesshin-hook compact","timeout":10,"async":true`, `sesshin-hook compact","timeout":10`,
			).Replace(wired("")),
			out: wired(""),
			changes: changes(Unchanged, map[string]Action{
				"hooks.Stop:stop": Replaced, "hooks.StopFailure:stop": Replaced,
				"hooks.SessionEnd:session-end": Replaced,
				"hooks.PreCompact:compact":     Replaced, "hooks.PostCompact:compact": Replaced,
			}, "unchanged statusLine"),
		},
		{
			name: "key order and number text differ from sesshin's",
			in: `{"hooks":{
			  "Stop":[{"hooks":[{"command":"` + hookBin + ` stop","type":"command","timeout":10,"async":true}],"matcher":""}],
			  "StopFailure":[{"matcher":"","hooks":[{"type":"command","command":"` + hookBin + ` stop","timeout":10.0,"async":true}]}],
			  "SessionEnd":[{"matcher":"","hooks":[{"type":"command","command":"` + hookBin + ` session-end","timeout":1e1}]}]}}`,
			out: `{"hooks":{` + ev("Stop", stop) + `,` + ev("StopFailure", stop) + `,` + ev("SessionEnd", hg("session-end", false)) + `,` +
				rest("Stop", "StopFailure", "SessionEnd") + `},` + sl + permsMember + `}`,
			changes: changes(Added, map[string]Action{
				"hooks.Stop:stop": Replaced, "hooks.StopFailure:stop": Replaced, "hooks.SessionEnd:session-end": Replaced,
			}, "added statusLine"),
		},
		{
			name:    "sesshin's hook shared with another tool's in one group",
			in:      `{"hooks":{"Stop":[{"matcher":"","hooks":[` + other + `,{"type":"command","command":"` + hookBin + ` stop","timeout":10,"async":true}]}]}}`,
			out:     `{"hooks":{` + ev("Stop", otherGroup(""), stop) + `,` + rest("Stop") + `},` + sl + permsMember + `}`,
			changes: changes(Added, map[string]Action{"hooks.Stop:stop": Replaced}, "added statusLine"),
		},
		{
			name: "sesshin's hook under matcher Bash, alone in its group",
			in: `{"hooks":{"Stop":[{"matcher":"Bash","hooks":[{"type":"command","command":"` + hookBin + ` stop","timeout":10,"async":true}]}],
			  "PostToolUse":[` + otherGroup("Bash") + `,{"matcher":"Bash","hooks":[{"type":"command","command":"` + hookBin + ` post-tool-use","timeout":10,"async":true}]}]}}`,
			out: `{"hooks":{` + ev("Stop", stop) + `,` + ev("PostToolUse", otherGroup("Bash"), hg("post-tool-use", true)) + `,` +
				rest("Stop", "PostToolUse") + `},` + sl + permsMember + `}`,
			changes: changes(Added, map[string]Action{"hooks.Stop:stop": Replaced, "hooks.PostToolUse:post-tool-use": Replaced}, "added statusLine"),
		},
		{
			name:    "matcher absent, alone in its group: rewritten in place",
			in:      `{"hooks":{"Stop":[` + otherGroup("") + `,{"hooks":[{"type":"command","command":"` + old + ` stop"}]},` + otherGroup("X") + `]}}`,
			out:     `{"hooks":{` + ev("Stop", otherGroup(""), stop, otherGroup("X")) + `,` + rest("Stop") + `},` + sl + permsMember + `}`,
			changes: changes(Added, map[string]Action{"hooks.Stop:stop": Replaced}, "added statusLine"),
		},
		{
			name:    "extra keys in sesshin's group and hook are dropped by the rewrite",
			in:      `{"hooks":{"Stop":[{"matcher":"","if":"x","hooks":[{"type":"command","command":"` + hookBin + ` stop","timeout":10,"async":true,"statusMessage":"m"}]}]}}`,
			out:     `{"hooks":{` + ev("Stop", stop) + `,` + rest("Stop") + `},` + sl + permsMember + `}`,
			changes: changes(Added, map[string]Action{"hooks.Stop:stop": Replaced}, "added statusLine"),
		},
		{
			name: "two of sesshin's hooks in one group move out, emptying it",
			in: `{"hooks":{"UserPromptSubmit":[{"matcher":"","hooks":[
			  {"type":"command","command":"` + hookBin + ` user-prompt","timeout":10,"async":true},
			  {"type":"command","command":"` + hookBin + ` terminal-sync","timeout":10,"async":true}]}]}}`,
			out: `{"hooks":{` + ev("UserPromptSubmit", hg("user-prompt", true), hg("terminal-sync", true)) + `,` + rest("UserPromptSubmit") + `},` + sl + permsMember + `}`,
			changes: changes(Added, map[string]Action{
				"hooks.UserPromptSubmit:user-prompt": Replaced, "hooks.UserPromptSubmit:terminal-sync": Replaced,
			}, "added statusLine"),
		},
		{
			name: "duplicates: the first is kept, the rest removed",
			in: `{"hooks":{` + ev("Stop", stop, otherGroup(""), strings.ReplaceAll(stop, hookBin, old),
				`{"matcher":"","hooks":[`+other+`,{"type":"command","command":"`+hookBin+` stop","timeout":10}]}`) + `}}`,
			out: `{"hooks":{` + ev("Stop", stop, otherGroup(""), `{"matcher":"","hooks":[`+other+`]}`) + `,` + rest("Stop") + `},` + sl + permsMember + `}`,
			changes: changes(Added, map[string]Action{"hooks.Stop:stop": Unchanged},
				"removed hooks.Stop:stop", "removed hooks.Stop:stop", "added statusLine"),
		},
		{
			name: "an unregistered event, and a verb that isn't registered for its event",
			in: `{"hooks":{` +
				ev("PreToolUse", otherGroup("Bash"), hg("post-tool-use", true)) + `,` +
				ev("Elicitation", hg("notification", true)) + `,` +
				ev("Stop", stop, hg("user-prompt", true), hg("statusline", true)) + `,` +
				rest("Stop") + `},` + sl + `}`,
			out: `{"hooks":{` + ev("PreToolUse", otherGroup("Bash")) + `,` + ev("Stop", stop) + `,` + rest("Stop") + `},` + sl + permsMember + `}`,
			changes: changes(Unchanged, permsAdded(),
				"removed hooks.PreToolUse:post-tool-use", "removed hooks.Elicitation:notification",
				"removed hooks.Stop:user-prompt", "removed hooks.Stop:statusline", "unchanged statusLine"),
		},
		{
			name: "other tools' entries, odd numbers, and other shapes are untouched",
			in: `{"a":1.0,"b":[1e3,-0,0.10,1E+2,12345678901234567890],"s":"é\u00e9\u2028<>&","hooks":{
			  "Stop":[` + otherGroup("") + `,{"matcher":"","hooks":[1,null,"x",[],{"type":"prompt","prompt":"p"},{"command":5},{"type":"command","command":"$HOME/.claude/sesshin-hook.sh stop"}]}],
			  "Custom":[{"matcher":"x"},{"hooks":[]}]},"statusLine":{"type":"command","command":"` + hookBin + ` statusline","padding":0.0}}`,
			out: `{"a":1.0,"b":[1e3,-0,0.10,1E+2,12345678901234567890],"s":"éé\u2028<>&","hooks":{
			  "Stop":[` + otherGroup("") + `,{"matcher":"","hooks":[1,null,"x",[],{"type":"prompt","prompt":"p"},{"command":5},{"type":"command","command":"$HOME/.claude/sesshin-hook.sh stop"}]},` + stop + `],
			  "Custom":[{"matcher":"x"},{"hooks":[]}],` + rest("Stop") + `},
			  "statusLine":{"type":"command","command":"` + hookBin + ` statusline","padding":0.0},"permissions":` + wiredPermissions + `}`,
			changes: changes(Added, nil, "unchanged statusLine"),
		},
		{
			name:    "sesshin's statusLine, stale path: other keys kept",
			in:      `{"statusLine":{"type":"command","padding":2,"command":"` + old + ` statusline"}}`,
			out:     `{"statusLine":{"type":"command","padding":2,"command":"` + hookBin + ` statusline"},"hooks":{` + rest() + `}` + permsMember + `}`,
			changes: changes(Added, nil, "replaced statusLine"),
		},
		{
			name:    "sesshin's statusLine with padding, already right",
			in:      `{"statusLine":{"type":"command","command":"` + hookBin + ` statusline","padding":2}}`,
			out:     `{"statusLine":{"type":"command","command":"` + hookBin + ` statusline","padding":2},"hooks":{` + rest() + `}` + permsMember + `}`,
			changes: changes(Added, nil, "unchanged statusLine"),
		},
		{
			name:    "sesshin's statusLine without a type",
			in:      `{"statusLine":{"padding":2,"command":"` + hookBin + ` statusline"}}`,
			out:     `{"statusLine":{"padding":2,"command":"` + hookBin + ` statusline","type":"command"},"hooks":{` + rest() + `}` + permsMember + `}`,
			changes: changes(Added, nil, "replaced statusLine"),
		},
		{
			name:    "sesshin's statusLine with another type",
			in:      `{"statusLine":{"type":"static","command":"` + hookBin + ` statusline"}}`,
			out:     `{"statusLine":{"type":"command","command":"` + hookBin + ` statusline"},"hooks":{` + rest() + `}` + permsMember + `}`,
			changes: changes(Added, nil, "replaced statusLine"),
		},
		{
			name:    "user's statusLine: replaced wholesale, with the warning",
			in:      `{"statusLine":{"type":"command","command":"npx ccstatusline","padding":0},"model":"x"}`,
			out:     `{"statusLine":` + wiredStatusLine + `,"model":"x","hooks":{` + rest() + `}` + permsMember + `}`,
			changes: changes(Added, nil, "replaced statusLine"),
			slWarn:  true,
		},
		{
			name:    "sesshin's path with another verb is the user's statusLine",
			in:      `{"statusLine":{"type":"command","command":"` + hookBin + ` stop"}}`,
			out:     `{"statusLine":` + wiredStatusLine + `,"hooks":{` + rest() + `}` + permsMember + `}`,
			changes: changes(Added, nil, "replaced statusLine"),
			slWarn:  true,
		},
		{
			name:    "a statusLine with no command",
			in:      `{"statusLine":{}}`,
			out:     `{"statusLine":` + wiredStatusLine + `,"hooks":{` + rest() + `}` + permsMember + `}`,
			changes: changes(Added, nil, "replaced statusLine"),
			slWarn:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, p := install(t, tt.in)
			checkProposal(t, tree, p, tt.out, tt.changes)
			if p.StatusLineReplaced != tt.slWarn {
				t.Errorf("StatusLineReplaced = %v", p.StatusLineReplaced)
			}
			converges(t, p)
		})
	}
}
