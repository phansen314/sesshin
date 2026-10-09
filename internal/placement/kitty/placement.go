package kitty

import (
	"encoding/json"
	"strconv"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
)

// Tag is the value of the placement's terminal key.
const Tag = "kitty"

// Keys of the kitty placement (design-spec.md, The kitty placement).
const (
	keyTerminal = "terminal"
	keySocket   = "socket"
	keyWindow   = "window_id"
	keyTabTitle = "tab_title"
	keyUserVars = "user_vars"
)

// Recognize returns the kitty placement the environment names, or nil: not
// in kitty, remote control off (KITTY_WINDOW_ID without KITTY_LISTEN_ON), or
// a KITTY_WINDOW_ID that is not a positive integer. The socket is kept
// verbatim, kitty's placeholders and all. TMUX and STY (placement.Multiplexed)
// and a nested claude are ruled out before the backend is asked, not by it.
func Recognize(getenv func(string) string) *jsonio.Object {
	want, ok := RecognizeParsed(getenv)
	if !ok {
		return nil
	}
	return PlacementOf(want.Socket, want.WindowID)
}

// PlacementOf is the placement of window id on socket, as Recognize builds
// one from the environment.
func PlacementOf(socket string, id int64) *jsonio.Object {
	return &jsonio.Object{Members: []jsonio.Member{
		{Key: keyTerminal, Value: Tag},
		{Key: keySocket, Value: socket},
		{Key: keyWindow, Value: json.Number(strconv.FormatInt(id, 10))},
	}}
}

// RecognizeParsed is Recognize's answer in its parsed form: the socket and
// window the environment names, and whether it names any.
func RecognizeParsed(getenv func(string) string) (Parsed, bool) {
	socket := getenv("KITTY_LISTEN_ON")
	id, ok := windowID(getenv("KITTY_WINDOW_ID"))
	if socket == "" || !ok {
		return Parsed{}, false
	}
	return Parsed{Socket: socket, WindowID: id}, true
}

// Replace returns next with the sync-only keys, tab_title and user_vars, of
// old, when old is a valid kitty placement of the same socket and window, or
// the session is resumed: resume opens its tab with exactly those keys. They
// describe the window, so otherwise another window's are dropped: sync
// writes the right ones at the next prompt. A nil next is nil, and a nil or
// invalid old keeps nothing. Neither argument is changed.
func Replace(next, old *jsonio.Object, resumed bool) *jsonio.Object {
	if next == nil {
		return nil
	}
	n, ok := Parse(next)
	if !ok {
		return next
	}
	o, ok := Parse(old)
	if !ok || !resumed && (o.Socket != n.Socket || o.WindowID != n.WindowID) {
		return next
	}
	out := &jsonio.Object{Members: append([]jsonio.Member(nil), next.Members...)}
	for _, k := range []string{keyTabTitle, keyUserVars} {
		if v, ok := old.Get(k); ok {
			out.Set(k, v)
		}
	}
	return out
}

// Stored returns the tab_title ("" when it has none) and the user_vars, in
// order, of a valid kitty placement, as resume reopens a tab with them; ok is
// false for any placement Parse rejects, nil included. Only the sync writes
// them.
func Stored(p *jsonio.Object) (title string, vars []placement.Var, ok bool) {
	if _, ok := Parse(p); !ok {
		return "", nil, false
	}
	if v, ok := p.Get(keyTabTitle); ok {
		title, _ = v.(string)
	}
	if v, ok := p.Get(keyUserVars); ok {
		o, _ := stringMembers(v)
		for _, m := range o.Members {
			vars = append(vars, placement.Var{Name: m.Key, Value: m.Value.(string)})
		}
	}
	return title, vars, true
}

// Parsed is a valid kitty placement's identifying keys: the window it names.
type Parsed struct {
	Socket   string
	WindowID int64
}

// Parse validates a placement read from sesshin.json, which model checks only
// for its terminal tag (design-spec.md, sesshin.json). It is valid when the tag
// is kitty, socket is a non-empty string, window_id is a positive integer,
// tab_title, when present, is a string, and user_vars, when present, is an
// object of strings. Anything else, nil included, reports false: the caller
// treats the placement as null. Unknown keys are ignored.
func Parse(p *jsonio.Object) (Parsed, bool) {
	if p == nil {
		return Parsed{}, false
	}
	if t, ok := p.Get(keyTerminal); !ok || t != Tag {
		return Parsed{}, false
	}
	socket, ok := p.Get(keySocket)
	s, isStr := socket.(string)
	if !ok || !isStr || s == "" {
		return Parsed{}, false
	}
	w, ok := p.Get(keyWindow)
	num, isNum := w.(json.Number)
	if !ok || !isNum {
		return Parsed{}, false
	}
	id, ok := windowID(string(num))
	if !ok {
		return Parsed{}, false
	}
	if v, ok := p.Get(keyTabTitle); ok {
		if _, isStr := v.(string); !isStr {
			return Parsed{}, false
		}
	}
	if v, ok := p.Get(keyUserVars); ok {
		if _, ok := stringMembers(v); !ok {
			return Parsed{}, false
		}
	}
	return Parsed{Socket: s, WindowID: id}, true
}

// windowID reads s as a positive integer in plain decimal, as kitty writes
// one: no sign, no leading zero, no fraction or exponent.
func windowID(s string) (int64, bool) {
	if s == "" || s[0] == '0' || s[0] == '+' {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 || id > model.MaxSafe {
		return 0, false
	}
	return id, true
}

// stringMembers reports whether v is an object whose values are all strings.
func stringMembers(v any) (*jsonio.Object, bool) {
	o, ok := v.(*jsonio.Object)
	if !ok || o == nil {
		return nil, false
	}
	for _, m := range o.Members {
		if _, ok := m.Value.(string); !ok {
			return nil, false
		}
	}
	return o, true
}
