package settings

import (
	"path"
	"strings"
)

// Split splits a command line into words by POSIX shell rules: single quotes,
// double quotes (with the backslash escapes \$ \` \" \\ and \newline), and a
// backslash outside quotes. It is deliberately conservative: ok is false for
// anything a shell might expand or treat specially, so that such a command is
// never taken for sesshin's. That is, unquoted: $ ` * ? [ { } ; | & < > ( ), a
// newline (or a backslash before one) or any other control character, a ~ or
// # that starts a word, and a leading name=value word (an assignment, which
// makes the next word the command); and inside double quotes, $ or `. An
// unterminated quote or a trailing backslash is not ok either.
func Split(s string) (words []string, ok bool) {
	if assignment(strings.TrimLeft(s, " \t")) {
		return nil, false
	}
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
			i++
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			inWord = true
			i += j + 2
		case c == '"':
			inWord = true
			i++
		quoted:
			for {
				if i >= len(s) {
					return nil, false
				}
				switch c := s[i]; c {
				case '"':
					i++
					break quoted
				case '$', '`':
					return nil, false
				case '\\':
					if i+1 >= len(s) {
						return nil, false
					}
					switch n := s[i+1]; n {
					case '$', '`', '"', '\\':
						cur.WriteByte(n)
						i += 2
					case '\n':
						i += 2
					default:
						cur.WriteByte('\\')
						i++
					}
				default:
					cur.WriteByte(c)
					i++
				}
			}
		case c == '\\':
			if i+1 >= len(s) || s[i+1] == '\n' {
				return nil, false
			}
			cur.WriteByte(s[i+1])
			inWord = true
			i += 2
		default:
			if special(c) || !inWord && (c == '~' || c == '#') {
				return nil, false
			}
			cur.WriteByte(c)
			inWord = true
			i++
		}
	}
	flush()
	return words, true
}

// special reports a byte a shell treats specially anywhere outside quotes.
func special(c byte) bool {
	return c < 0x20 || c == 0x7f || strings.IndexByte("$`*?[{};|&<>()", c) >= 0
}

// assignment reports whether s starts with an unquoted name=: a shell word
// that sets a variable for the next word.
func assignment(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '=':
			return i > 0
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return false
}

// Quote returns p as one shell word: as is when it holds only A-Za-z0-9/._-,
// else single-quoted, an embedded single quote written as quote, backslash,
// quote, quote (hooks-spec.md, Registration).
// Split(Quote(p)) is [p].
func Quote(p string) string {
	safe := p != ""
	for i := 0; i < len(p); i++ {
		c := p[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("/._-", c) >= 0) {
			safe = false
			break
		}
	}
	if safe {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// SesshinHookName reports whether p's base name is sesshin-hook: the any-path
// member of operations.md's sesshin's entries test. Callers add the paths they
// know (this sesshin's sesshin-hook, install.json's hook_binary).
func SesshinHookName(p string) bool {
	return p != "" && !strings.HasSuffix(p, "/") && path.Base(p) == "sesshin-hook"
}

// verbOf returns the verb of a command that is sesshin's: one that splits into
// exactly [P, verb] with ours(P).
func verbOf(command string, ours func(string) bool) (verb string, ok bool) {
	words, ok := Split(command)
	if !ok || len(words) != 2 || !ours(words[0]) {
		return "", false
	}
	return words[1], true
}
