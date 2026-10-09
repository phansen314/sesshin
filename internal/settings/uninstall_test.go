package settings

import (
	"strings"
	"testing"
)

func TestUninstall(t *testing.T) {
	old := "/old/bin/sesshin-hook"
	stop := hg("stop", true)
	whole := func(items ...string) []string { return items }
	var allRemoved []string
	for _, w := range registeredWhats() {
		allRemoved = append(allRemoved, "removed "+w)
	}
	// What uninstall removes from a wired file, in Order: the hooks, the
	// statusLine, then sesshin's five rules.
	wiredRemoved := append(allRemoved[:len(allRemoved):len(allRemoved)], "removed statusLine",
		"removed permissions.allow:Bash(sesshin:*)", "removed permissions.ask:Bash(sesshin install:*)",
		"removed permissions.ask:Bash(sesshin uninstall:*)", "removed permissions.ask:Bash(sesshin prune:*)",
		"removed permissions.ask:Bash(sesshin resume:*)")
	tests := []struct {
		name    string
		in      string
		out     string
		changes []string
	}{
		{
			name:    "fully wired: hooks, statusLine, and permissions go, the rest stays",
			in:      wired(`"model":"opus","n":1.0,`),
			out:     `{"model":"opus","n":1.0}`,
			changes: wiredRemoved,
		},
		{
			name:    "fully wired and nothing else",
			in:      wired(""),
			out:     `{}`,
			changes: wiredRemoved,
		},
		{
			name:    "stale paths and any sesshin-hook path count",
			in:      strings.ReplaceAll(wired(""), hookBin, old),
			out:     `{}`,
			changes: wiredRemoved,
		},
		{
			name:    "nothing of sesshin's: no items",
			in:      `{"model":"x","hooks":{"Stop":[` + otherGroup("") + `]}}`,
			out:     `{"model":"x","hooks":{"Stop":[` + otherGroup("") + `]}}`,
			changes: whole(),
		},
		{
			name:    "empty settings",
			in:      `{}`,
			out:     `{}`,
			changes: whole(),
		},
		{
			name:    "a hooks that was already empty is left alone",
			in:      `{"hooks":{},"x":1}`,
			out:     `{"hooks":{},"x":1}`,
			changes: whole(),
		},
		{
			name:    "an event that was already empty is left alone",
			in:      `{"hooks":{"Stop":[],"Other":[{"hooks":[]}]}}`,
			out:     `{"hooks":{"Stop":[],"Other":[{"hooks":[]}]}}`,
			changes: whole(),
		},
		{
			name: "emptied group goes, group with another tool's hook stays, event with a group stays",
			in: `{"hooks":{
			  "Stop":[` + otherGroup("") + `,` + stop + `],
			  "Notification":[{"matcher":"","hooks":[` + other + `,{"type":"command","command":"` + hookBin + ` notification"}]}]}}`,
			out:     `{"hooks":{"Stop":[` + otherGroup("") + `],"Notification":[` + otherGroup("") + `]}}`,
			changes: whole("removed hooks.Stop:stop", "removed hooks.Notification:notification"),
		},
		{
			name:    "emptied event goes, and hooks with it, others keep their place",
			in:      `{"a":1,"hooks":{"Stop":[` + stop + `,` + strings.ReplaceAll(stop, hookBin, old) + `],"SessionEnd":[` + hg("session-end", false) + `]},"b":2}`,
			out:     `{"a":1,"b":2}`,
			changes: whole("removed hooks.Stop:stop", "removed hooks.Stop:stop", "removed hooks.SessionEnd:session-end"),
		},
		{
			name:    "hooks stays when an event is left",
			in:      `{"hooks":{"Custom":[` + otherGroup("x") + `],"Stop":[` + stop + `]}}`,
			out:     `{"hooks":{"Custom":[` + otherGroup("x") + `]}}`,
			changes: whole("removed hooks.Stop:stop"),
		},
		{
			name: "entries under unregistered events and wrong verbs go, in settings.json order",
			in: `{"hooks":{
			  "Zed":[` + hg("whatever", true) + `],
			  "Stop":[` + hg("user-prompt", true) + `,` + hg("statusline", true) + `,` + stop + `],
			  "Alpha":[{"matcher":"","hooks":[{"type":"command","command":"/x/sesshin-hook"}]}]}}`,
			out:     `{"hooks":{"Alpha":[{"matcher":"","hooks":[{"type":"command","command":"/x/sesshin-hook"}]}]}}`,
			changes: whole("removed hooks.Zed:whatever", "removed hooks.Stop:user-prompt", "removed hooks.Stop:statusline", "removed hooks.Stop:stop"),
		},
		{
			name:    "sesshin's statusLine with padding goes whole",
			in:      `{"statusLine":{"type":"command","command":"` + old + ` statusline","padding":2},"x":1}`,
			out:     `{"x":1}`,
			changes: whole("removed statusLine"),
		},
		{
			name:    "a statusLine set since install is left alone, reported unchanged",
			in:      `{"statusLine":{"type":"command","command":"npx ccstatusline"},"x":1}`,
			out:     `{"statusLine":{"type":"command","command":"npx ccstatusline"},"x":1}`,
			changes: whole("unchanged statusLine"),
		},
		{
			name:    "a statusLine with sesshin's path and another verb is not sesshin's",
			in:      `{"statusLine":{"type":"command","command":"` + hookBin + ` stop"}}`,
			out:     `{"statusLine":{"type":"command","command":"` + hookBin + ` stop"}}`,
			changes: whole("unchanged statusLine"),
		},
		{
			name:    "hooks of herd's shim, and of other tools, stay",
			in:      `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"~/.claude/sesshin-hook.sh stop"},{"type":"command","command":"$HOME/.claude/sesshin/hook.sh stop"},{"type":"command","command":"` + hookBin + ` stop; echo"}]}]}}`,
			out:     `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"~/.claude/sesshin-hook.sh stop"},{"type":"command","command":"$HOME/.claude/sesshin/hook.sh stop"},{"type":"command","command":"` + hookBin + ` stop; echo"}]}]}}`,
			changes: whole(),
		},
		{
			name:    "odd numbers and strings are kept",
			in:      `{"a":1.0,"b":1e3,"s":"<>&\u2028","hooks":{"Stop":[` + stop + `]}}`,
			out:     `{"a":1.0,"b":1e3,"s":"<>&\u2028"}`,
			changes: whole("removed hooks.Stop:stop"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, p := uninstall(t, tt.in)
			checkProposal(t, tree, p, tt.out, tt.changes)
			if p.StatusLineReplaced {
				t.Error("StatusLineReplaced")
			}
			// Uninstalling again changes nothing.
			again, err := ProposeUninstall(p.Tree, ours)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range again.Changes {
				if c.Action == Removed {
					t.Errorf("uninstalling again: %s %s", c.Action, c.What)
				}
			}
			if marshal(t, again.Tree) != marshal(t, p.Tree) {
				t.Error("uninstalling again changed the bytes")
			}
		})
	}
}

// The Order conflict: operations.md's uninstall Order says the list is empty
// when settings.json holds no sesshin entry "and its statusLine isn't sesshin's",
// but its Effects reports a statusLine set since install `unchanged`. A
// non-sesshin statusLine is an `unchanged` item; an absent one has none.
func TestUninstallStatusLineItem(t *testing.T) {
	_, p := uninstall(t, `{"statusLine":{"command":"x"}}`)
	if got := p.list(); !sameList(got, []string{"unchanged statusLine"}) {
		t.Errorf("%q", got)
	}
	_, p = uninstall(t, `{}`)
	if len(p.Changes) != 0 {
		t.Errorf("%q", p.list())
	}
}

// Install then uninstall gives back the settings, when none of sesshin's entries
// were there to begin with and no statusLine was replaced.
func TestInstallUninstallRoundTrip(t *testing.T) {
	for _, in := range []string{
		`{}`,
		`{"model":"x","n":1.0}`,
		`{"hooks":{"Stop":[` + otherGroup("") + `],"Custom":[` + otherGroup("X") + `]},"model":"x"}`,
		`{"hooks":{"PostToolUse":[` + otherGroup("Bash") + `,` + otherGroup("") + `]},"z":[1e3]}`,
	} {
		_, p := install(t, in)
		_, q := uninstall(t, marshal(t, p.Tree))
		want := compact(t, in)
		if got := compact(t, marshal(t, q.Tree)); got != want {
			t.Errorf("%s\n -> %s", in, got)
		}
	}
}
