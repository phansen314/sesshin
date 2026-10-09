package settings

import (
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// permissionsOf is the compact text of a tree's permissions member, or
// "absent".
func permissionsOf(t *testing.T, tree *jsonio.Object) string {
	t.Helper()
	v, ok := tree.Get("permissions")
	if !ok {
		return "absent"
	}
	b, err := jsonio.MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

// permissionItems are the changes of a proposal that name a permission rule.
func permissionItems(p Proposal) []string {
	var out []string
	for _, l := range p.list() {
		if strings.Contains(l, " permissions.") {
			out = append(out, l)
		}
	}
	return out
}

const (
	hAllow = `"Bash(sesshin:*)"`
	jqRule = `"Bash(jq:*)"`
	hI     = `"Bash(sesshin install:*)"`
	hU     = `"Bash(sesshin uninstall:*)"`
	hP     = `"Bash(sesshin prune:*)"`
	hR     = `"Bash(sesshin resume:*)"`
	wiredA = `[` + hAllow + `]`
	wiredK = `[` + hI + `,` + hU + `,` + hP + `,` + hR + `]`
)

func TestPermissionRules(t *testing.T) {
	rules := PermissionRules()
	var got []string
	for _, r := range rules {
		got = append(got, r.Array+":"+r.Rule)
	}
	want := []string{"allow:Bash(sesshin:*)", "ask:Bash(sesshin install:*)", "ask:Bash(sesshin uninstall:*)", "ask:Bash(sesshin prune:*)", "ask:Bash(sesshin resume:*)"}
	if !sameList(got, want) {
		t.Errorf("rules %q, want %q", got, want)
	}
	rules[0].Rule = "changed"
	if PermissionRules()[0].Rule != "Bash(sesshin:*)" {
		t.Error("PermissionRules returns its own storage")
	}
}

func TestInstallPermissions(t *testing.T) {
	added := func(a ...Action) []string {
		var out []string
		for i, r := range PermissionRules() {
			out = append(out, string(a[i])+" "+permissionWhat(r))
		}
		return out
	}
	all := func(a Action) []string { return added(a, a, a, a, a) }
	tests := []struct {
		name    string
		in      string
		out     string
		changes []string
	}{
		{
			name:    "absent: permissions is created at the end",
			in:      `{"model":"x"}`,
			out:     `{"allow":` + wiredA + `,"ask":` + wiredK + `}`,
			changes: all(Added),
		},
		{
			name:    "permissions without allow or ask: they are created after what is there",
			in:      `{"permissions":{"deny":["Read(./.env)"],"defaultMode":"plan"}}`,
			out:     `{"deny":["Read(./.env)"],"defaultMode":"plan","allow":` + wiredA + `,"ask":` + wiredK + `}`,
			changes: all(Added),
		},
		{
			name:    "empty permissions is filled",
			in:      `{"permissions":{}}`,
			out:     `{"allow":` + wiredA + `,"ask":` + wiredK + `}`,
			changes: all(Added),
		},
		{
			name:    "present: nothing moves",
			in:      `{"permissions":{"ask":` + wiredK + `,"allow":[` + jqRule + `,` + hAllow + `]}}`,
			out:     `{"ask":` + wiredK + `,"allow":[` + jqRule + `,` + hAllow + `]}`,
			changes: all(Unchanged),
		},
		{
			name:    "some present: the missing are appended after the user's rules",
			in:      `{"permissions":{"allow":["Read(x)",` + jqRule + `,"Edit(y)"],"ask":[` + hU + `]}}`,
			out:     `{"allow":["Read(x)",` + jqRule + `,"Edit(y)",` + hAllow + `],"ask":[` + hU + `,` + hI + `,` + hP + `,` + hR + `]}`,
			changes: added(Added, Added, Unchanged, Added, Added),
		},
		{
			name:    "duplicated: both copies stay, the rule is unchanged",
			in:      `{"permissions":{"allow":[` + hAllow + `,` + hAllow + `,` + jqRule + `],"ask":` + wiredK + `}}`,
			out:     `{"allow":[` + hAllow + `,` + hAllow + `,` + jqRule + `],"ask":` + wiredK + `}`,
			changes: all(Unchanged),
		},
		{
			name:    "in deny only: still added to allow, deny kept",
			in:      `{"permissions":{"deny":[` + hAllow + `,` + hP + `]}}`,
			out:     `{"deny":[` + hAllow + `,` + hP + `],"allow":` + wiredA + `,"ask":` + wiredK + `}`,
			changes: all(Added),
		},
		{
			name:    "in the other array: a rule is compared within its own array",
			in:      `{"permissions":{"allow":[` + hI + `],"ask":[` + hAllow + `]}}`,
			out:     `{"allow":[` + hI + `,` + hAllow + `],"ask":[` + hAllow + `,` + hI + `,` + hU + `,` + hP + `,` + hR + `]}`,
			changes: all(Added),
		},
		{
			name:    "whole strings only: a longer or different rule is not the rule",
			in:      `{"permissions":{"allow":["Bash(sesshin:* )","Bash(sesshin list:*)","bash(jq:*)"]}}`,
			out:     `{"allow":["Bash(sesshin:* )","Bash(sesshin list:*)","bash(jq:*)",` + hAllow + `],"ask":` + wiredK + `}`,
			changes: all(Added),
		},
		{
			name:    "non-string items stay where they are",
			in:      `{"permissions":{"allow":[1,null,{"a":1.0},[],` + hAllow + `,true],"ask":[null]}}`,
			out:     `{"allow":[1,null,{"a":1.0},[],` + hAllow + `,true],"ask":[null,` + hI + `,` + hU + `,` + hP + `,` + hR + `]}`,
			changes: added(Unchanged, Added, Added, Added, Added),
		},
		{
			name:    "empty arrays are filled in place",
			in:      `{"permissions":{"ask":[],"allow":[]}}`,
			out:     `{"ask":` + wiredK + `,"allow":` + wiredA + `}`,
			changes: all(Added),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, p := install(t, tt.in)
			if got := permissionsOf(t, p.Tree); got != compact(t, tt.out) {
				t.Errorf("permissions:\n got %s\nwant %s", got, compact(t, tt.out))
			}
			if got := permissionItems(p); !sameList(got, tt.changes) {
				t.Errorf("changes:\n got %q\nwant %q", got, tt.changes)
			}
			if got := p.list(); len(got) != 18 || !sameList(got[13:], tt.changes) {
				t.Errorf("the rules come after statusLine: %q", got)
			}
			// Nothing outside permissions is touched.
			a, _ := tree.Get("model")
			b, _ := p.Tree.Get("model")
			if a != b {
				t.Error("model changed")
			}
			converges(t, p)
		})
	}
}

func TestInstallPermissionsKeepPlace(t *testing.T) {
	// permissions already in the file keeps its place among the other keys.
	_, p := install(t, `{"permissions":{"defaultMode":"plan"},"model":"x"}`)
	keys := make([]string, 0, p.Tree.Len())
	for _, m := range p.Tree.Members {
		keys = append(keys, m.Key)
	}
	if want := []string{"permissions", "model", "hooks", "statusLine"}; !sameList(keys, want) {
		t.Errorf("keys %q, want %q", keys, want)
	}
}

func TestParsePermissionsCorruptNamesKey(t *testing.T) {
	// ProposeInstall and ProposeUninstall check a hand-built tree too.
	for _, in := range []string{`{"permissions":[]}`, `{"permissions":{"allow":"x"}}`, `{"permissions":{"ask":{}}}`} {
		v, _, _ := jsonio.ParseValue([]byte(in))
		root := v.(*jsonio.Object)
		if _, err := ProposeInstall(root, hookBin, ours); err == nil {
			t.Errorf("install accepted %s", in)
		}
		if _, err := ProposeUninstall(root, ours); err == nil {
			t.Errorf("uninstall accepted %s", in)
		}
	}
}

func TestUninstallPermissions(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		out     string // the permissions member after
		changes []string
	}{
		{
			name:    "absent: no items",
			in:      `{"model":"x"}`,
			out:     `absent`,
			changes: nil,
		},
		{
			name: "wired: sesshin's five go, the user's Bash(jq:*) stays",
			in:   `{"permissions":{"allow":[` + hAllow + `,` + jqRule + `],"ask":` + wiredK + `}}`,
			out:  `{"allow":[` + jqRule + `]}`,
			changes: []string{
				"removed permissions.allow:Bash(sesshin:*)", "removed permissions.ask:Bash(sesshin install:*)",
				"removed permissions.ask:Bash(sesshin uninstall:*)", "removed permissions.ask:Bash(sesshin prune:*)",
				"removed permissions.ask:Bash(sesshin resume:*)",
			},
		},
		{
			name:    "only sesshin's, copies and all: arrays and permissions go",
			in:      `{"permissions":{"allow":[` + hAllow + `,` + hAllow + `],"ask":[` + hP + `,` + hI + `,` + hP + `]}}`,
			out:     `absent`,
			changes: []string{"removed permissions.allow:Bash(sesshin:*)", "removed permissions.ask:Bash(sesshin install:*)", "removed permissions.ask:Bash(sesshin prune:*)"},
		},
		{
			name:    "items in the table's order, whatever their order in the file",
			in:      `{"permissions":{"ask":[` + hP + `,` + hU + `,` + hI + `],"allow":[` + hAllow + `]}}`,
			out:     `absent`,
			changes: []string{"removed permissions.allow:Bash(sesshin:*)", "removed permissions.ask:Bash(sesshin install:*)", "removed permissions.ask:Bash(sesshin uninstall:*)", "removed permissions.ask:Bash(sesshin prune:*)"},
		},
		{
			name:    "the user's rules, non-strings, deny and defaultMode stay",
			in:      `{"permissions":{"defaultMode":"plan","allow":["Read(x)",` + hAllow + `,3,null],"deny":[` + hP + `],"ask":["Edit(y)",` + hI + `]}}`,
			out:     `{"defaultMode":"plan","allow":["Read(x)",3,null],"deny":[` + hP + `],"ask":["Edit(y)"]}`,
			changes: []string{"removed permissions.allow:Bash(sesshin:*)", "removed permissions.ask:Bash(sesshin install:*)"},
		},
		{
			name:    "an emptied array goes and the other stays",
			in:      `{"permissions":{"allow":[` + hAllow + `],"ask":["Edit(y)"]}}`,
			out:     `{"ask":["Edit(y)"]}`,
			changes: []string{"removed permissions.allow:Bash(sesshin:*)"},
		},
		{
			name:    "an emptied array with deny left: permissions stays",
			in:      `{"permissions":{"allow":[` + hAllow + `],"deny":["x"]}}`,
			out:     `{"deny":["x"]}`,
			changes: []string{"removed permissions.allow:Bash(sesshin:*)"},
		},
		{
			name:    "only Bash(jq:*): nothing to remove",
			in:      `{"permissions":{"allow":[` + jqRule + `]}}`,
			out:     `{"allow":[` + jqRule + `]}`,
			changes: nil,
		},
		{
			name:    "a rule in the other array is not sesshin's there",
			in:      `{"permissions":{"allow":[` + hI + `],"ask":[` + hAllow + `]}}`,
			out:     `{"allow":[` + hI + `],"ask":[` + hAllow + `]}`,
			changes: nil,
		},
		{
			name:    "arrays and permissions that were empty before are left alone",
			in:      `{"permissions":{"allow":[],"ask":[]}}`,
			out:     `{"allow":[],"ask":[]}`,
			changes: nil,
		},
		{
			name:    "empty before, and the other array emptied: permissions stays",
			in:      `{"permissions":{"allow":[],"ask":[` + hP + `]}}`,
			out:     `{"allow":[]}`,
			changes: []string{"removed permissions.ask:Bash(sesshin prune:*)"},
		},
		{
			name:    "an empty permissions is left alone",
			in:      `{"permissions":{}}`,
			out:     `{}`,
			changes: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, p := uninstall(t, tt.in)
			want := tt.out
			if want != "absent" {
				want = compact(t, want)
			}
			if got := permissionsOf(t, p.Tree); got != want {
				t.Errorf("permissions:\n got %s\nwant %s", got, want)
			}
			if got := p.list(); !sameList(got, tt.changes) && (len(got) != 0 || len(tt.changes) != 0) {
				t.Errorf("changes:\n got %q\nwant %q", got, tt.changes)
			}
		})
	}
}

// Uninstall's items come after statusLine.
func TestUninstallPermissionsAfterStatusLine(t *testing.T) {
	_, p := uninstall(t, `{"permissions":{"allow":[`+hAllow+`]},"statusLine":`+wiredStatusLine+`}`)
	if got, want := p.list(), []string{"removed statusLine", "removed permissions.allow:Bash(sesshin:*)"}; !sameList(got, want) {
		t.Errorf("%q", got)
	}
}
