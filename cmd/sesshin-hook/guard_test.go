package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/phansen314/sesshin"

// allowed is the one package outside the standard library and sesshin's own
// that sesshin-hook may link: macOS's process table is read through it, and
// its initialization was measured at nothing (design-spec.md, Hook cost).
const allowed = "golang.org/x/sys/unix"

// forbidden are the internal packages sesshin-hook must never link, directly or
// through another package (implementation-spec.md, Import direction).
var forbidden = []string{"cli", "ops", "config", "settings", "pick", "migrate"}

// dep is one package sesshin-hook links.
type dep struct {
	path     string
	standard bool
	dir      string
}

// hookPath are sesshin's packages that sesshin-hook links or will link
// (implementation-spec.md, Package layout). The guards check them and
// everything they import whether or not sesshin-hook links them yet, so a
// package is held to the rules before its first verb is wired. Add each new
// hook-path package here as it is created; TestHookPathListed fails until it is.
var hookPath = []string{"hook", "fsys", "jsonio", "model", "payload", "proc", "loc", "hookconf", "hooklog", "record", "statusline", "placement", "placement/backends", "placement/kitty", "live", "testhook"}

// deps lists every package sesshin-hook links, in build b, and every
// package hookPath's packages link.
func deps(t *testing.T, b build) []dep {
	t.Helper()
	args := []string{"list", "-deps", "-tags=" + b.tags, "-f", "{{.ImportPath}}\t{{.Standard}}\t{{.Dir}}", "."}
	for _, p := range hookPath {
		args = append(args, module+"/internal/"+p)
	}
	cmd := b.command(args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	var ds []dep
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			t.Fatalf("go list line %q", line)
		}
		ds = append(ds, dep{f[0], f[1] == "true", f[2]})
	}
	return ds
}

// build is one build the guards check: a system and build tags.
type build struct{ goos, tags string }

func (b build) String() string { return b.goos + " tags " + strconv.Quote(b.tags) }

// command is the go command for b: go list resolves build constraints for
// GOOS without a toolchain for it.
func (b build) command(args ...string) *exec.Cmd {
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOOS="+b.goos)
	return cmd
}

// builds are the builds checked, on each supported system: the shipped
// one, and the test-hook one (implementation-spec.md, Test hooks).
var builds = []build{{"linux", ""}, {"linux", "sesshintest"}, {"darwin", ""}, {"darwin", "sesshintest"}}

// sesshin-hook links the standard library and sesshin's own internal packages
// only, and none of those sesshin-hook may not import (implementation-spec.md,
// Toolchain).
func TestHookDependencies(t *testing.T) {
	for _, b := range builds {
		for _, d := range deps(t, b) {
			if d.standard || d.path == module+"/cmd/sesshin-hook" || d.path == allowed {
				continue
			}
			rest, ok := strings.CutPrefix(d.path, module+"/internal/")
			if !ok {
				t.Errorf("%v: sesshin-hook links %s, outside the standard library, %s, and sesshin's internal packages", b, d.path, allowed)
				continue
			}
			for _, f := range forbidden {
				if rest == f || strings.HasPrefix(rest, f+"/") {
					t.Errorf("%v: sesshin-hook links %s", b, d.path)
				}
			}
		}
	}
}

// Every sesshin package sesshin-hook links is in hookPath, so the list can't fall
// behind the code.
func TestHookPathListed(t *testing.T) {
	listed := map[string]bool{}
	for _, p := range hookPath {
		listed[p] = true
	}
	for _, b := range builds {
		cmd := b.command("list", "-deps", "-tags="+b.tags, "-f", "{{.ImportPath}}", ".")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list: %v", err)
		}
		for _, path := range strings.Fields(string(out)) {
			if rest, ok := strings.CutPrefix(path, module+"/internal/"); ok && !listed[rest] {
				t.Errorf("%v: sesshin-hook links %s, missing from hookPath", b, path)
			}
		}
	}
}

// H5: nothing sesshin-hook links calls os.Exit, or log.Fatal*
// or log.Panic*, which exit or panic past a verb's own recover.
func TestHookNoExit(t *testing.T) {
	for _, dir := range hookDirs(t) {
		for _, path := range goFiles(t, dir, false) {
			f := parse(t, path)
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				x, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				name := sel.Sel.Name
				if (x.Name == "os" && name == "Exit") || (x.Name == "log" && (strings.HasPrefix(name, "Fatal") || strings.HasPrefix(name, "Panic"))) {
					t.Errorf("%s: %s.%s", position(f, sel.Pos()), x.Name, name)
				}
				return true
			})
		}
	}
}

// hookDirs are the directories of sesshin's own packages the guards check,
// cmd/sesshin-hook aside.
func hookDirs(t *testing.T) []string {
	dirs := map[string]bool{}
	for _, b := range builds {
		for _, d := range deps(t, b) {
			if own(d) && d.path != module+"/cmd/sesshin-hook" {
				dirs[d.dir] = true
			}
		}
	}
	var out []string
	for d := range dirs {
		out = append(out, d)
	}
	return out
}

// Package initialization runs before main's recover and can't be recovered,
// so no package sesshin-hook links has an init function or a package-level
// variable whose initializer calls anything or asserts a type
// (implementation-spec.md, The hook binary). Conservative: a conversion is a
// call too; use a constant, or do the work inside the recovered path. Only
// sesshin's own packages are held to it: the one other, allowed, was
// measured instead.
func TestHookNoInitWork(t *testing.T) {
	dirs := map[string]bool{}
	for _, b := range builds {
		for _, d := range deps(t, b) {
			if own(d) {
				dirs[d.dir] = true
			}
		}
	}
	for dir := range dirs {
		for _, path := range goFiles(t, dir, false) {
			f := parse(t, path)
			for _, decl := range f.Decls {
				switch decl := decl.(type) {
				case *ast.FuncDecl:
					if decl.Recv == nil && decl.Name.Name == "init" {
						t.Errorf("%s: func init", position(f, decl.Pos()))
					}
				case *ast.GenDecl:
					if decl.Tok != token.VAR {
						continue
					}
					for _, spec := range decl.Specs {
						for _, v := range spec.(*ast.ValueSpec).Values {
							if n := work(v); n != nil {
								t.Errorf("%s: package-level initializer does work", position(f, n.Pos()))
							}
						}
					}
				}
			}
		}
	}
}

// work returns the first expression in e that runs code, can block, or can
// panic at package initialization, outside a function literal's body, or
// nil: a call (conversions included), a type assertion, a map literal
// (runtime.makemap), a channel receive, a division or remainder, an index,
// or a slice expression. Conservative: use a constant, or do the work inside
// the recovered path.
func work(e ast.Expr) ast.Node {
	var found ast.Node
	ast.Inspect(e, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr, *ast.TypeAssertExpr, *ast.IndexExpr, *ast.IndexListExpr, *ast.SliceExpr:
			found = n
			return false
		case *ast.CompositeLit:
			if _, ok := n.Type.(*ast.MapType); ok {
				found = n
				return false
			}
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				found = n
				return false
			}
		case *ast.BinaryExpr:
			if n.Op == token.QUO || n.Op == token.REM {
				found = n
				return false
			}
		}
		return true
	})
	return found
}

// H5: no go statement in sesshin's own code, tests aside.
func TestNoGoStatements(t *testing.T) {
	root := moduleRoot(t)
	for _, path := range goFiles(t, root, true) {
		f := parse(t, path)
		ast.Inspect(f, func(n ast.Node) bool {
			if g, ok := n.(*ast.GoStmt); ok {
				t.Errorf("%s: go statement", position(f, g.Pos()))
			}
			return true
		})
	}
}

// own reports whether d is one of sesshin's own packages.
func own(d dep) bool { return d.path == module || strings.HasPrefix(d.path, module+"/") }

var fset = token.NewFileSet()

func parse(t *testing.T, path string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func position(f *ast.File, p token.Pos) string { return fset.Position(p).String() }

// goFiles lists dir's non-test Go files, for every platform and tag, and with
// recursive, those of its subdirectories too, skipping testdata and hidden
// directories.
func goFiles(t *testing.T, dir string, recursive bool) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (!recursive || name == "testdata" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	return paths
}

// moduleRoot is the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}
