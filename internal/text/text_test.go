package text

import (
	"testing"
	"unicode/utf8"
)

func TestScrub(t *testing.T) {
	for _, tc := range [][2]string{
		{"", ""},
		{"plain/path é 😀", "plain/path é 😀"},
		{"a\nb\r\nc\td\x00e\x1f", "a b  c d e "},
		{"a\x7fb", "a b"},
		{"a\u0080b\u0085c\u009fd e", "a b c d e"},
		{"a b c\u202ad", "a b c\u202ad"},
		{"  ", "  "},
		{"\xc2", "\xc2"},
		{"\xe2\x80", "\xe2\x80"},
	} {
		if got := Scrub(tc[0]); got != tc[1] {
			t.Errorf("Scrub(%q) = %q; want %q", tc[0], got, tc[1])
		}
	}
}

// What Scrub returns always passes Is, has no C1 control, and is valid
// UTF-8 when its input was.
func FuzzScrub(f *testing.F) {
	for _, s := range []string{"", "a", "é", "a b", "a\x00", "a\x1f", "a\x7f", "a\u0080", "a\u0085", "a\u2028", "a\u2029", "a\u202a", "\xe2\x80", "😀", "a\nb", "a\tb"} {
		f.Add(s)
	}
	f.Add("a  \u0085\r\n\x7f\xc2\x85\xe2\x80\xa8")
	f.Fuzz(func(t *testing.T, s string) {
		got := Scrub(s)
		if !Is(got) {
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
