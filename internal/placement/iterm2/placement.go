package iterm2

import (
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Tag is the value of the placement's terminal key.
const Tag = "iterm2"

// Keys of the iTerm2 placement (design-spec.md, The iTerm2 placement).
const (
	keyTerminal = "terminal"
	keySession  = "session_id"
)

// Recognize returns the iTerm2 placement the environment names, or nil. goos
// is the system's name: only macOS has iTerm2, and a forwarded
// ITERM_SESSION_ID (over ssh, say) names nothing there. TERM_PROGRAM must be
// iTerm.app, since no other terminal clears ITERM_SESSION_ID, and
// ITERM_SESSION_ID must be <prefix>:<UUID>, the UUID after its last colon, in
// either case; it is kept in capitals. TMUX and STY (placement.Multiplexed)
// and a nested claude are ruled out before the backend is asked, not by it.
func Recognize(goos string, getenv func(string) string) *jsonio.Object {
	if goos != "darwin" || getenv("TERM_PROGRAM") != "iTerm.app" {
		return nil
	}
	id := getenv("ITERM_SESSION_ID")
	i := strings.LastIndexByte(id, ':')
	if i < 0 || !isUUID(id[i+1:]) {
		return nil
	}
	return PlacementOf(id[i+1:])
}

// PlacementOf is the placement of the session with the UUID given, as
// Recognize builds one: the UUID in capitals.
func PlacementOf(uuid string) *jsonio.Object {
	return &jsonio.Object{Members: []jsonio.Member{
		{Key: keyTerminal, Value: Tag},
		{Key: keySession, Value: strings.ToUpper(uuid)},
	}}
}

// Parse validates a placement read from sesshin.json, which model checks only
// for its terminal tag. It is valid when the tag is iterm2 and session_id is
// a string holding a UUID, in either case; the UUID returned is in capitals.
// Anything else, nil included, reports false: the caller treats the placement
// as null. Unknown keys are ignored.
func Parse(p *jsonio.Object) (uuid string, ok bool) {
	if p == nil {
		return "", false
	}
	if t, ok := p.Get(keyTerminal); !ok || t != Tag {
		return "", false
	}
	v, _ := p.Get(keySession)
	s, isStr := v.(string)
	if !isStr || !isUUID(s) {
		return "", false
	}
	return strings.ToUpper(s), true
}

// isUUID reports whether s is a UUID in the 8-4-4-4-12 form, in either case.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}
