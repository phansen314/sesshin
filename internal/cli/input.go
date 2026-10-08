package cli

import (
	"cmp"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
)

// integer is a JSON integer literal: what Int converts to a number.
var integer = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// buildInput places each argument and each option given at its field, and
// the tokens after "--" at the command's Rest. Values are judged by the
// operation's checks, not here (implementation-spec.md, The CLI), except
// what can't be built: a malformed --var is a problem at its field, and a
// file or the working directory that can't be read is an error. Problems that
// need no file or environment are found first, and with any, nothing is
// read: invalid-input comes before io and environment.
func buildInput(c *Command, cmd *cobra.Command, args []string, env Env) (*jsonio.Object, []model.Problem, *ops.Error) {
	in := &jsonio.Object{}
	positional, rest := split(c, cmd, args)
	for i, a := range c.Arguments {
		setAt(in, a.Field, positional[i])
	}
	for _, ptr := range c.Objects {
		setAt(in, ptr, &jsonio.Object{})
	}
	if c.Rest != "" {
		setAt(in, c.Rest, list2any(rest))
	}
	var problems []model.Problem
	var dirs, files []Option
	for _, o := range c.Options {
		given := cmd.Flags().Changed(o.Name)
		switch {
		case o.Type == Dir:
			dirs = append(dirs, o)
		case !given:
		case o.Type == File:
			files = append(files, o)
		case o.Type == Bool:
			b, _ := cmd.Flags().GetBool(o.Name)
			setAt(in, o.Field, b)
		case o.Type == List:
			vals, _ := cmd.Flags().GetStringArray(o.Name)
			setAt(in, o.Field, list(vals))
		case o.Type == Repeat:
			vals, _ := cmd.Flags().GetStringArray(o.Name)
			setAt(in, o.Field, list2any(vals))
		case o.Type == JSON:
			s, _ := cmd.Flags().GetString(o.Name)
			v, repeated, err := jsonio.ParseValue([]byte(s))
			switch {
			case err != nil:
				problems = append(problems, model.Problem{Field: o.Field, Reason: err.Error()})
			case len(repeated) > 0:
				for _, r := range repeated {
					problems = append(problems, model.Problem{Field: o.Field + r, Reason: "repeated key"})
				}
			default:
				setAt(in, o.Field, v)
			}
		case o.Type == Map:
			problems = append(problems, setMap(in, o.Field, *cmd.Flags().Lookup(o.Name).Value.(*tokens))...)
		default:
			s, _ := cmd.Flags().GetString(o.Name)
			setAt(in, o.Field, scalar(o.Type, s))
		}
	}

	// A directory is resolved even when there are problems, so that a missing
	// cwd is not added to them. One that can't be resolved is an error, which
	// problems take precedence over.
	var dirErr *ops.Error
	for _, o := range dirs {
		s, _ := cmd.Flags().GetString(o.Name)
		given := cmd.Flags().Changed(o.Name)
		if prob := checkTilde(s); given && prob != "" {
			problems = append(problems, model.Problem{Field: o.Field, Reason: prob})
			setAt(in, o.Field, s)
			continue
		}
		dir, e := resolveDir(s, given, env)
		if e != nil {
			dirErr = cmp.Or(dirErr, e)
			continue
		}
		setAt(in, o.Field, dir)
	}
	if len(problems) > 0 {
		return in, problems, nil
	}
	if dirErr != nil {
		return nil, nil, dirErr
	}
	for _, o := range files {
		s, _ := cmd.Flags().GetString(o.Name)
		data, e := readInput(s, env)
		if e != nil {
			return nil, nil, e
		}
		setAt(in, o.Field, string(data)) // UTF-8 is the operation's check
	}
	return in, nil, nil
}

// tokens is the value of a Map option: each occurrence, as given. pflag's
// StringArray would lose an empty one on reading it back (its getter parses
// the list as CSV), and an empty --var is a malformed token to report.
type tokens []string

func (t *tokens) Set(s string) error { *t = append(*t, s); return nil }
func (t *tokens) String() string     { return strings.Join(*t, " ") }
func (t *tokens) Type() string       { return "stringArray" }

// list2any is tokens as the items of a JSON array; never nil.
func list2any(tokens []string) []any {
	out := []any{}
	for _, t := range tokens {
		out = append(out, t)
	}
	return out
}

// setMap puts each KEY=VALUE, split at the first "=", into the object at
// field. A token with no "=" and a KEY seen before are problems at field,
// and are left out; a bad KEY is the operation's to judge.
func setMap(in *jsonio.Object, field string, tokens []string) []model.Problem {
	var problems []model.Problem
	m := &jsonio.Object{}
	for _, t := range tokens {
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			problems = append(problems, model.Problem{Field: field, Reason: "expected KEY=VALUE, got " + strconv.Quote(t)})
			continue
		}
		if _, dup := m.Get(k); dup {
			problems = append(problems, model.Problem{Field: field, Reason: "repeated key " + strconv.Quote(k)})
			continue
		}
		m.Set(k, v)
	}
	setAt(in, field, m)
	return problems
}

// scalar converts one token of type t.
func scalar(t Type, s string) any {
	if t == Int && integer.MatchString(s) {
		return json.Number(s)
	}
	return s
}

// list joins the lists of a list option's values, in order. Each value is
// comma-separated; the empty string is the empty list, and an empty item
// beside others stays one, which the operation rejects.
func list(values []string) []any {
	out := []any{}
	for _, v := range values {
		if v == "" {
			continue
		}
		for _, item := range strings.Split(v, ",") {
			out = append(out, item)
		}
	}
	return out
}

// setAt sets the field at ptr, creating the objects above it. Pointers come
// from the command tables: plain names, no escapes.
func setAt(obj *jsonio.Object, ptr string, v any) {
	segs := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for _, s := range segs[:len(segs)-1] {
		next, ok := obj.Get(s)
		child, isObj := next.(*jsonio.Object)
		if !ok || !isObj {
			child = &jsonio.Object{}
			obj.Set(s, child)
		}
		obj = child
	}
	obj.Set(segs[len(segs)-1], v)
}

// readInput reads --input, --prompt-file, or --text-file: the file at path, or
// stdin for "-". It is a blocking os.Open and io.ReadAll, not fsys, so process
// substitution and named pipes work (implementation-spec.md, One package
// touches the disk). A file that can't be read, or a directory, is io.
func readInput(path string, env Env) ([]byte, *ops.Error) {
	if path == "-" {
		if env.Stdin == nil {
			return nil, nil
		}
		data, err := io.ReadAll(env.Stdin)
		if err != nil {
			return nil, ops.IOError("-", err)
		}
		return data, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ops.IOError(path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f) // a directory opens, and fails here with EISDIR
	if err != nil {
		return nil, ops.IOError(path, err)
	}
	return data, nil
}

// checkTilde is the problem with a --cwd that names another user's home,
// "~user/…", which sesshin never expands; "" for any other.
func checkTilde(cwd string) string {
	if !strings.HasPrefix(cwd, "~") || cwd == "~" || strings.HasPrefix(cwd, "~/") {
		return ""
	}
	return "~user is not expanded: only ~/ is; give the path in full"
}

// resolveDir is --cwd as the operation takes it (cli-spec.md, spawn): a
// leading "~/" (or "~" alone) is the home directory, a relative path is
// under the working directory, and when it wasn't given it is the working
// directory. ".." is left in place, for the operation to judge, so the
// result is joined by hand and never cleaned.
func resolveDir(cwd string, given bool, env Env) (string, *ops.Error) {
	switch {
	case given && (cwd == "~" || strings.HasPrefix(cwd, "~/")):
		home := env.spawn().Getenv("HOME")
		if !filepath.IsAbs(home) {
			return "", &ops.Error{
				Kind:    ops.KindEnvironment,
				Message: "--cwd begins with ~/, and HOME is not an absolute path",
				Details: map[string]any{"variable": "HOME"},
			}
		}
		return strings.TrimSuffix(home, "/") + strings.TrimPrefix(cwd, "~"), nil
	case given && (cwd == "" || filepath.IsAbs(cwd)):
		return cwd, nil // an empty one is the operation's to refuse
	}
	wd, err := env.getwd()
	if err != nil {
		return "", ops.IOError(".", err)
	}
	if !given {
		return wd, nil
	}
	return strings.TrimSuffix(wd, "/") + "/" + cwd, nil
}
