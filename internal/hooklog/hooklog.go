package hooklog

import (
	"os"
	"strconv"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/phansen314/sesshin/internal/fsys"
)

// The log's names in the state directory, and the size past which it is
// rotated (hooks-spec.md, Log).
const (
	FileName    = "hooks.log"
	RotatedName = "hooks.log.1"
	MaxSize     = 1 << 20
)

// MaxMessage caps a message, in bytes, before escaping: a line stays one
// write that a person can read, whatever a hook is handed.
const MaxMessage = 2048

// Append appends one line to state's hooks.log: `<timestamp> <verb>
// <session-uuid or -> <message>`, timestamp now in UTC whole seconds. An
// empty verb or session is written "-". Each field is escaped onto one line,
// and the message is cut at MaxMessage bytes.
//
// The line is one O_APPEND write, so concurrent hooks never interleave
// within it. Then, if the file is past MaxSize, it is rotated to
// hooks.log.1 (see rotate). Nothing here takes a lock but rotation's
// try-lock. The error is for tests: a hook has nowhere to report it.
func Append(state fsys.Root, now time.Time, verb, session, msg string) error {
	return appendLimit(state, now, verb, session, msg, MaxSize)
}

func appendLimit(state fsys.Root, now time.Time, verb, session, msg string, limit int64) error {
	f, err := state.OpenAppend(FileName)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(Line(now, verb, session, msg)); err != nil {
		return err
	}
	return rotate(state, f, limit)
}

// rotate renames hooks.log to hooks.log.1, replacing it, when f, the
// descriptor this hook appended through, shows the file past limit
// (implementation-spec.md, Log rotation). Two hooks that notice at once
// mustn't both rename: the second would move a fresh, nearly empty log over
// the full one. So the rename is made under a try-lock on f, and only if
// the path still names f's file and it is still past the limit. A hook that
// finds the lock held skips: another is rotating. One that gets it after
// another's rename finds the path naming a new file, or nothing, and skips.
// The lock is released when f is closed.
func rotate(state fsys.Root, f fsys.AppendFile, limit int64) error {
	fi, err := f.Stat()
	if err != nil || fi.Size() <= limit {
		return err
	}
	if err := f.TryLock(); err != nil {
		if errno, ok := fsys.ErrnoOf(err); ok && errno == syscall.EAGAIN {
			return nil
		}
		return err
	}
	there, err := state.Stat(FileName)
	if err != nil {
		if errno, ok := fsys.ErrnoOf(err); ok && errno == syscall.ENOENT {
			return nil
		}
		return err
	}
	if !os.SameFile(fi, there) {
		return nil
	}
	if fi, err = f.Stat(); err != nil || fi.Size() <= limit {
		return err
	}
	return state.Rename(FileName, RotatedName)
}

// Line is one entry, newline included.
func Line(now time.Time, verb, session, msg string) []byte {
	b := make([]byte, 0, 64+len(verb)+len(session)+len(msg))
	b = now.UTC().Truncate(time.Second).AppendFormat(b, "2006-01-02T15:04:05Z")
	b = append(b, ' ')
	b = field(b, verb)
	b = append(b, ' ')
	b = field(b, session)
	b = append(b, ' ')
	if len(msg) > MaxMessage {
		cut := MaxMessage
		for cut > 0 && !utf8.RuneStart(msg[cut]) {
			cut--
		}
		msg = msg[:cut] + "…"
	}
	b = escape(b, msg, false)
	return append(b, '\n')
}

// field appends a verb or session: "-" when empty, and a space escaped, so
// a line always splits into its four parts at its first three spaces.
func field(b []byte, s string) []byte {
	if s == "" {
		return append(b, '-')
	}
	return escape(b, s, true)
}

// escape appends s with every character that could break a line, or hide in
// one, escaped: \n, \r, \t, \\, \xNN for the rest of C0 and DEL,
// \uNNNN for C1 (NEL among them), U+2028, and U+2029, which some viewers
// break lines at; with space, a space is \x20. Everything else is written as
// is, invalid UTF-8 included: the log is for people, not a parser.
func escape(b []byte, s string, space bool) []byte {
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b = append(b, s[i])
		case r == '\n':
			b = append(b, `\n`...)
		case r == '\r':
			b = append(b, `\r`...)
		case r == '\t':
			b = append(b, `\t`...)
		case r == '\\':
			b = append(b, `\\`...)
		case r == ' ' && space:
			b = append(b, `\x20`...)
		case r < 0x20 || r == 0x7f:
			b = append(b, `\x`...)
			b = appendHex(b, r, 2)
		case (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029:
			b = append(b, `\u`...)
			b = appendHex(b, r, 4)
		default:
			b = append(b, s[i:i+n]...)
		}
		i += n
	}
	return b
}

// appendHex appends r in lowercase hex, zero-padded to width digits.
func appendHex(b []byte, r rune, width int) []byte {
	h := strconv.FormatInt(int64(r), 16)
	for range width - len(h) {
		b = append(b, '0')
	}
	return append(b, h...)
}
