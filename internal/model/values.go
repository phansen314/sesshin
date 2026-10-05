package model

import (
	"time"
	"unicode/utf8"
)

// Timestamp is a time as sesshin stores it: UTC, whole seconds, a Z suffix
// (design-spec.md, Timestamps). One read from a file has passed the
// validator, so it names a real time.
type Timestamp string

const timestampLayout = "2006-01-02T15:04:05Z"

// FormatTimestamp returns t as a Timestamp, truncated to the second.
func FormatTimestamp(t time.Time) Timestamp {
	return Timestamp(t.UTC().Format(timestampLayout))
}

// Time returns ts as a time. ts must be valid, as every Timestamp read from a
// file or made by FormatTimestamp is; otherwise it returns the zero time.
func (ts Timestamp) Time() time.Time {
	t, _ := time.Parse(timestampLayout, string(ts))
	return t
}

// Timestamp checks that v, at ptr, is a timestamp: the schema's pattern, then
// a real time (no 02-30, no hour 24), a rule beyond it.
func (p *Problems) Timestamp(v any, ptr string) (Timestamp, bool) {
	s, ok := p.String(v, ptr)
	if !ok {
		return "", false
	}
	if !timestampShape(s) {
		p.Add(ptr, "must be a timestamp: YYYY-MM-DDTHH:MM:SSZ")
		return "", false
	}
	if _, err := time.Parse(timestampLayout, s); err != nil {
		p.AddAdditional(ptr, "must be a real date and time")
		return "", false
	}
	return Timestamp(s), true
}

// timestampShape is defs' timestamp pattern,
// ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$.
func timestampShape(s string) bool {
	const shape = "dddd-dd-ddTdd:dd:ddZ"
	if len(s) != len(shape) {
		return false
	}
	for i := range len(shape) {
		if shape[i] == 'd' {
			if s[i] < '0' || s[i] > '9' {
				return false
			}
		} else if s[i] != shape[i] {
			return false
		}
	}
	return true
}

// IsUUID reports whether s is a lowercase UUID: defs' uuid pattern, and a
// session directory's name (design-spec.md, Session UUIDs).
func IsUUID(s string) bool {
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
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}

// IsJob reports whether s is a job name: defs' job pattern,
// ^(?![0-9]+$)[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$ (design-spec.md,
// Reservations). Not all digits, so 12 always means a sesshin ID.
func IsJob(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	digits := true
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			digits = false
		case c >= '0' && c <= '9':
		case c == '-' && i > 0 && i < len(s)-1:
			digits = false
		default:
			return false
		}
	}
	return !digits
}

// JobKey is the job with ASCII A-Z lowercased: what names its reservation
// file and what every held-job check compares (design-spec.md, Reservations).
// Jobs that differ only in case are one job.
func JobKey(s string) string {
	for i := range len(s) {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for ; i < len(b); i++ {
				if b[i] >= 'A' && b[i] <= 'Z' {
					b[i] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// IsJobKey reports whether s is a job key: a job already lowercased.
func IsJobKey(s string) bool { return IsJob(s) && JobKey(s) == s }

// IsToken reports whether s is a reservation token: 32 lowercase hex
// characters.
func IsToken(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := range len(s) {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// IsEnum reports whether s passes the open-set shape guard,
// ^[a-z][a-z0-9_]{0,63}$ (design-spec.md, Open sets).
func IsEnum(s string) bool { return guard(s, false, false) }

// IsPermissionMode reports whether s passes permission_mode's guard,
// ^[A-Za-z][A-Za-z0-9_]{0,63}$: Claude Code's modes are camelCase.
func IsPermissionMode(s string) bool { return guard(s, true, false) }

// IsEntrypoint reports whether s passes entrypoint's guard,
// ^[a-z][a-z0-9_-]{0,63}$: CLAUDE_CODE_ENTRYPOINT's values have hyphens.
func IsEntrypoint(s string) bool { return guard(s, false, true) }

// guard is the shape guards' one pattern: a letter, then up to 63 letters,
// digits, and underscores; capitals and hyphens only when allowed.
func guard(s string, upper, hyphen bool) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', upper && c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_' || hyphen && c == '-'):
		default:
			return false
		}
	}
	return true
}

// IsEventType reports whether s is a last_event_type: an open-set value,
// optionally followed by a colon and another,
// ^[a-z][a-z0-9_]{0,63}(?::[a-z][a-z0-9_]{0,63})?$.
func IsEventType(s string) bool {
	for i := range len(s) {
		if s[i] == ':' {
			return IsEnum(s[:i]) && IsEnum(s[i+1:])
		}
	}
	return IsEnum(s)
}

// IsText reports whether s is defs' text: no U+0000–U+001F, U+007F–U+009F,
// U+2028, or U+2029 (hooks-spec.md, Reading the payload). s must be valid
// UTF-8, as every string the tree holds is.
func IsText(s string) bool {
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
// and U+2029 replaced by a space, so the result always passes IsText
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

// text checks that v, at ptr, is defs' text.
func (p *Problems) text(v any, ptr string) (string, bool) {
	s, ok := p.String(v, ptr)
	if ok && !IsText(s) {
		p.Add(ptr, "must not contain line breaks or control characters")
		return "", false
	}
	return s, ok
}

// guarded checks that v, at ptr, is a string that passes is.
func (p *Problems) guarded(v any, ptr string, is func(string) bool, reason string) (string, bool) {
	s, ok := p.String(v, ptr)
	if ok && !is(s) {
		p.Add(ptr, reason)
		return "", false
	}
	return s, ok
}

// Reasons for the shape guards.
const (
	reasonEnum           = "must match ^[a-z][a-z0-9_]{0,63}$"
	reasonPermissionMode = "must match ^[A-Za-z][A-Za-z0-9_]{0,63}$"
	reasonEntrypoint     = "must match ^[a-z][a-z0-9_-]{0,63}$"
	reasonEventType      = "must be <verb> or <verb>:<qualifier>, each matching ^[a-z][a-z0-9_]{0,63}$"
	reasonUUID           = "must be a lowercase UUID"
	reasonJob            = "must match ^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$ and not be all digits"
	reasonToken          = "must be 32 lowercase hex characters"
	reasonAbsolute       = "must be an absolute path"
)

// absolute checks that v, at ptr, is a string starting with "/".
func (p *Problems) absolute(v any, ptr string) (string, bool) {
	return p.guarded(v, ptr, func(s string) bool { return len(s) > 0 && s[0] == '/' }, reasonAbsolute)
}

// pidStartedAtMax is pid_started_at's maxLength, in code points.
const pidStartedAtMax = 128

// pidStartedAt checks that v, at ptr, is a non-null pid_started_at.
func (p *Problems) pidStartedAt(v any, ptr string) (string, bool) {
	s, ok := p.String(v, ptr)
	if ok && utf8.RuneCountInString(s) > pidStartedAtMax {
		p.Add(ptr, "must be at most 128 characters")
		return "", false
	}
	return s, ok
}
