package jsonio

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

// Raw U+2028 and U+2029, and their escapes.
const (
	lineSep    = "\u2028"
	paraSep    = "\u2029"
	lineSepEsc = `\u2028`
	paraSepEsc = `\u2029`
)

// jsonSpace is JSON's whitespace.
const jsonSpace = " \t\r\n"

// PayloadDepth is the deepest nesting of objects and arrays a stored payload
// may have. Claude Code's is about 4. Indented output grows with the square
// of the depth, so a payload near the encoder's limit would make every tick
// write hundreds of megabytes (implementation-spec.md, JSON reading).
const PayloadDepth = 64

// Payload returns the statusline payload raw as received, ready to store
// in statusline.json: written back with its key order, number text, and
// escapes as received, with two exceptions (design-spec.md, File format):
// raw U+2028 and U+2029, which the encoder leaves alone inside a
// json.RawMessage, are escaped, and an escape naming half a surrogate pair
// becomes \ufffd, which sesshin's strict reader would otherwise refuse (Node
// writes one for a string cut inside an emoji). A payload that isn't valid
// UTF-8, isn't exactly one JSON object, is nested deeper than PayloadDepth,
// or repeats a key in any object (after the rewrites above) is a ReadError:
// it is not stored (hooks-spec.md, statusline, Degraded). What Payload
// returns always reads back through ParseValue.
func Payload(raw []byte) (json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, &ReadError{Failure: FailInvalidUTF8}
	}
	if !json.Valid(raw) {
		return nil, &ReadError{Failure: FailSyntax}
	}
	raw = bytes.Trim(raw, jsonSpace)
	if raw[0] != '{' {
		return nil, &ReadError{Failure: FailNotObject}
	}
	if depth(raw) > PayloadDepth {
		return nil, &ReadError{Failure: FailSyntax, Detail: "nested deeper than " + strconv.Itoa(PayloadDepth)}
	}
	raw = bytes.Clone(raw)
	// In valid JSON, U+2028 and U+2029 can only occur inside a string, where
	// an escape stands for the same character.
	if bytes.Contains(raw, []byte(lineSep)) || bytes.Contains(raw, []byte(paraSep)) {
		raw = bytes.ReplaceAll(raw, []byte(lineSep), []byte(lineSepEsc))
		raw = bytes.ReplaceAll(raw, []byte(paraSep), []byte(paraSepEsc))
	}
	// Each lone surrogate's escape is six bytes, as is its replacement's.
	for i := 0; ; {
		j, ok := loneSurrogate(raw[i:])
		if !ok {
			break
		}
		i += j
		copy(raw[i:], replacementEsc)
		i += len(replacementEsc)
	}
	// Checked after the rewrites, which can make two distinct keys equal.
	if hasRepeatedKey(raw) {
		return nil, &ReadError{Failure: FailSyntax, Detail: "repeated key"}
	}
	return raw, nil
}

// replacementEsc is U+FFFD's escape, what the decoder reads a lone surrogate
// as.
const replacementEsc = `\ufffd`

// depth is the deepest nesting of objects and arrays in data, which must be
// valid JSON.
func depth(data []byte) int {
	d, most := 0, 0
	in := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case in && c == '\\':
			i++
		case c == '"':
			in = !in
		case in:
		case c == '{' || c == '[':
			d++
			most = max(most, d)
		case c == '}' || c == ']':
			d--
		}
	}
	return most
}

// hasRepeatedKey reports whether any object in data, which must be valid
// JSON, has the same key twice. Keys are compared as decoded.
func hasRepeatedKey(data []byte) bool {
	type scope struct {
		keys    map[string]struct{} // nil for an array
		wantKey bool                // in an object: the next token is a key
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var stack []scope
	// valueDone marks the enclosing object's member complete.
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].keys != nil {
			stack[n-1].wantKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, scope{keys: map[string]struct{}{}, wantKey: true})
			case '[':
				stack = append(stack, scope{})
			default:
				stack = stack[:len(stack)-1]
				valueDone()
			}
		case string:
			if n := len(stack); n > 0 && stack[n-1].wantKey {
				top := &stack[n-1]
				if _, dup := top.keys[t]; dup {
					return true
				}
				top.keys[t] = struct{}{}
				top.wantKey = false
			} else {
				valueDone()
			}
		default:
			valueDone()
		}
	}
}
