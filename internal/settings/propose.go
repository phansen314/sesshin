package settings

import (
	"bytes"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Action is what a proposal does to one of sesshin's entries.
type Action string

const (
	Added     Action = "added"
	Replaced  Action = "replaced"
	Unchanged Action = "unchanged"
	Removed   Action = "removed"
)

// Change is one item of an operation's changes: What is hooks.<Event>:<verb>,
// statusLine, or permissions.<allow|ask>:<rule>.
type Change struct {
	What   string
	Action Action
}

// Proposal is a proposed settings.json and what it changes.
type Proposal struct {
	Tree    *jsonio.Object
	Changes []Change
	// StatusLineReplaced is set when ProposeInstall replaced a statusLine
	// that was not sesshin's: the status-line-replaced warning.
	StatusLineReplaced bool
}

// entry is one hook that is sesshin's, found in the tree.
type entry struct {
	event  string
	verb   string
	group  *jsonio.Object
	hook   *jsonio.Object
	nHooks int // the group's hooks when scanned
}

// scan returns sesshin's hook entries in settings.json order. A hook that isn't
// an object, or has no string command, is not sesshin's. The tree must pass Check.
func scan(root *jsonio.Object, ours func(string) bool) []entry {
	v, ok := root.Get("hooks")
	if !ok {
		return nil
	}
	var out []entry
	for _, ev := range v.(*jsonio.Object).Members {
		for _, g := range ev.Value.([]any) {
			group := g.(*jsonio.Object)
			hv, _ := group.Get("hooks")
			hooks, _ := hv.([]any)
			for _, h := range hooks {
				hook, ok := h.(*jsonio.Object)
				if !ok {
					continue
				}
				if verb, ok := commandVerb(hook, ours); ok {
					out = append(out, entry{ev.Key, verb, group, hook, len(hooks)})
				}
			}
		}
	}
	return out
}

// commandVerb returns the verb of o's command when that is sesshin's.
func commandVerb(o *jsonio.Object, ours func(string) bool) (string, bool) {
	cv, _ := o.Get("command")
	cmd, ok := cv.(string)
	if !ok {
		return "", false
	}
	return verbOf(cmd, ours)
}

func what(event, verb string) string { return "hooks." + event + ":" + verb }

// sameBytes reports whether two tree values encode to the same bytes: keys in
// the same order, numbers with the same text.
func sameBytes(a, b *jsonio.Object) bool {
	ab, err1 := a.MarshalJSON()
	bb, err2 := b.MarshalJSON()
	return err1 == nil && err2 == nil && bytes.Equal(ab, bb)
}

// emptyMatcher reports a group whose matcher is "" or absent.
func emptyMatcher(g *jsonio.Object) bool {
	m, ok := g.Get("matcher")
	return !ok || m == ""
}

type eventVerb struct{ event, verb string }

// ProposeInstall returns tree (which it does not change) with sesshin wired in,
// to run hookBinary: each registered hook, and the statusLine, as
// hooks-spec.md's Registration and operations.md's install say. ours says
// which paths are sesshin-hook. Changes are in operations.md's Order. The error
// is a *CorruptError when tree fails Check.
//
// Of sesshin's entries for a registered event and verb, the first in
// settings.json order is kept, and the rest removed. A kept entry whose
// whole group is byte-identical to sesshin's is unchanged. Otherwise it is
// replaced: its group rewritten in place when sesshin's hook is the group's only
// hook and the matcher is "" or absent, else the hook is taken out of its
// group, which keeps its other hooks, and sesshin's own group is appended to the
// event's array. A registered command with no entry is appended. Entries
// under other events, or with other verbs, are removed. A group, event, or
// hooks that a removal left empty is removed.
//
// statusLine is added when absent. When sesshin's, its other keys are kept and
// type and command set. Otherwise it is replaced wholesale, with
// StatusLineReplaced.
//
// Each permission rule missing from its array is appended to it (permissions,
// allow, and ask created when absent); nothing else in permissions changes.
func ProposeInstall(tree *jsonio.Object, hookBinary string, ours func(string) bool) (Proposal, error) {
	if err := Check(tree); err != nil {
		return Proposal{}, err
	}
	root := clone(tree).(*jsonio.Object)
	entries := scan(root, ours)

	regs := Registrations()
	registered := map[eventVerb]bool{}
	for _, r := range regs {
		registered[eventVerb{r.Event, r.Verb}] = true
	}
	first := map[eventVerb]*entry{}
	var extra []*entry
	for i := range entries {
		e := &entries[i]
		k := eventVerb{e.event, e.verb}
		if registered[k] && first[k] == nil {
			first[k] = e
		} else {
			extra = append(extra, e)
		}
	}

	var p Proposal
	removed := map[*jsonio.Object]bool{}
	var appends []appended
	for _, r := range regs {
		want := sesshinGroup(hookBinary, r)
		w := what(r.Event, r.Verb)
		e := first[eventVerb{r.Event, r.Verb}]
		switch {
		case e == nil:
			appends = append(appends, appended{r.Event, want})
			p.Changes = append(p.Changes, Change{w, Added})
		case sameBytes(e.group, want):
			p.Changes = append(p.Changes, Change{w, Unchanged})
		case e.nHooks == 1 && emptyMatcher(e.group):
			e.group.Members = want.Members
			p.Changes = append(p.Changes, Change{w, Replaced})
		default:
			removed[e.hook] = true
			appends = append(appends, appended{r.Event, want})
			p.Changes = append(p.Changes, Change{w, Replaced})
		}
	}
	for _, e := range extra {
		removed[e.hook] = true
		p.Changes = append(p.Changes, Change{what(e.event, e.verb), Removed})
	}
	applyRemovals(root, entries, removed, appends)

	p.Changes = append(p.Changes, installStatusLine(root, &p, hookBinary, ours))
	p.Changes = append(p.Changes, installPermissions(root)...)
	p.Tree = root
	return p, nil
}

type appended struct {
	event string
	group *jsonio.Object
}

// installStatusLine sets root's statusLine and returns its change.
func installStatusLine(root *jsonio.Object, p *Proposal, hookBinary string, ours func(string) bool) Change {
	c := Change{"statusLine", Replaced}
	v, ok := root.Get("statusLine")
	if !ok {
		root.Set("statusLine", sesshinStatusLine(hookBinary))
		c.Action = Added
		return c
	}
	sl := v.(*jsonio.Object)
	if verb, ok := commandVerb(sl, ours); ok && verb == StatusLineVerb {
		typ, _ := sl.Get("type")
		cmd, _ := sl.Get("command")
		if typ == "command" && cmd == command(hookBinary, StatusLineVerb) {
			c.Action = Unchanged
			return c
		}
		sl.Set("type", "command")
		sl.Set("command", command(hookBinary, StatusLineVerb))
		return c
	}
	root.Set("statusLine", sesshinStatusLine(hookBinary))
	p.StatusLineReplaced = true
	return c
}

// ProposeUninstall returns tree (which it does not change) without sesshin's
// entries: every hook entry that is sesshin's, each a removed item in settings.json
// order, then statusLine, removed while it is sesshin's. A statusLine that isn't
// sesshin's is left alone, and reported unchanged; an absent one has no item.
// Emptied groups, events, and hooks are removed, as for install.
func ProposeUninstall(tree *jsonio.Object, ours func(string) bool) (Proposal, error) {
	if err := Check(tree); err != nil {
		return Proposal{}, err
	}
	root := clone(tree).(*jsonio.Object)
	entries := scan(root, ours)
	removed := map[*jsonio.Object]bool{}
	var p Proposal
	for _, e := range entries {
		removed[e.hook] = true
		p.Changes = append(p.Changes, Change{what(e.event, e.verb), Removed})
	}
	applyRemovals(root, entries, removed, nil)
	if v, ok := root.Get("statusLine"); ok {
		c := Change{"statusLine", Unchanged}
		if verb, ok := commandVerb(v.(*jsonio.Object), ours); ok && verb == StatusLineVerb {
			root.Delete("statusLine")
			c.Action = Removed
		}
		p.Changes = append(p.Changes, c)
	}
	p.Changes = append(p.Changes, uninstallPermissions(root)...)
	p.Tree = root
	return p, nil
}

// applyRemovals takes the removed hooks out of their groups, appends the new
// groups to their events (creating events and hooks as needed), then removes
// each group a removal emptied, each event that left without groups, and
// hooks if it was left empty. Appending comes first, so an event whose only
// group is replaced keeps its place.
func applyRemovals(root *jsonio.Object, entries []entry, removed map[*jsonio.Object]bool, appends []appended) {
	touched := map[*jsonio.Object]bool{}
	for _, e := range entries {
		if !removed[e.hook] || touched[e.group] {
			continue
		}
		touched[e.group] = true
		hv, _ := e.group.Get("hooks")
		var kept []any
		for _, h := range hv.([]any) {
			if ho, ok := h.(*jsonio.Object); !ok || !removed[ho] {
				kept = append(kept, h)
			}
		}
		if kept == nil {
			kept = []any{}
		}
		e.group.Set("hooks", kept)
	}

	if len(appends) > 0 {
		hv, ok := root.Get("hooks")
		hooks, _ := hv.(*jsonio.Object)
		if !ok {
			hooks = &jsonio.Object{}
			root.Set("hooks", hooks)
		}
		for _, a := range appends {
			ev, _ := hooks.Get(a.event)
			groups, _ := ev.([]any)
			hooks.Set(a.event, append(groups[:len(groups):len(groups)], a.group))
		}
	}

	if len(touched) == 0 {
		return
	}
	hooks := mustObject(root, "hooks")
	emptied := false
	for _, ev := range append([]jsonio.Member(nil), hooks.Members...) {
		groups := ev.Value.([]any)
		kept := make([]any, 0, len(groups))
		for _, g := range groups {
			group := g.(*jsonio.Object)
			if hv, _ := group.Get("hooks"); touched[group] && len(hv.([]any)) == 0 {
				continue
			}
			kept = append(kept, g)
		}
		switch {
		case len(kept) == len(groups):
		case len(kept) == 0:
			hooks.Delete(ev.Key)
			emptied = true
		default:
			hooks.Set(ev.Key, kept)
		}
	}
	if emptied && hooks.Len() == 0 {
		root.Delete("hooks")
	}
}

func mustObject(o *jsonio.Object, key string) *jsonio.Object {
	v, _ := o.Get(key)
	return v.(*jsonio.Object)
}
