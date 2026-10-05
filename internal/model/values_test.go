package model

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The hand-written guards are the spec's patterns (design-spec.md, File
// schemas), which sesshin-hook can't compile at initialization. Each is checked
// against the pattern itself, compiled by Go's regexp: RE2 reads these
// patterns as ECMA-262 does once \uXXXX is written \x{XXXX}.
type guardCase struct {
	name    string
	is      func(string) bool
	pattern string
	// not is a lookahead the pattern starts with, (?!not), which RE2 lacks:
	// a string must not match it.
	not string
}

// says compiles the pattern, and returns whether it matches a string.
func (g guardCase) says() func(string) bool {
	re := regexp.MustCompile(g.pattern)
	if g.not == "" {
		return re.MatchString
	}
	not := regexp.MustCompile("^" + g.not)
	return func(s string) bool { return re.MatchString(s) && !not.MatchString(s) }
}

var guards = []guardCase{
	{"uuid", IsUUID, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, ""},
	{"enum", IsEnum, `^[a-z][a-z0-9_]{0,63}$`, ""},
	{"permission_mode", IsPermissionMode, `^[A-Za-z][A-Za-z0-9_]{0,63}$`, ""},
	{"entrypoint", IsEntrypoint, `^[a-z][a-z0-9_-]{0,63}$`, ""},
	{"last_event_type", IsEventType, `^[a-z][a-z0-9_]{0,63}(?::[a-z][a-z0-9_]{0,63})?$`, ""},
	{"text", IsText, `^[^\x{0000}-\x{001F}\x{007F}-\x{009F}\x{2028}\x{2029}]*$`, ""},
	{"job", IsJob, `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`, `[0-9]+$`},
	{"token", IsToken, `^[0-9a-f]{32}$`, ""},
	{"timestamp", timestampShape, `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`, ""},
}

var guardSeeds = []string{
	"", "a", "A", "z", "0", "_", "-", "a0", "a_", "a-", "aB", "Ab", "_a", "0a", "-a", "a:b", "a:", ":b", "a:b:c", "a:B", "A:b", "a::b",
	"acceptEdits", "sdk-cli", "end:prompt_input_exit", "é", "aé", "a b", "a\x00", "a\x1f", "a\x20", "a\x7f", "a\u0080", "a\u0085",
	"a‧", "a ", "a ", "a‪", " ", "\xe2\x80", "😀", "a\nb", "a\tb",
	strings.Repeat("a", 64), strings.Repeat("a", 65), "a:" + strings.Repeat("b", 64), "a:" + strings.Repeat("b", 65),
	strings.Repeat("a", 64) + ":b", strings.Repeat("a", 65) + ":b",
	"3fa85f64-5717-4562-b3fc-2c963f66afa6", "3FA85F64-5717-4562-B3FC-2C963F66AFA6", "3fa85f64-5717-4562-b3fc-2c963f66afa", "3fa85f64-5717-4562-b3fc-2c963f66afa6a",
	"3fa85f64x5717-4562-b3fc-2c963f66afa6", "3fa85f64-5717-4562-b3fc-2c963f66afg6", "g0000000-0000-0000-0000-000000000000",
	"api-review", "12", "0a", "1-2", "a--b", "-", "9", "a1", strings.Repeat("1", 64), strings.Repeat("1", 65), strings.Repeat("a", 62) + "-a", strings.Repeat("a", 63) + "-a",
	"3fa85f6457174562b3fc2c963f66afa6", "3fa85f6457174562b3fc2c963f66afa", "3fa85f6457174562b3fc2c963f66afa6a", "3FA85F6457174562B3FC2C963F66AFA6",
	"2026-10-03T18:31:51Z", "2026-10-03T18:31:51z", "2026-10-03 18:31:51Z", "2026-10-03T18:31:51", "2026-10-03T18:31:51.5Z", "2026-1a-03T18:31:51Z", "x2026-10-03T18:31:51Z",
}

func TestGuards(t *testing.T) {
	for _, g := range guards {
		says := g.says()
		for _, s := range guardSeeds {
			if got, want := g.is(s), says(s); got != want {
				t.Errorf("%s(%q) = %v, pattern says %v", g.name, s, got, want)
			}
		}
	}
}

func FuzzGuards(f *testing.F) {
	for _, s := range guardSeeds {
		f.Add(s)
	}
	says := make([]func(string) bool, len(guards))
	for i, g := range guards {
		says[i] = g.says()
	}
	f.Fuzz(func(t *testing.T, s string) {
		for i, g := range guards {
			if g.name == "text" && !utf8.ValidString(s) {
				continue // the tree holds valid UTF-8 only
			}
			if got, want := g.is(s), says[i](s); got != want {
				t.Errorf("%s(%q) = %v, pattern says %v", g.name, s, got, want)
			}
		}
	})
}

func TestScrub(t *testing.T) {
	for _, tc := range [][2]string{
		{"", ""},
		{"plain/path é 😀", "plain/path é 😀"},
		{"a\nb\r\nc\td\x00e\x1f", "a b  c d e "},
		{"a\x7fb", "a b"},
		{"a\u0080b\u0085c\u009fd e", "a b c d e"},
		{"a b c‪d", "a b c‪d"},
		{"  ", "  "},
		{"\xc2", "\xc2"},
		{"\xe2\x80", "\xe2\x80"},
	} {
		if got := Scrub(tc[0]); got != tc[1] {
			t.Errorf("Scrub(%q) = %q; want %q", tc[0], got, tc[1])
		}
	}
}

// What Scrub returns always passes IsText, has no C1 control, and is valid
// UTF-8 when its input was (review-foundation #4).
func FuzzScrub(f *testing.F) {
	for _, s := range guardSeeds {
		f.Add(s)
	}
	f.Add("a  \u0085\r\n\x7f\xc2\x85\xe2\x80\xa8")
	f.Fuzz(func(t *testing.T, s string) {
		got := Scrub(s)
		if !IsText(got) {
			t.Errorf("Scrub(%q) = %q, not text", s, got)
		}
		if !utf8.ValidString(s) {
			return
		}
		if !utf8.ValidString(got) {
			t.Errorf("Scrub(%q) = %q, not valid UTF-8", s, got)
		}
		for _, r := range got {
			if r >= 0x80 && r <= 0x9F {
				t.Errorf("Scrub(%q) = %q, holds C1 %U", s, got, r)
			}
		}
		if utf8.RuneCountInString(got) != utf8.RuneCountInString(s) {
			t.Errorf("Scrub(%q) = %q: each character should become one space", s, got)
		}
	})
}

func TestIntegerLiteral(t *testing.T) {
	for s, want := range map[string]bool{
		"0": true, "-0": true, "7": true, "-7": true, "10": true, "9007199254740993": true,
		"": false, "-": false, "00": false, "01": false, "-01": false, "1.0": false, "1e0": false, "1E0": false, "+1": false, "1-": false,
	} {
		if got := isIntegerLiteral(s); got != want {
			t.Errorf("isIntegerLiteral(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestTimestamp(t *testing.T) {
	at := time.Date(2026, 10, 3, 12, 31, 51, 999_999_999, time.FixedZone("MDT", -6*3600))
	ts := FormatTimestamp(at)
	if ts != "2026-10-03T18:31:51Z" {
		t.Errorf("FormatTimestamp: %s", ts)
	}
	if got := ts.Time(); !got.Equal(at.Truncate(time.Second)) || got.Location() != time.UTC {
		t.Errorf("Time: %v", got)
	}
}
