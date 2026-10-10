// Package text is defs' text (design-spec.md, File schemas): a string with no
// line break and no control character, the test for one, and Scrub, which
// makes one of any string. It is a leaf, below model and the terminal
// backends, which both scrub what they store.
//
// sesshin-hook links it, so it imports nothing, starts no goroutine, and has
// no init function or package-level initializer that does work
// (implementation-spec.md, The hook binary).
package text

// Is reports whether s is defs' text: no U+0000–U+001F, U+007F–U+009F,
// U+2028, or U+2029 (hooks-spec.md, Reading the payload). s must be valid
// UTF-8, as every string the tree holds is.
func Is(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c < 0x20 || c == 0x7F:
			return false
		case c == 0xC2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] <= 0x9F:
			return false // C1
		case c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9):
			return false // U+2028, U+2029
		}
	}
	return true
}

// Scrub returns s with each C0 control (U+0000–U+001F, line breaks among
// them), DEL (U+007F), C1 control (U+0080–U+009F, NEL among them), U+2028,
// and U+2029 replaced by a space, so the result always passes Is
// (hooks-spec.md, Reading the payload). s is returned as is when it holds
// none of them.
func Scrub(s string) string {
	var b []byte
	for i := 0; i < len(s); {
		n := controlAt(s, i)
		if n == 0 {
			if b != nil {
				b = append(b, s[i])
			}
			i++
			continue
		}
		if b == nil {
			b = append(make([]byte, 0, len(s)), s[:i]...)
		}
		b = append(b, ' ')
		i += n
	}
	if b == nil {
		return s
	}
	return string(b)
}

// controlAt is the length in bytes of the character Scrub replaces at s[i],
// or 0 when there is none there.
func controlAt(s string, i int) int {
	switch c := s[i]; {
	case c < 0x20 || c == 0x7F:
		return 1
	case c == 0xC2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] <= 0x9F:
		return 2 // C1
	case c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9):
		return 3 // U+2028, U+2029
	}
	return 0
}
