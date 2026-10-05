package jsonio

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPayloadRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		fail Failure
	}{
		{"invalid UTF-8", "{\"a\":\"\xff\"}", FailInvalidUTF8},
		{"empty", "", FailSyntax},
		{"truncated", `{"a":1`, FailSyntax},
		{"trailing data", `{"a":1} x`, FailSyntax},
		{"second value", "{}\n{}", FailSyntax},
		{"byte-order mark", "\xEF\xBB\xBF{}", FailSyntax},
		{"repeated key", `{"a":1,"a":2}`, FailSyntax},
		{"repeated key nested", `{"a":[{"b":1,"c":{"d":1,"d":2}}]}`, FailSyntax},
		{"repeated by escape", `{"\u0061":1,"a":2}`, FailSyntax},
		{"repeated by surrogate rewrite", `{"\ud800":1,"\udc00":2}`, FailSyntax},
		{"array", ` [1]`, FailNotObject},
		{"string", `"x"`, FailNotObject},
	} {
		if p, err := Payload([]byte(tc.in)); failureOf(err) != tc.fail || p != nil {
			t.Errorf("%s: %q, %v; want failure %q", tc.name, p, err, tc.fail)
		}
	}
}

// The same key in different objects, or as a key and a value, is no repeat.
func TestPayloadNoRepeat(t *testing.T) {
	in := `{"a":"a","b":{"a":[{"a":1},{"a":2}]},"c":{"a":{"b":1}}}`
	if _, err := Payload([]byte(in)); err != nil {
		t.Errorf("Payload(%s): %v", in, err)
	}
}

// The stored payload is the input, trimmed, with raw U+2028 and U+2029
// escaped, and never shares the caller's buffer.
func TestPayloadBytes(t *testing.T) {
	for _, tc := range [][2]string{
		{" {\"a\" : 1.10 }\n", `{"a" : 1.10 }`},
		{"{\"a\":\"x\u2028y\u2029z\"}", `{"a":"x\u2028y\u2029z"}`},
		{`{"a":"\u2028 \u0041 \/"}`, `{"a":"\u2028 \u0041 \/"}`},
		{"{\"\u2028\":1}", `{"\u2028":1}`},
	} {
		in := []byte(tc[0])
		p, err := Payload(in)
		if err != nil || string(p) != tc[1] {
			t.Errorf("Payload(%q) = %q, %v; want %q", tc[0], p, err, tc[1])
			continue
		}
		for i := range in {
			in[i] = 'x'
		}
		if string(p) != tc[1] {
			t.Errorf("Payload(%q) shares the caller's buffer", tc[0])
		}
	}
}

// A payload nested past PayloadDepth is refused, whatever strings hide
// brackets; one at it is stored, and writes.
func TestPayloadDepth(t *testing.T) {
	nest := func(n int, inner string) []byte {
		return []byte(`{"a":` + strings.Repeat("[", n-1) + inner + strings.Repeat("]", n-1) + "}")
	}
	if _, err := Payload(nest(PayloadDepth+1, "1")); failureOf(err) != FailSyntax {
		t.Errorf("depth %d: %v, want refused", PayloadDepth+1, err)
	}
	p, err := Payload(nest(PayloadDepth, `"[[[{\"\\[{"`))
	if err != nil {
		t.Fatalf("depth %d: %v", PayloadDepth, err)
	}
	if _, err := MarshalFile(struct {
		Payload json.RawMessage `json:"payload"`
	}{p}); err != nil {
		t.Fatal(err)
	}
}

// An escape naming half a surrogate pair becomes \ufffd; a whole pair, and
// an escaped backslash before a "u", are kept.
func TestPayloadLoneSurrogates(t *testing.T) {
	for _, tc := range [][2]string{
		{`{"a":"\ud83d"}`, `{"a":"\ufffd"}`},
		{`{"a":"x\ud83d\ude00y"}`, `{"a":"x\ud83d\ude00y"}`},
		{`{"a":"\ude00\ud83d"}`, `{"a":"\ufffd\ufffd"}`},
		{`{"a":"\ud83d\ud83d\ude00"}`, `{"a":"\ufffd\ud83d\ude00"}`},
		{`{"a":"\ud83dx","\uDC00":1}`, `{"a":"\ufffdx","\ufffd":1}`},
		{`{"a":"\\ud83d"}`, `{"a":"\\ud83d"}`},
	} {
		p, err := Payload([]byte(tc[0]))
		if err != nil || string(p) != tc[1] {
			t.Errorf("Payload(%s) = %s, %v; want %s", tc[0], p, err, tc[1])
		}
	}
}

// Payload accepts exactly the valid UTF-8, single JSON objects, and what it
// returns decodes to the same value as the input.
func FuzzPayload(f *testing.F) {
	for _, s := range []string{
		`{}`, ` {"a":1.10} `, "{\"a\":\"x y \"}", `{"a":" "}`, "{\"a\":\"\xff\"}",
		`{"a":1,"a":2}`, `{"\ud800":1,"\udc00":2}`, `{"a":{"b":1},"c":{"b":1}}`,
		`[]`, `{} {}`, "\xEF\xBB\xBF{}", `{"a":"\ud800"}`, ``,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Payload(data)
		trimmed := bytes.TrimLeft(data, jsonSpace)
		acceptable := utf8.Valid(data) && json.Valid(data) && len(trimmed) > 0 && trimmed[0] == '{' && tokenDepth(data) <= PayloadDepth
		var re *ReadError
		repeat := errors.As(err, &re) && re.Detail == "repeated key"
		if (err == nil) != (acceptable && !repeat) || (repeat && !acceptable) {
			t.Fatalf("Payload(%q): err %v, but acceptable=%v", data, err, acceptable)
		}
		if err != nil {
			return
		}
		if bytes.Contains(p, []byte(lineSep)) || bytes.Contains(p, []byte(paraSep)) {
			t.Fatalf("Payload(%q) = %q: raw separator left", data, p)
		}
		var in, out any
		if json.Unmarshal(data, &in) != nil || json.Unmarshal(p, &out) != nil || !reflect.DeepEqual(in, out) {
			t.Fatalf("Payload(%q) = %q: a different value", data, p)
		}
		// What is stored reads back through the strict reader: a stored
		// statusline.json is never unusable for its payload.
		file, err := MarshalFile(struct {
			Payload json.RawMessage `json:"payload"`
		}{p})
		if err != nil {
			t.Fatalf("Payload(%q) = %q: write: %v", data, p, err)
		}
		v, repeated, err := ParseValue(file)
		if err != nil || len(repeated) > 0 {
			t.Fatalf("Payload(%q) = %q: reads back as %v, repeated %v", data, p, err, repeated)
		}
		if _, ok := v.(*Object); !ok {
			t.Fatalf("Payload(%q) = %q: reads back as %T", data, p, v)
		}
		if _, repeated, err := ParseObject(p); err != nil || len(repeated) > 0 {
			t.Fatalf("Payload(%q) = %q: as an object: %v, repeated %v", data, p, err, repeated)
		}
	})
}

// tokenDepth is the deepest nesting in valid JSON, by the decoder's tokens.
func tokenDepth(data []byte) int {
	dec := json.NewDecoder(bytes.NewReader(data))
	d, most := 0, 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return most
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			d++
			most = max(most, d)
		case json.Delim('}'), json.Delim(']'):
			d--
		}
	}
}
