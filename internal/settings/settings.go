// Package settings builds the proposed settings.json: it reads Claude Code's
// settings.json into the ordered tree, finds sesshin's entries in it, and
// proposes the tree with sesshin wired in (ProposeInstall) or out
// (ProposeUninstall), changing nothing else (implementation-spec.md, The
// settings.json proposal; hooks-spec.md, Registration).
//
// It is pure: no file I/O, and nothing of install.json or the output
// envelope. The caller reads and writes the files, and supplies the
// predicate that says which paths are sesshin-hook. Only sesshin links it;
// sesshin-hook never does (implementation-spec.md, Import direction).
package settings

import (
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// MaxDepth is the deepest nesting of objects and arrays settings.json may
// have. A user's is a few levels deep; the proposal is indented, and grows
// with the square of the depth, so one near the encoder's limit would make
// every proposal hundreds of megabytes.
const MaxDepth = 64

// CorruptError is a settings.json that was read but is bad: not one JSON
// object, nested too deep, or with a sesshin-relevant key of the wrong shape.
// Detail names the key (hooks.Stop[2].hooks). The caller sets Path to the
// settings file, as operations.md's corrupt error carries it.
type CorruptError struct {
	Path   string
	Detail string
}

func (e *CorruptError) Error() string {
	if e.Path == "" {
		return e.Detail
	}
	return e.Path + ": " + e.Detail
}

func corrupt(detail string) error { return &CorruptError{Detail: detail} }

// Empty returns the tree of an absent settings.json: an empty object.
func Empty() *jsonio.Object { return &jsonio.Object{} }

// Parse reads the bytes of settings.json strictly into the ordered tree, and
// checks the shape of the keys sesshin reads (Check). Every failure is a
// *CorruptError. A key repeated at a place sesshin reads is corrupt: Claude
// Code takes the last, a lookup here would take the first, and a proposal
// would keep both. A repeat anywhere else is the user's, and kept.
func Parse(data []byte) (*jsonio.Object, error) {
	root, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		return nil, corrupt(err.Error())
	}
	if d := depth(root); d > MaxDepth {
		return nil, corrupt("nested deeper than " + strconv.Itoa(MaxDepth) + " levels")
	}
	for _, ptr := range repeated {
		if path, ok := relevantRepeat(ptr); ok {
			return nil, corrupt("repeated key " + path)
		}
	}
	if err := Check(root); err != nil {
		return nil, err
	}
	return root, nil
}

// depth is the deepest nesting of objects and arrays in v, counted without
// recursion.
func depth(v any) int {
	type item struct {
		v any
		d int
	}
	most := 0
	stack := []item{{v, 1}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch v := it.v.(type) {
		case *jsonio.Object:
			most = max(most, it.d)
			for _, m := range v.Members {
				stack = append(stack, item{m.Value, it.d + 1})
			}
		case []any:
			most = max(most, it.d)
			for _, e := range v {
				stack = append(stack, item{e, it.d + 1})
			}
		}
	}
	return most
}

// relevantRepeat reports whether the JSON Pointer of a repeated key is one
// sesshin reads, and names it as Check does.
func relevantRepeat(ptr string) (string, bool) {
	tokens := strings.Split(ptr[1:], "/")
	for i, t := range tokens {
		t = strings.ReplaceAll(t, "~1", "/")
		tokens[i] = strings.ReplaceAll(t, "~0", "~")
	}
	n := len(tokens)
	last := tokens[n-1]
	var ok bool
	switch tokens[0] {
	case "hooks":
		switch n {
		case 1, 2: // hooks, or an event
			ok = true
		case 4: // a group's matcher and hooks
			ok = last == "matcher" || last == "hooks"
		case 6: // a hook's type and command
			ok = tokens[3] == "hooks" && (last == "type" || last == "command")
		}
	case "statusLine":
		ok = n == 1 || n == 2 && (last == "type" || last == "command")
	}
	if !ok {
		return "", false
	}
	var sb strings.Builder
	for i, t := range tokens {
		switch {
		case tokens[0] == "hooks" && (i == 2 || i == 4):
			sb.WriteString("[" + t + "]")
		case i == 0:
			sb.WriteString(t)
		default:
			sb.WriteString("." + t)
		}
	}
	return sb.String(), true
}

// Check reports a sesshin-relevant key of the wrong shape as a *CorruptError
// naming it: a hooks that isn't an object, an event's value that isn't an
// array, a group that isn't an object or whose hooks (when it has one) isn't
// an array, a statusLine that isn't an object, a permissions that isn't an
// object, or a permissions.allow or permissions.ask that isn't an array
// (hooks-spec.md, Registration). Nothing else is checked: a hook that isn't an object, or has
// no string command, is some other tool's.
func Check(root *jsonio.Object) error {
	if v, ok := root.Get("hooks"); ok {
		hooks, ok := v.(*jsonio.Object)
		if !ok {
			return corrupt("hooks is not an object")
		}
		for _, ev := range hooks.Members {
			at := "hooks." + ev.Key
			groups, ok := ev.Value.([]any)
			if !ok {
				return corrupt(at + " is not an array")
			}
			for i, g := range groups {
				at := at + "[" + strconv.Itoa(i) + "]"
				group, ok := g.(*jsonio.Object)
				if !ok {
					return corrupt(at + " is not an object")
				}
				if h, ok := group.Get("hooks"); ok {
					if _, ok := h.([]any); !ok {
						return corrupt(at + ".hooks is not an array")
					}
				}
			}
		}
	}
	if v, ok := root.Get("statusLine"); ok {
		if _, ok := v.(*jsonio.Object); !ok {
			return corrupt("statusLine is not an object")
		}
	}
	if v, ok := root.Get("permissions"); ok {
		perms, ok := v.(*jsonio.Object)
		if !ok {
			return corrupt("permissions is not an object")
		}
		for _, array := range []string{"allow", "ask"} {
			if v, ok := perms.Get(array); ok {
				if _, ok := v.([]any); !ok {
					return corrupt("permissions." + array + " is not an array")
				}
			}
		}
	}
	return nil
}

// Marshal encodes a tree as settings.proposed.json: two-space indent, the
// File format's escaping, key order and number text as stored, and a trailing
// newline (design-spec.md, File format).
func Marshal(tree *jsonio.Object) ([]byte, error) { return jsonio.MarshalFile(tree) }

// clone copies a tree, so a proposal never changes its input.
func clone(v any) any {
	switch v := v.(type) {
	case *jsonio.Object:
		out := &jsonio.Object{Members: make([]jsonio.Member, len(v.Members))}
		for i, m := range v.Members {
			out.Members[i] = jsonio.Member{Key: m.Key, Value: clone(m.Value)}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = clone(e)
		}
		return out
	}
	return v
}
