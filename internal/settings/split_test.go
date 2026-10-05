package settings

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		in   string
		want []string // nil: not ok
	}{
		// What splits.
		{`/a/sesshin-hook stop`, []string{"/a/sesshin-hook", "stop"}},
		{"  /a/sesshin-hook \t stop  ", []string{"/a/sesshin-hook", "stop"}},
		{`'/a b/sesshin-hook' stop`, []string{"/a b/sesshin-hook", "stop"}},
		{`"/a b/sesshin-hook" stop`, []string{"/a b/sesshin-hook", "stop"}},
		{`/a\ b/sesshin-hook stop`, []string{"/a b/sesshin-hook", "stop"}},
		{`'it'\''s/sesshin-hook' stop`, []string{"it's/sesshin-hook", "stop"}},
		{`/a/'sesshin'-"hook" st\op`, []string{"/a/sesshin-hook", "stop"}},
		{`'' x`, []string{"", "x"}},
		{`"" x`, []string{"", "x"}},
		{`'$HOME' "a b"`, []string{"$HOME", "a b"}},
		{`'*?[{};|&<>()~#' x`, []string{"*?[{};|&<>()~#", "x"}},
		{`"*?[{};|&<>()~#" x`, []string{"*?[{};|&<>()~#", "x"}},
		{`a#b x`, []string{"a#b", "x"}},
		{`a~b x`, []string{"a~b", "x"}},
		{`a=b/sesshin-hook`, nil},
		{`"a=b" x`, []string{"a=b", "x"}},
		{`'A=1' x`, []string{"A=1", "x"}},
		{`x A=1`, []string{"x", "A=1"}},
		{`=x y`, []string{"=x", "y"}},
		{`1=x y`, []string{"1=x", "y"}},
		{`a-b=x y`, []string{"a-b=x", "y"}},
		{"\"a\nb\" x", []string{"a\nb", "x"}},
		{"'a\nb' x", []string{"a\nb", "x"}},
		{`"a\$b\` + "`" + `c\"d\\e\fg" x`, []string{"a$b`c\"d\\e\\fg", "x"}},
		{"\"a\\\nb\" x", []string{"ab", "x"}},
		{`\$ \' \" x`, []string{"$", "'", `"`, "x"}},
		{`é/sesshin-hook ü`, []string{"é/sesshin-hook", "ü"}},

		// Anything a shell might expand or treat specially is not ok.
		{`$HOME/sesshin-hook stop`, nil},
		{`${HOME}/sesshin-hook stop`, nil},
		{`/a/sesshin-hook $1`, nil},
		{`"$HOME/sesshin-hook" stop`, nil},
		{`"/a/sesshin-hook" "$x"`, nil},
		{"`x`/sesshin-hook stop", nil},
		{"\"`x`/sesshin-hook\" stop", nil},
		{`$(x)/sesshin-hook stop`, nil},
		{`/a/*/sesshin-hook stop`, nil},
		{`/a/sesshin-hook st*`, nil},
		{`/a/sesshin-hoo? stop`, nil},
		{`/a/[h]erd-hook stop`, nil},
		{`~/bin/sesshin-hook stop`, nil},
		{`~root/sesshin-hook stop`, nil},
		{`/a/sesshin-hook ~`, nil},
		{`/a/{sesshin-hook,x} stop`, nil},
		{`/a/sesshin-hook }`, nil},
		{`/a/sesshin-hook stop; rm -rf x`, nil},
		{`/a/sesshin-hook stop|cat`, nil},
		{`/a/sesshin-hook stop&`, nil},
		{`/a/sesshin-hook stop>x`, nil},
		{`/a/sesshin-hook <x`, nil},
		{`(/a/sesshin-hook stop)`, nil},
		{`/a/sesshin-hook stop)`, nil},
		{`/a/sesshin-hook stop # c`, nil},
		{`#/a/sesshin-hook stop`, nil},
		{"/a/sesshin-hook\nstop", nil},
		{"/a/sesshin-hook stop\n", nil},
		{"/a/sesshin-hook \\\nstop", nil},
		{"/a/sesshin-hook\rstop", nil},
		{"/a/sesshin-hook\x00 stop", nil},
		{`FOO=1 /a/sesshin-hook stop`, nil},
		{`FOO=/a/sesshin-hook stop`, nil},
		{`_x1= stop`, nil},
		{`'/a/sesshin-hook stop`, nil},
		{`"/a/sesshin-hook stop`, nil},
		{`/a/sesshin-hook stop\`, nil},
		{`"/a/sesshin-hook stop\`, nil},
	}
	for _, tt := range tests {
		got, ok := Split(tt.in)
		if tt.want == nil {
			if ok {
				t.Errorf("Split(%q) = %q, want not ok", tt.in, got)
			}
			continue
		}
		if !ok || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Split(%q) = %q, %v; want %q", tt.in, got, ok, tt.want)
		}
	}
}

func TestSplitEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		if got, ok := Split(in); !ok || len(got) != 0 {
			t.Errorf("Split(%q) = %q, %v; want no words", in, got, ok)
		}
	}
}

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/home/me/go/bin/sesshin-hook", "/home/me/go/bin/sesshin-hook"},
		{"/a_b.c-d/E9", "/a_b.c-d/E9"},
		{"/home/me/my bin/sesshin-hook", `'/home/me/my bin/sesshin-hook'`},
		{"/home/o'brien/sesshin-hook", `'/home/o'\''brien/sesshin-hook'`},
		{"/a/$x", `'/a/$x'`},
		{"/a/é", `'/a/é'`},
		{"/a\nb", "'/a\nb'"},
		{"''", `''\'''\'''`},
		{"", `''`},
	}
	for _, tt := range tests {
		if got := Quote(tt.in); got != tt.want {
			t.Errorf("Quote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Quoting any path, then splitting, gives back [path, verb].
func TestQuoteSplitRoundTrip(t *testing.T) {
	alphabet := []string{"a", "Z", "0", "/", ".", "_", "-", " ", "\t", "'", `"`, `\`, "$", "`", "*", "?", "[", "{", "}", ";", "|", "&", "<", ">", "(", ")", "~", "#", "=", "!", "\n", "\r", "é", "世", "\x00", "\x7f"}
	rng := rand.New(rand.NewSource(1))
	check := func(p string) {
		cmd := Quote(p) + " stop"
		got, ok := Split(cmd)
		if !ok || !reflect.DeepEqual(got, []string{p, "stop"}) {
			t.Fatalf("path %q: command %q splits to %q, %v", p, cmd, got, ok)
		}
		if verb, ok := verbOf(cmd, func(w string) bool { return w == p }); !ok || verb != "stop" {
			t.Fatalf("path %q: verbOf(%q) = %q, %v", p, cmd, verb, ok)
		}
	}
	for _, p := range []string{"", "/", "'", "''", `\`, "~", "#", "A=1", "-"} {
		check(p)
	}
	for range 20000 {
		var sb strings.Builder
		for range rng.Intn(12) {
			sb.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		check(sb.String())
	}
}

func TestSesshinHookName(t *testing.T) {
	for p, want := range map[string]bool{
		"/a/sesshin-hook":       true,
		"sesshin-hook":          true,
		"/a b/sesshin-hook":     true,
		"/a/sesshin-hook/":      false,
		"/a/sesshin-hook.sh":    false,
		"/a/sesshin-hook2":      false,
		"/a/xsesshin-hook":      false,
		"/a/sesshin":            false,
		"/sesshin-hook/sesshin": false,
		"":                      false,
		"/":                     false,
		"/a/Sesshin-Hook":       false,
		"/a/sesshin-hook/..":    false,
		"/a/sesshin-hook/./x":   false,
	} {
		if got := SesshinHookName(p); got != want {
			t.Errorf("SesshinHookName(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestVerbOf(t *testing.T) {
	tests := []struct {
		cmd  string
		verb string // "" for not sesshin's
	}{
		{`/home/me/go/bin/sesshin-hook stop`, "stop"},
		{`/other/sesshin-hook stop`, "stop"},
		{`'/my dir/sesshin-hook' statusline`, "statusline"},
		{`/home/me/go/bin/sesshin-hook`, ""},
		{`/home/me/go/bin/sesshin-hook stop now`, ""},
		{`/other/tool stop`, ""},
		{`$HOME/.claude/sesshin-hook stop`, ""},
		{`sh /a/sesshin-hook stop`, ""},
		// herd's shim script is not sesshin's, whatever its location.
		{`/home/me/.claude/sesshin/hook.sh stop`, ""},
		{`~/.claude/sesshin-hook.sh session-start`, ""},
		{`"$HOME/.claude/hooks/sesshin.sh" stop`, ""},
		{`/home/me/.claude/sesshin-hook.sh stop`, ""},
		{`sesshin-hook stop; true`, ""},
	}
	for _, tt := range tests {
		verb, ok := verbOf(tt.cmd, ours)
		if ok != (tt.verb != "") || verb != tt.verb {
			t.Errorf("verbOf(%q) = %q, %v; want %q", tt.cmd, verb, ok, tt.verb)
		}
	}
	// A path the predicate names that isn't called sesshin-hook.
	if _, ok := verbOf(`/x/renamed stop`, func(p string) bool { return p == "/x/renamed" }); !ok {
		t.Error("predicate path not accepted")
	}
}
