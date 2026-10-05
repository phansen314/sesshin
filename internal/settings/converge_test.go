package settings

import (
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// gen builds random settings.json text: sesshin's entries of every staleness,
// other tools' entries, odd shapes, and a statusLine of each kind.
type gen struct{ r *rand.Rand }

func (g gen) pick(xs ...string) string { return xs[g.r.Intn(len(xs))] }

func (g gen) sesshinHook() string {
	path := g.pick(hookBin, "/old/sesshin-hook", "'/my dir/sesshin-hook'", "sesshin-hook")
	verb := g.pick("session-start", "user-prompt", "terminal-sync", "post-tool-use", "stop", "notification", "compact",
		"cwd-changed", "session-end", "statusline", "bogus")
	timeout := g.pick("10", "10", "10.0", "5", "1e1")
	async := g.pick(`,"async":true`, `,"async":true`, `,"async":false`, ``)
	if g.r.Intn(2) == 0 {
		return `{"type":"command","command":"` + path + ` ` + verb + `","timeout":` + timeout + async + `}`
	}
	return `{"command":"` + path + ` ` + verb + `"` + async + `,"timeout":` + timeout + `,"type":"command"}`
}

func (g gen) hook() string {
	switch n := g.r.Intn(10); {
	case n < 4:
		return g.sesshinHook()
	case n < 6:
		return other
	default:
		return g.pick(`1`, `null`, `[]`, `{"type":"prompt","prompt":"p"}`, `{"command":5}`,
			`{"type":"command","command":"$HOME/.claude/sesshin-hook.sh stop"}`, `{"type":"command","command":"`+hookBin+` stop; x"}`)
	}
}

func (g gen) group() string {
	var members []string
	switch g.r.Intn(4) {
	case 0:
		members = append(members, `"matcher":""`)
	case 1:
		members = append(members, `"matcher":"Bash"`)
	}
	if g.r.Intn(8) == 0 {
		members = append(members, `"if":"x"`)
	}
	if g.r.Intn(12) != 0 {
		var hooks []string
		for range g.r.Intn(4) {
			hooks = append(hooks, g.hook())
		}
		members = append(members, `"hooks":[`+strings.Join(hooks, ",")+`]`)
	}
	return `{` + strings.Join(members, ",") + `}`
}

// rule is one item of a permissions array: sesshin's rules in either array,
// the user's, and items that aren't strings.
func (g gen) rule() string {
	return g.pick(`"Bash(sesshin:*)"`, `"Bash(jq:*)"`, `"Bash(sesshin install:*)"`, `"Bash(sesshin uninstall:*)"`, `"Bash(sesshin prune:*)"`,
		`"Bash(sesshin:*)"`, `"Bash(sesshin install:*)"`, `"Read(~/x/**)"`, `"Bash(sesshin list:*)"`, `"bash(jq:*)"`, `7`, `null`, `{"a":1.0}`)
}

// permissions is a permissions member of every shape install and uninstall
// handle: any of allow, ask, deny, and defaultMode, each array of any rules.
func (g gen) permissions() string {
	var members []string
	for _, key := range []string{"allow", "ask", "deny"} {
		if g.r.Intn(3) == 0 {
			continue
		}
		var items []string
		for range g.r.Intn(6) {
			items = append(items, g.rule())
		}
		members = append(members, `"`+key+`":[`+strings.Join(items, ",")+`]`)
	}
	if g.r.Intn(3) == 0 {
		members = append(members, `"defaultMode":"plan"`)
	}
	g.r.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	return `"permissions":{` + strings.Join(members, ",") + `}`
}

func (g gen) settings() string {
	var members []string
	if g.r.Intn(2) == 0 {
		members = append(members, `"model":"x","n":1.0,"e":[1e3,-0]`)
	}
	if g.r.Intn(5) != 0 {
		events := []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "Stop", "StopFailure",
			"Notification", "PreCompact", "PostCompact", "CwdChanged", "SessionEnd", "PreToolUse", "Elicitation"}
		g.r.Shuffle(len(events), func(i, j int) { events[i], events[j] = events[j], events[i] })
		var evs []string
		for _, e := range events[:g.r.Intn(len(events)+1)] {
			var groups []string
			for range g.r.Intn(4) {
				groups = append(groups, g.group())
			}
			evs = append(evs, `"`+e+`":[`+strings.Join(groups, ",")+`]`)
		}
		members = append(members, `"hooks":{`+strings.Join(evs, ",")+`}`)
	}
	switch g.r.Intn(6) {
	case 0:
		members = append(members, `"statusLine":{"type":"command","command":"`+g.pick(hookBin, "/old/sesshin-hook")+` statusline"}`)
	case 1:
		members = append(members, `"statusLine":{"padding":0,"command":"`+hookBin+` statusline"}`)
	case 2:
		members = append(members, `"statusLine":{"type":"command","command":"my-line.sh"}`)
	}
	if g.r.Intn(2) == 0 {
		members = append(members, `"z":{"hooks":[1.50]}`)
	}
	if g.r.Intn(3) != 0 {
		members = append(members, g.permissions())
	}
	g.r.Shuffle(len(members), func(i, j int) {
		// Keep hooks and statusLine as generated relative to the rest, but
		// vary where they fall.
		members[i], members[j] = members[j], members[i]
	})
	return `{` + strings.Join(members, ",") + `}`
}

// foreign counts the hooks in groups that aren't sesshin's, by their bytes.
func foreign(t *testing.T, tree *jsonio.Object) map[string]int {
	t.Helper()
	sesshinsHooks := map[*jsonio.Object]bool{}
	for _, e := range scan(tree, ours) {
		sesshinsHooks[e.hook] = true
	}
	out := map[string]int{}
	v, ok := tree.Get("hooks")
	if !ok {
		return out
	}
	for _, ev := range v.(*jsonio.Object).Members {
		for _, g := range ev.Value.([]any) {
			hv, _ := g.(*jsonio.Object).Get("hooks")
			hooks, _ := hv.([]any)
			for _, h := range hooks {
				if ho, ok := h.(*jsonio.Object); ok && sesshinsHooks[ho] {
					continue
				}
				b, err := jsonio.MarshalLine(h)
				if err != nil {
					t.Fatal(err)
				}
				out[string(b)]++
			}
		}
	}
	return out
}

// permissionCounts counts the items of each array under permissions as
// "array item" by their bytes, leaving out the rules for which skip is true.
// Anything under permissions that isn't allow or ask is counted as a whole.
func permissionCounts(t *testing.T, tree *jsonio.Object, skip func(array, rule string) bool) map[string]int {
	t.Helper()
	out := map[string]int{}
	v, ok := tree.Get("permissions")
	if !ok {
		return out
	}
	for _, m := range v.(*jsonio.Object).Members {
		items, ok := m.Value.([]any)
		if !ok || m.Key != "allow" && m.Key != "ask" {
			b, err := jsonio.MarshalLine(m.Value)
			if err != nil {
				t.Fatal(err)
			}
			out[m.Key+" "+strings.TrimSuffix(string(b), "\n")]++
			continue
		}
		for _, it := range items {
			if s, ok := it.(string); ok && skip(m.Key, s) {
				continue
			}
			b, err := jsonio.MarshalLine(it)
			if err != nil {
				t.Fatal(err)
			}
			out[m.Key+" "+strings.TrimSuffix(string(b), "\n")]++
		}
	}
	return out
}

func sameCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, n := range a {
		if b[k] != n {
			return false
		}
	}
	return true
}

// Proposing again from any proposal gives every item unchanged and the same
// bytes; install wires exactly one entry per registration and keeps every
// hook that isn't sesshin's; uninstall leaves none of sesshin's and keeps the same.
func TestConvergenceProperty(t *testing.T) {
	g := gen{rand.New(rand.NewSource(7))}
	for i := range 3000 {
		in := g.settings()
		tree, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("generated %s: %v", in, err)
		}
		p, err := ProposeInstall(tree, hookBin, ours)
		if err != nil {
			t.Fatal(err)
		}
		if t.Failed() {
			return
		}
		converges(t, p)
		out := marshal(t, p.Tree)
		back := parse(t, out)
		if es := scan(back, ours); len(es) != len(Registrations()) {
			t.Fatalf("case %d: install left %d sesshin entries:\n%s\n->\n%s", i, len(es), in, out)
		}
		if !sameCounts(foreign(t, tree), foreign(t, back)) {
			t.Fatalf("case %d: install changed other hooks:\n%s\n->\n%s", i, in, out)
		}
		// Every rule is in its array, and nothing else in permissions moved,
		// or changed: only the missing rules were added.
		for _, r := range PermissionRules() {
			if n := permissionCounts(t, back, func(a, s string) bool { return a != r.Array || s != r.Rule })[r.Array+` "`+r.Rule+`"`]; n == 0 {
				t.Fatalf("case %d: install left %s out of %s:\n%s\n->\n%s", i, r.Rule, r.Array, in, out)
			}
		}
		isRule := func(a, s string) bool {
			for _, r := range PermissionRules() {
				if r.Array == a && r.Rule == s {
					return true
				}
			}
			return false
		}
		if !sameCounts(permissionCounts(t, tree, isRule), permissionCounts(t, back, isRule)) {
			t.Fatalf("case %d: install changed permissions beyond its rules:\n%s\n->\n%s", i, in, out)
		}
		if t.Failed() {
			t.Fatalf("case %d:\n%s\n->\n%s", i, in, out)
		}

		u, err := ProposeUninstall(tree, ours)
		if err != nil {
			t.Fatal(err)
		}
		uout := marshal(t, u.Tree)
		uback := parse(t, uout)
		if es := scan(uback, ours); len(es) != 0 {
			t.Fatalf("case %d: uninstall left %d sesshin entries:\n%s\n->\n%s", i, len(es), in, uout)
		}
		if !sameCounts(foreign(t, tree), foreign(t, uback)) {
			t.Fatalf("case %d: uninstall changed other hooks:\n%s\n->\n%s", i, in, uout)
		}
		sesshinsRules := func(a, s string) bool { return sesshinsRule(a, s) }
		// Leaving out sesshin's rules drops nothing: none was left to leave out.
		none := func(string, string) bool { return false }
		if len(permissionCounts(t, uback, sesshinsRules)) != len(permissionCounts(t, uback, none)) {
			t.Fatalf("case %d: uninstall left sesshin's rules:\n%s\n->\n%s", i, in, uout)
		}
		if !sameCounts(permissionCounts(t, tree, sesshinsRules), permissionCounts(t, uback, sesshinsRules)) {
			t.Fatalf("case %d: uninstall changed permissions beyond sesshin's rules:\n%s\n->\n%s", i, in, uout)
		}
		u2, err := ProposeUninstall(uback, ours)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range u2.Changes {
			if c.Action == Removed {
				t.Fatalf("case %d: uninstalling again removed %s:\n%s", i, c.What, uout)
			}
		}
		if marshal(t, u2.Tree) != uout {
			t.Fatalf("case %d: uninstalling again changed the bytes", i)
		}
		// Uninstalling an installed file leaves no sesshin entry, whatever it
		// was before.
		ui, _ := ProposeUninstall(back, ours)
		if es := scan(ui.Tree, ours); len(es) != 0 {
			t.Fatalf("case %d: uninstall after install left entries", i)
		}
	}
}

// A file already in the File format, with odd numbers and the characters it
// escapes, comes back byte for byte when nothing needs changing.
func TestAlreadyWiredRoundTrips(t *testing.T) {
	in := `{
  "model": "opus",
  "n": [
    1.0,
    1e3,
    -0,
    0.10,
    1E+2
  ],
  "s": "é世 <>&\u2028\u2029\"\\\u0000",
  "empty": {},
  "none": [],
  "hooks": {
    "Stop": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "other-tool run"
          }
        ]
      },
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/home/me/go/bin/sesshin-hook stop",
            "timeout": 10,
            "async": true
          }
        ]
      }
    ]
  }
}
`
	// Add what makes it wired: every other registered event, then statusLine.
	tree := parse(t, in)
	p, err := ProposeInstall(tree, hookBin, ours)
	if err != nil {
		t.Fatal(err)
	}
	wiredOut := marshal(t, p.Tree)
	q, err := ProposeInstall(parse(t, wiredOut), hookBin, ours)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range q.Changes {
		if c.Action != Unchanged {
			t.Errorf("%s %s", c.Action, c.What)
		}
	}
	if got := marshal(t, q.Tree); got != wiredOut {
		t.Errorf("round trip changed the bytes:\n%s\n%s", wiredOut, got)
	}
	// And the file itself is what Marshal writes of its parse.
	if got := marshal(t, tree); got != in {
		t.Errorf("Marshal(Parse(in)) differs:\n%s\n%s", in, got)
	}
	if !strings.HasSuffix(wiredOut, "}\n") || strings.HasSuffix(wiredOut, "\n\n") {
		t.Error("not one trailing newline")
	}
}

// Golden files: testdata/<name>.json is a settings.json; <name>.install.json
// and <name>.uninstall.json are the proposals, and <name>.changes the items.
func TestGolden(t *testing.T) {
	inputs, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, path := range inputs {
		base := strings.TrimSuffix(path, ".json")
		if strings.Contains(filepath.Base(base), ".") {
			continue // a golden output
		}
		n++
		t.Run(filepath.Base(base), func(t *testing.T) {
			in, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tree, err := Parse(in)
			if err != nil {
				t.Fatal(err)
			}
			ip, err := ProposeInstall(tree, hookBin, ours)
			if err != nil {
				t.Fatal(err)
			}
			up, err := ProposeUninstall(tree, ours)
			if err != nil {
				t.Fatal(err)
			}
			var changes strings.Builder
			changes.WriteString("install\n")
			for _, l := range ip.list() {
				changes.WriteString("  " + l + "\n")
			}
			if ip.StatusLineReplaced {
				changes.WriteString("  (status-line-replaced)\n")
			}
			changes.WriteString("uninstall\n")
			for _, l := range up.list() {
				changes.WriteString("  " + l + "\n")
			}
			files := map[string]string{
				base + ".install.json":   marshal(t, ip.Tree),
				base + ".uninstall.json": marshal(t, up.Tree),
				base + ".changes":        changes.String(),
			}
			for name, got := range files {
				if *update {
					if err := os.WriteFile(name, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				if string(want) != got {
					t.Errorf("%s differs:\n--- want\n%s--- got\n%s", name, want, got)
				}
			}
			// The install proposal is a fixed point, and a file that already
			// wires sesshin, written as sesshin writes, comes back byte for byte.
			converges(t, ip)
			wired := true
			for _, c := range ip.Changes {
				wired = wired && c.Action == Unchanged
			}
			if wired && marshal(t, ip.Tree) != string(in) {
				t.Error("nothing to change, but the bytes differ")
			}
		})
	}
	if n < 3 {
		t.Errorf("only %d golden inputs", n)
	}
}
