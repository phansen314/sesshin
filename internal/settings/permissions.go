package settings

import "github.com/phansen314/sesshin/internal/jsonio"

// installPermissions appends each of sesshin's permission rules missing from its
// array in root, creating permissions, allow, or ask at the end of their
// parent, and returns one change per rule, in the table's order. Whole
// strings are compared; items that aren't strings are left where they are.
// root must pass Check.
func installPermissions(root *jsonio.Object) []Change {
	var out []Change
	for _, r := range PermissionRules() {
		perms, ok := objectAt(root, "permissions")
		if !ok {
			perms = &jsonio.Object{}
			root.Set("permissions", perms)
		}
		var items []any
		if v, ok := perms.Get(r.Array); ok {
			items = v.([]any)
		}
		c := Change{permissionWhat(r), Unchanged}
		if !hasString(items, r.Rule) {
			perms.Set(r.Array, append(items[:len(items):len(items)], r.Rule))
			c.Action = Added
		}
		out = append(out, c)
	}
	return out
}

// uninstallPermissions removes every copy of sesshin's rules from allow and ask
// in root, then each array the removal emptied, then permissions if that left
// it empty, and returns one removed change per rule found: allow before ask,
// each in the table's order. root must pass Check.
func uninstallPermissions(root *jsonio.Object) []Change {
	perms, ok := objectAt(root, "permissions")
	if !ok {
		return nil
	}
	var out []Change
	emptied := false
	for _, array := range []string{"allow", "ask"} {
		v, ok := perms.Get(array)
		if !ok {
			continue
		}
		items := v.([]any)
		kept := make([]any, 0, len(items))
		for _, it := range items {
			if s, ok := it.(string); !ok || !sesshinsRule(array, s) {
				kept = append(kept, it)
			}
		}
		if len(kept) == len(items) {
			continue
		}
		for _, r := range PermissionRules() {
			if r.Array == array && hasString(items, r.Rule) {
				out = append(out, Change{permissionWhat(r), Removed})
			}
		}
		if len(kept) == 0 {
			perms.Delete(array)
			emptied = true
		} else {
			perms.Set(array, kept)
		}
	}
	if emptied && perms.Len() == 0 {
		root.Delete("permissions")
	}
	return out
}

// objectAt returns the object at key, when there is one.
func objectAt(o *jsonio.Object, key string) (*jsonio.Object, bool) {
	v, _ := o.Get(key)
	obj, ok := v.(*jsonio.Object)
	return obj, ok
}

func hasString(items []any, s string) bool {
	for _, it := range items {
		if it == s {
			return true
		}
	}
	return false
}

// sesshinsRule reports whether s is one of sesshin's rules for array.
func sesshinsRule(array, s string) bool {
	for _, r := range PermissionRules() {
		if r.Array == array && r.Rule == s {
			return true
		}
	}
	return false
}
