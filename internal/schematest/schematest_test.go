package schematest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAllSchemasCompile(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		Schema(t, id)
	}
}

// numbers are the only schema paths allowed a non-integer number: the two
// that accept any JSON number (implementation-spec.md, Integer literals).
// Every other number is an integer, so its validator takes integer literals
// only; a new non-integer number must be added here, and to the spec.
var numbers = map[string]bool{
	"statusline-file/properties/cost_sample/properties/usd": true,
	"statusline-file/properties/burn_usd_per_hour":          true,
	// The session view reports these payload numbers, and the burn rate, as
	// stored.
	"session-view/properties/metrics/properties/cost_usd":          true,
	"session-view/properties/metrics/properties/burn_usd_per_hour": true,
	"session-view/properties/metrics/properties/context_percent":   true,
	"session-view/properties/prompt_cache/properties/hit_ratio":    true,
}

// The published output schemas leave objects open, so a caller validating
// with them accepts a newer release's added fields (operations.md,
// Versioning).
func TestOutputSchemasOpen(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !IsOutput(id) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(Dir(), id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		var walk func(v any, path string)
		walk = func(v any, path string) {
			switch v := v.(type) {
			case map[string]any:
				for k, x := range v {
					if k == "additionalProperties" && x == false {
						t.Errorf("%s%s: an output object is closed", id, path)
					}
					walk(x, path+"/"+k)
				}
			case []any:
				for i, x := range v {
					walk(x, fmt.Sprintf("%s/%d", path, i))
				}
			}
		}
		walk(doc, "")
	}
}

// Tests compile the output schemas closed: a field the spec doesn't describe
// fails, nested ones too, while an object marked open stays open.
func TestOutputClosedInTests(t *testing.T) {
	for _, tc := range []struct {
		id, in string
		want   []string
	}{
		{"session-ref", `{"id": 12, "session_id": "s", "name": "api"}`, nil},
		{"session-ref", `{"id": 12, "session_id": "s", "name": "api", "x": 1}`, []string{"/x"}},
		{"envelope", `{"ok": false, "error": {"kind": "io", "message": "m", "details": {}, "x": 1}, "warnings": []}`, []string{"/error/x"}},
		{"envelope", `{"ok": true, "result": {"anything": 1}, "warnings": [], "x": 1}`, []string{"/x"}},
	} {
		ok, f := Check(t, tc.id, []byte(tc.in))
		if ok != (tc.want == nil) || !ok && !f.Matches(tc.want) {
			t.Errorf("%s %s: ok %v, failure %s; want %q", tc.id, tc.in, ok, f, tc.want)
		}
	}
}

// Fail if a schema allows a non-integer number anywhere but numbers.
func TestNoNonIntegerNumbers(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(Dir(), id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		var walk func(v any, path string)
		walk = func(v any, path string) {
			switch v := v.(type) {
			case map[string]any:
				for k, x := range v {
					if k == "type" && (x == "number" || containsNumber(x)) && !numbers[id+path] {
						t.Errorf("%s%s: allows a non-integer number", id, path)
					}
					if k == "multipleOf" {
						t.Errorf("%s%s: uses multipleOf", id, path)
					}
					walk(x, path+"/"+k)
				}
			case []any:
				for _, x := range v {
					walk(x, path+"/[]")
				}
			}
		}
		walk(doc, "")
	}
}

// A value schema without a type would let a non-integer number through
// unnoticed by TestNoNonIntegerNumbers, so every one must say what it holds.
func TestNoUntypedValues(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(Dir(), id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		checkTyped(t, id, "", doc, !onlyDefs(doc))
	}
}

// onlyDefs reports whether a schema document is a container of $defs alone
// (defs), which validates no value itself.
func onlyDefs(doc any) bool {
	m, _ := doc.(map[string]any)
	for k := range m {
		if k != "$schema" && k != "$id" && k != "$defs" {
			return false
		}
	}
	_, ok := m["$defs"]
	return ok
}

// checkTyped checks s, a schema at path, and the value schemas within it. A
// value schema (value) must constrain its type. A branch — anyOf, oneOf,
// allOf, then, else — is one only when its parent is a value schema that does
// not; otherwise it refines a value typed elsewhere, as do its members.
func checkTyped(t *testing.T, id, path string, s any, value bool) {
	m, ok := s.(map[string]any)
	if !ok {
		if s != false {
			t.Errorf("%s%s: untyped schema %v", id, path, s)
		}
		return
	}
	typed := false
	for _, k := range []string{"type", "$ref", "const", "enum"} {
		if _, ok := m[k]; ok {
			typed = true
		}
	}
	branches := map[string]any{} // by path
	for _, k := range []string{"anyOf", "oneOf", "allOf"} {
		for i, b := range asSlice(m[k]) {
			branches[fmt.Sprintf("%s/%s/%d", path, k, i)] = b
		}
	}
	for _, k := range []string{"then", "else"} {
		if b, ok := m[k]; ok {
			branches[path+"/"+k] = b
		}
	}
	if value && !typed && len(branches) == 0 {
		t.Errorf("%s%s: untyped schema", id, path)
	}
	for bpath, b := range branches {
		checkTyped(t, id, bpath, b, value && !typed)
	}
	for _, k := range []string{"properties", "$defs"} {
		props, _ := m[k].(map[string]any)
		for name, p := range props {
			checkTyped(t, id, path+"/"+k+"/"+name, p, value || k == "$defs")
		}
	}
	for _, k := range []string{"items", "additionalProperties"} {
		if x, ok := m[k]; ok {
			checkTyped(t, id, path+"/"+k, x, value)
		}
	}
}

func asSlice(v any) []any {
	a, _ := v.([]any)
	return a
}

func containsNumber(v any) bool {
	a, ok := v.([]any)
	if !ok {
		return false
	}
	for _, x := range a {
		if x == "number" {
			return true
		}
	}
	return false
}

func TestECMAEngine(t *testing.T) {
	for _, tc := range []struct {
		pattern, in string
		match       bool
	}{
		{`^[^\u0000-\u001F]+$`, "abc", true},
		{`^[^\u0000-\u001F]+$`, "a\nb", false},
		{`^[^\u2028]$`, "\u2028", false},
		{`^\\u0041$`, `\u0041`, true}, // an escaped backslash, then a literal u0041
		{`^\\u0041$`, "A", false},
		{`^a\.b$`, "a.b", true},
		{`^[.]$`, ".", true},
		{`^[.]$`, "a", false},
		{`^(?![0-9]+$)[a-z0-9]+$`, "a1", true},
		{`^(?![0-9]+$)[a-z0-9]+$`, "12", false},
		{`^(job:)?(?![0-9]+$)[a-z0-9]+$`, "api", true},
		{`^(job:)?(?![0-9]+$)[a-z0-9]+$`, "job:api", true},
		{`^(job:)?(?![0-9]+$)[a-z0-9]+$`, "job:12", false},
		{`^(job:)?(?![0-9]+$)[a-z0-9]+$`, "12", false},
		{`^(job:)?(?![0-9]+$)[a-z0-9]+$`, "job:", false},
		{`^(job:)?(?![0-9]+$)[a-z0-9]+$`, "job:job:a", false},
	} {
		re, err := ecmaEngine(tc.pattern)
		if err != nil {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		if got := re.MatchString(tc.in); got != tc.match {
			t.Errorf("%s on %q: %v, want %v", tc.pattern, tc.in, got, tc.match)
		}
	}
}

// Syntax whose meaning differs between ECMA-262 and RE2 is refused.
func TestECMAEngineRefuses(t *testing.T) {
	for _, pattern := range []string{`^a.b$`, `^[a]..$`, `^\s$`, `^[\S]$`} {
		if _, err := ecmaEngine(pattern); err == nil {
			t.Errorf("%s: compiled", pattern)
		}
	}
}

// Parent-reported errors land at the child.
func TestLocations(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{`{"schema": 2, "last_id": 0, "migration": 0}`, nil},
		{`{"schema": 2}`, []string{"/last_id", "/migration"}},
		{`{"schema": 2, "last_id": 0, "migration": 0, "x": 1, "a/b": 2}`, []string{"/a~1b", "/x"}},
		{`{"schema": 1, "last_id": -1, "migration": 0}`, []string{"/last_id", "/schema"}},
		{`{"schema": 2, "last_id": 9007199254740992, "migration": 0}`, []string{"/last_id"}}, // exact, not rounded through a float64
	} {
		ok, f := Check(t, "state-file", []byte(tc.in))
		if got := f.Fields; ok != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) || f.Alternatives != nil {
			t.Errorf("%s: ok %v, locations %q; want %q", tc.in, ok, got, tc.want)
		}
	}
}

// Each alternative of a failing anyOf is kept apart; a validator must match
// exactly one.
func TestAlternatives(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		matches [][]string
		misses  [][]string
	}{
		{`{"schema": 2, "id": 0, "job": null, "source": "hook", "placement": null, "extra": {}}`, `[] + one of [["/id"] ["/id"]] at "/id"`,
			[][]string{{"/id"}}, [][]string{nil, {""}}},
		{`{"schema": 2, "id": "x", "job": null, "source": "hook", "placement": {}, "extra": {}, "y": 1}`, `["/placement/terminal" "/y"] + one of [["/id"] ["/id"]] at "/id"`,
			[][]string{{"/id", "/placement/terminal", "/y"}}, [][]string{{"/id", "/y"}}},
	} {
		_, f := Check(t, "sesshin-file", []byte(tc.in))
		if got := f.String(); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.in, got, tc.want)
		}
		for _, m := range tc.matches {
			if !f.Matches(m) {
				t.Errorf("%s: %q should match", tc.in, m)
			}
		}
		for _, m := range tc.misses {
			if f.Matches(m) {
				t.Errorf("%s: %q should not match", tc.in, m)
			}
		}
	}
}
