package record

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Design-spec, Two tiers; implementation-spec, Import direction: nothing
// written to lifecycle.json comes from sesshin.json. Two checks, one of what the
// code does and one of what it names.

// The same events, on state directories that differ only in sesshin.json and
// state.json (missing, pending, issued with a placement, unusable, another
// format), the backend's placement, and the sesshin IDs on offer, leave
// byte-identical lifecycle.json files.
func TestLifecycleIgnoresSesshin(t *testing.T) {
	sesshins := map[string]string{
		"missing":        "",
		"pending":        `{"schema": 1, "id": null, "job": null, "source": "hook", "placement": null, "extra": {}}`,
		"issued":         `{"schema": 1, "id": 7, "job": null, "source": "hook", "placement": {"terminal": "kitty", "socket": "unix:/x", "window_id": 4}, "extra": {}}`,
		"issued, nested": `{"schema": 1, "id": 99999, "job": null, "source": "hook", "placement": null, "extra": {}}`,
		"unusable":       "{",
		"another format": `{"schema": 4, "id": 3, "job": null, "source": "hook", "placement": null, "extra": {}}`,
	}
	events := []Event{
		{Kind: SessionStart, Source: "startup", Cwd: "/w", PermissionMode: "plan"},
		{Kind: UserPromptSubmit, PromptID: "p1", SessionTitle: "t"},
		{Kind: PostToolUse, PromptID: "p1"},
		{Kind: Stop, PromptID: "p1", BackgroundTasks: 1},
		{Kind: PostToolUse, PromptID: "p1"},
		{Kind: SessionStart, Source: "resume"},
		{Kind: SessionEnd, Reason: "logout"},
	}
	run := func(sesshin, state string, place bool) []string {
		f := newFix(t)
		if place {
			p, _, _ := placed(kitty(5))
			f.env.Placement = p
		}
		// The session directory exists, so sesshin.json is on disk before the
		// first event; lifecycle.json is not.
		if sesshin != "" {
			f.write(f.sessionPath(sid, "sesshin.json"), sesshin)
		} else {
			os.MkdirAll(f.sessionPath(sid), 0o700)
		}
		if state != "" {
			f.write(f.path("state.json"), state)
		}
		var out []string
		for i, ev := range events {
			f.env.Now = t0.Add(time.Duration(i) * time.Minute)
			f.rec(ev)
			data, err := os.ReadFile(f.sessionPath(sid, "lifecycle.json"))
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, string(data))
		}
		return out
	}
	want := run("", "", false)
	for name, sesshin := range sesshins {
		for _, state := range []string{"", `{"schema": 1, "last_id": 40}`, "{"} {
			for _, place := range []bool{false, true} {
				got := run(sesshin, state, place)
				for i := range want {
					if got[i] != want[i] {
						t.Errorf("sesshin.json %s, state.json %q, placement %v: lifecycle.json after event %d differs:\n%s\nwant\n%s", name, state, place, i, got[i], want[i])
						break
					}
				}
			}
		}
	}
}

// The code that builds lifecycle.json (event.go, effects.go, and
// recordLifecycle) names nothing of sesshin.json: not its file name
// (model.SesshinName), its struct, its reader, nor the placement. Only sesshin.go
// and sync.go, which write sesshin.json, do.
func TestLifecycleCodeNamesNoSesshin(t *testing.T) {
	forbidden := map[string]bool{"SesshinFile": true, "ReadSesshin": true, "SesshinName": true, "placement": true, "Placement": true, "completeSesshin": true}
	for _, name := range []string{"event.go", "effects.go", "record.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		check := func(n ast.Node, where string) {
			ast.Inspect(n, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && forbidden[id.Name] {
					t.Errorf("%s: %s names %s", name, where, id.Name)
				}
				return true
			})
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			switch {
			case name != "record.go":
				check(decl, "the file")
			case isFunc && fn.Name.Name == "recordLifecycle":
				check(fn, "recordLifecycle")
			}
		}
	}
}
