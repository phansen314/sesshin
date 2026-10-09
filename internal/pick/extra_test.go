package pick

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
)

// obj parses an extra's JSON, as the session view holds it.
func obj(t *testing.T, s string) *jsonio.Object {
	t.Helper()
	o, _, err := jsonio.ParseObject([]byte(s))
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return o
}

// setExtra rewrites session n's sesshin.json with the given extra JSON.
func (f *fixture) setExtra(n int, extra, placement string) {
	f.t.Helper()
	if placement == "" {
		placement = "null"
	}
	b := fmt.Sprintf(`{"schema": 2,"id":%d,"job":null,"source":"spawn","placement":%s,"extra":%s}`, n, placement, extra)
	if _, r := model.ReadSesshin([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture sesshin.json unusable: %s", r.Reason())
	}
	f.write(uuid(n), "sesshin.json", b)
}

func TestRenderExtra(t *testing.T) {
	if got := renderExtra(nil); got != "" {
		t.Errorf("nil: %q", got)
	}
	for _, tc := range []struct{ in, want string }{
		{`{}`, ""},
		{`{"ticket":"auth-3"}`, `ticket=auth-3`},
		{`{"ticket":"auth-4","note":"waiting on review"}`, `ticket=auth-4 note="waiting on review"`},
		{`{"task":57,"tags":["db","api"]}`, `task=57 tags=["db","api"]`},
		// scalar kinds
		{`{"a":"s","b":1,"c":-2.5e3,"d":true,"e":false,"f":null}`, `a=s b=1 c=-2.5e3 d=true e=false f=null`},
		{`{"v":1.10}`, `v=1.10`},
		{`{"task":"57"}`, `task=57`},
		{`{"a":57,"b":"57"}`, `a=57 b=57`},
		// nested
		{`{"o":{"k":[1,{"x":"y z"}],"e":{}},"a":[]}`, `o={"k":[1,{"x":"y z"}],"e":{}} a=[]`},
		// keys
		{`{"two words":1}`, `"two words"=1`},
		{`{"a=b":1}`, `"a=b"=1`},
		{`{"café":1}`, `"café"=1`},
		{`{"":1}`, `""=1`},
		{`{"a_b.c-d9":1}`, `a_b.c-d9=1`},
		{`{"k\t":1}`, `"k\t"=1`},
		// values
		{`{"v":"a b"}`, `v="a b"`},
		{`{"v":"a\u00a0b"}`, "v=\"a\u00a0b\""}, // NBSP: quoted, not escaped
		{`{"v":"a\u2028b"}`, `v="a\u2028b"`},
		{`{"v":"a\u2029b"}`, `v="a\u2029b"`},
		{`{"v":"a=b"}`, `v="a=b"`},
		{`{"v":"a\"b"}`, `v="a\"b"`},
		{`{"v":""}`, `v=""`},
		{`{"v":"a\tb"}`, `v="a\tb"`},
		{`{"v":"a\nb"}`, `v="a\nb"`},
		{`{"v":"a\u001bb"}`, `v="a\u001bb"`},
		{`{"v":"a\u007fb"}`, "v=\"a b\""}, // DEL is a control: quoted, then scrubbed
		{`{"v":"a\\b"}`, `v=a\b`},
		{`{"v":"日本"}`, `v=日本`},
		// no HTML escaping
		{`{"a<b":"<>&","c":["<>&"]}`, `"a<b"=<>& c=["<>&"]`},
		// stored order, not sorted
		{`{"z":1,"a":2,"m":3}`, `z=1 a=2 m=3`},
	} {
		if got := renderExtra(obj(t, tc.in)); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderExtraHostile(t *testing.T) {
	forged := uuid(99)
	o := obj(t, `{"k\u2028\t":"x\t`+forged+`\n\u001b[31m\u2028y","z":"\t`+forged+`\n"}`)
	got := renderExtra(o)
	if strings.ContainsAny(got, "\t\n\r\x1b\u2028\u2029") {
		t.Errorf("raw control in %q", got)
	}
	if !strings.HasPrefix(got, `"k\u2028\t"="x\t`+forged) || !strings.Contains(got, ` z="\t`+forged+`\n"`) {
		t.Errorf("%q", got)
	}
}

// The cap: 200 columns, cut on a grapheme boundary, ending in …
func TestExtraCap(t *testing.T) {
	long := func(prefix string) *jsonio.Object {
		return &jsonio.Object{Members: []jsonio.Member{{Key: "v", Value: prefix + strings.Repeat("x", 400)}}}
	}
	// Exactly 200 stays whole.
	exact := &jsonio.Object{Members: []jsonio.Member{{Key: "v", Value: strings.Repeat("x", 198)}}}
	if got := renderExtra(exact); cells(got) != 200 || strings.HasSuffix(got, "…") {
		t.Errorf("exact: %d cells %q", cells(got), got)
	}
	over := &jsonio.Object{Members: []jsonio.Member{{Key: "v", Value: strings.Repeat("x", 199)}}}
	if got := renderExtra(over); cells(got) != 200 || !strings.HasSuffix(got, "…") {
		t.Errorf("over: %d cells %q", cells(got), got)
	}
	// DEL and C1 are no columns wide until scrubbed to spaces: the cap
	// measures the scrubbed column.
	controls := &jsonio.Object{Members: []jsonio.Member{{Key: "v", Value: strings.Repeat("x", 190) + strings.Repeat("\x7f\u0085\u009b", 10)}}}
	if got := renderExtra(controls); cells(got) != 200 || !strings.HasSuffix(got, "…") {
		t.Errorf("controls: %d cells %q", cells(got), got)
	}
	for name, tc := range map[string]struct {
		prefix string
		// cluster is a cluster that must be whole or absent in the result.
		cluster string
	}{
		"ascii": {"", ""},
		// "v=" is 2 cells; 196 of "x" puts the budget (199) one cell into the cluster.
		"♨️":   {strings.Repeat("x", 196), "♨️"},
		"zwj":  {strings.Repeat("x", 195), "👨‍👩‍👧"},
		"zwj2": {strings.Repeat("x", 194), "👨‍👩‍👧"},
	} {
		o := long(tc.prefix + tc.cluster)
		got := renderExtra(o)
		if w := cells(got); w > 200 {
			t.Errorf("%s: %d cells", name, w)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("%s: no ellipsis: %q", name, got)
		}
		body := strings.TrimSuffix(got, "…")
		for _, part := range []string{"\u200d", "\ufe0f"} {
			if strings.HasSuffix(body, part) {
				t.Errorf("%s: cluster split before %q: %q", name, part, body)
			}
		}
		if tc.cluster != "" {
			// The cluster is whole or entirely absent.
			if rest := strings.ReplaceAll(body, "x", ""); rest != "v=" && rest != "v="+tc.cluster {
				t.Errorf("%s: partial cluster: %q", name, rest)
			}
		}
	}
}

// A cut that ends exactly before a cluster, and one in the middle of it.
func TestCapCells(t *testing.T) {
	zwj := "👨‍👩‍👧" // 3 people, joined: one cluster, 2 cells by the emoji rule
	for _, tc := range []struct {
		s     string
		limit int
		want  string
	}{
		{"abcdef", 6, "abcdef"},
		{"abcdefg", 6, "abcde…"},
		{"abcd" + zwj, 6, "abcd" + zwj}, // 4 + 2 = 6: fits
		{"abcde" + zwj, 6, "abcde…"},
		{"abc♨️def", 6, "abc♨️…"},
		{"abc♨️def", 5, "abc…"},
		{"abcd♨️def", 5, "abcd…"},
		{"abcd" + zwj + "x", 7, "abcd" + zwj + "x"},
		{"abcd" + zwj + "xy", 7, "abcd" + zwj + "…"},
		{"abcd" + zwj + "x", 6, "abcd…"},
	} {
		got := capCells(tc.s, tc.limit)
		if got != tc.want || cells(got) > tc.limit {
			t.Errorf("capCells(%q, %d) = %q (%d cells), want %q", tc.s, tc.limit, got, cells(got), tc.want)
		}
	}
}

func extraView(n int, name, extra string, t *testing.T) ops.SessionView {
	id := int64(n)
	v := ops.SessionView{ID: &id, SessionID: uuid(n), Name: name, LastSeen: "2026-10-04T10:00:00.000Z"}
	if extra != "" {
		v.Extra = obj(t, extra)
	}
	return v
}

func TestRestartLinesExtra(t *testing.T) {
	long := strings.Repeat("n", 40)
	views := []ops.SessionView{
		extraView(1, "api", `{"ticket":"auth-4"}`, t),
		extraView(2, "docs-longer", ``, t),
		extraView(3, "x", `{}`, t),
		extraView(4, long, `{"t":1}`, t),
		extraView(5, "日本", `{"t":2}`, t),
	}
	got := renderLines(views, now, "/home/p")
	// Name padded to 32 (the long one is capped), Extra after two spaces.
	pad := func(s string, w int) string { return s + strings.Repeat(" ", w-len([]rune(s))) }
	for i, want := range []string{
		"  " + pad("api", 32) + "  ticket=auth-4",
		"  docs-longer",
		"  x",
		"  " + long + "  t=1", // longer than the cap: not padded, not cut
		"  日本" + strings.Repeat(" ", 32-4) + "  t=2",
	} {
		_, rest, _ := strings.Cut(got[i], "\t")
		if !strings.HasSuffix(rest, want) {
			t.Errorf("line %d: %q lacks suffix %q", i, rest, want)
		}
	}
	// No trailing spaces on a line without extra.
	for _, i := range []int{1, 2} {
		if strings.HasSuffix(got[i], " ") {
			t.Errorf("trailing space: %q", got[i])
		}
	}

	// No candidate with extra: nothing pads the name.
	plain := []ops.SessionView{extraView(1, "a", ``, t), extraView(2, "longer name", `{}`, t)}
	for _, l := range renderLines(plain, now, "/home/p") {
		if strings.HasSuffix(l, " ") {
			t.Errorf("trailing space: %q", l)
		}
	}
	// The same views with and without extra differ only in the extra.
	for i, l := range renderLines(plain, now, "/home/p") {
		if !strings.HasSuffix(l, plain[i].Name) {
			t.Errorf("line %q", l)
		}
	}
}

func TestJumpLinesExtra(t *testing.T) {
	long := strings.Repeat("n", 40)
	views := []ops.SessionView{
		extraView(1, "api", `{"ticket":"auth-4"}`, t),
		extraView(2, "docs-longer", ``, t),
		extraView(3, "x", `{}`, t),
		extraView(4, long, `{"t":1}`, t),
		extraView(5, "日本", `{"t":2}`, t),
	}
	for i := range views {
		att := "idle"
		views[i].Attention = &att
		views[i].LastEventAt = model.FormatTimestamp(now)
	}
	got := renderJumpLines(views, now, "/home/p")
	pad := func(s string, w int) string { return s + strings.Repeat(" ", w-cells(s)) }
	for i, want := range []string{
		"  " + pad("api", 32) + "  ticket=auth-4",
		"  docs-longer",
		"  x",
		"  " + long + "  t=1",
		"  " + pad("日本", 32) + "  t=2",
	} {
		_, rest, _ := strings.Cut(got[i], "\t")
		if !strings.HasSuffix(rest, want) {
			t.Errorf("line %d: %q lacks suffix %q", i, rest, want)
		}
	}
	for _, i := range []int{1, 2} {
		if strings.HasSuffix(got[i], " ") {
			t.Errorf("trailing space: %q", got[i])
		}
	}
	// No candidate with extra: no trailing spaces anywhere.
	for _, l := range renderJumpLines(views[1:3], now, "/home/p") {
		if strings.HasSuffix(l, " ") {
			t.Errorf("trailing space: %q", l)
		}
	}
}

// A hostile extra stays on one line under its own key, through both pickers.
func TestExtraHostileThroughPickers(t *testing.T) {
	forged := uuid(99)
	extra := `{"note":"a\tb\n` + forged + `\u001b[31m\u2028z","k\t":"v"}`
	check := func(t *testing.T, lines []string, key string) {
		t.Helper()
		if len(lines) != 1 {
			t.Fatalf("%d lines: %q", len(lines), lines)
		}
		k, rest, _ := strings.Cut(lines[0], "\t")
		if k != key || strings.ContainsAny(rest, "\t\n\r\x1b\u2028\u2029") {
			t.Errorf("line %q", lines[0])
		}
		if !strings.Contains(rest, `note="a\tb\n`+forged+`\u001b[31m\u2028z"`) || !strings.Contains(rest, `"k\t"=v`) {
			t.Errorf("extra not under its own keys: %q", rest)
		}
	}
	t.Run("restart", func(t *testing.T) {
		f := newFixture(t)
		f.add(1, time.Minute)
		f.setExtra(1, extra, "")
		f.selects(1)
		f.restart(Input{})
		check(t, f.stdin(), uuid(1))
	})
	t.Run("jump", func(t *testing.T) {
		f := newJumpFixture(t)
		f.live(1, "idle", time.Minute)
		f.setExtra(1, extra, jumpPlacement)
		f.selects(1)
		f.jump(JumpInput{})
		check(t, f.stdin(), uuid(1))
	})
}

func TestPreviewExtra(t *testing.T) {
	// {} and nil.
	for _, v := range []ops.SessionView{extraView(1, "n", ``, t), extraView(1, "n", `{}`, t)} {
		p := preview(v, now, titleOf)
		if !strings.HasSuffix(p, "tab title:       —\nextra:           —\n") {
			t.Errorf("preview:\n%s", p)
		}
	}

	// A value past the cap: whole, indented, last.
	long := strings.Repeat("x", 300)
	v := extraView(1, "n", `{"ticket":"auth-3","long":"`+long+`","nest":{"a":[1,1.10]},"nl":"a\nb\tc","h":"<>&","e":{}}`, t)
	p := preview(v, now, titleOf)
	if !strings.HasSuffix(p, "\n") || strings.HasSuffix(p, "\n\n") {
		t.Errorf("ending: %q", p[len(p)-10:])
	}
	_, block, ok := strings.Cut(p, "tab title:       —\nextra:\n")
	if !ok {
		t.Fatalf("no extra block:\n%s", p)
	}
	want, err := jsonio.MarshalFile(v.Extra)
	if err != nil {
		t.Fatal(err)
	}
	if block != string(want) {
		t.Errorf("block:\n%s\nwant:\n%s", block, want)
	}
	for _, s := range []string{`  "ticket": "auth-3",`, `"long": "` + long + `"`, `1.10`, `"nl": "a\nb\tc"`, `"h": "<>&"`, `"e": {}`} {
		if !strings.Contains(block, s) {
			t.Errorf("block lacks %q:\n%s", s, block)
		}
	}
	if strings.Count(block, "\n") != strings.Count(string(want), "\n") {
		t.Errorf("a newline in a value broke a line:\n%s", block)
	}

	// DEL and C1 as escapes, none raw.
	v = extraView(1, "n", `{"k\u007f":"a\u007fb\u0085c\u009bd\u0080e\u009ff"}`, t)
	p = preview(v, now, titleOf)
	for _, r := range p {
		if r == 0x7f || r >= 0x80 && r <= 0x9f {
			t.Errorf("raw %U in preview", r)
		}
	}
	for _, s := range []string{`\u007f`, `\u0085`, `\u009b`, `\u0080`, `\u009f`} {
		if !strings.Contains(p, s) {
			t.Errorf("preview lacks %s:\n%s", s, p)
		}
	}
	if !strings.Contains(p, `"k\u007f": "a\u007fb\u0085c\u009bd\u0080e\u009ff"`) {
		t.Errorf("preview:\n%s", p)
	}
}
