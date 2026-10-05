package jsonio

import (
	"encoding/json"
	"reflect"
	"testing"
)

// statuslineFile stands in for the model's statusline.json type (#16), so the
// File format rules are pinned here: a stored payload, sesshin's own strings,
// and nil collections.
type statuslineFile struct {
	Schema     int               `json:"schema"`
	ReceivedAt string            `json:"received_at"`
	Payload    json.RawMessage   `json:"payload"`
	GitBranch  *string           `json:"git_branch"`
	Name       string            `json:"name"`
	UserVars   map[string]string `json:"user_vars"`
	Changes    []string          `json:"changes"`
}

func mustParse(t *testing.T, s string) *Object {
	t.Helper()
	o, repeated, err := ParseObject([]byte(s))
	if err != nil || len(repeated) > 0 {
		t.Fatalf("parse %s: %v %v", s, err, repeated)
	}
	return o
}

func mustPayload(t *testing.T, s string) json.RawMessage {
	t.Helper()
	p, err := Payload([]byte(s))
	if err != nil {
		t.Fatalf("Payload(%q): %v", s, err)
	}
	return p
}

// Exact bytes of a file covering every File format rule: key order from the
// struct; the payload's keys, numbers, and escapes as received, with its raw
// U+2028 escaped; empty {} and [] in and out of the payload; nil collections
// as {} and []; raw <>& and non-ASCII and U+007F–U+009F; escaped controls,
// U+2028 and U+2029, and invalid UTF-8 as U+FFFD in sesshin's own strings.
func TestMarshalFileBytes(t *testing.T) {
	branch := "main"
	f := statuslineFile{
		Schema:     1,
		ReceivedAt: "2026-10-03T18:00:00Z",
		Payload: mustPayload(t, "\n {\"session_id\":\"x\", \"z\":1.10,\"big\":12345678901234567890,\"huge\":1e400,"+
			"\"empty\":{},\"none\":[],\"s\":\"<&> caf\u00e9 \\u0041 \\/ a\u2028b\u2029c \\u2028\",\"nested\":{\"b\":-0,\"a\":[1,null,true]}}\n"),
		GitBranch: &branch,
		Name:      "Zürich 🛫 <&> a/b \u007f\u0085 tab\there line\u2028para\u2029 bad\xff",
	}
	got, err := MarshalFile(f)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "schema": 1,
  "received_at": "2026-10-03T18:00:00Z",
  "payload": {
    "session_id": "x",
    "z": 1.10,
    "big": 12345678901234567890,
    "huge": 1e400,
    "empty": {},
    "none": [],
    "s": "<&> café \u0041 \/ a\u2028b\u2029c \u2028",
    "nested": {
      "b": -0,
      "a": [
        1,
        null,
        true
      ]
    }
  },
  "git_branch": "main",
  "name": "Zürich 🛫 <&> a/b ` + "\u007f\u0085" + ` tab\there line\u2028para\u2029 bad\ufffd",
  "user_vars": {},
  "changes": []
}
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A nil payload (none stored) and a nil pointer stay null.
func TestMarshalFileNulls(t *testing.T) {
	got, err := MarshalFile(struct {
		Payload json.RawMessage `json:"payload"`
		Branch  *string         `json:"git_branch"`
		Extra   *Object         `json:"extra"`
	}{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"payload\": null,\n  \"git_branch\": null,\n  \"extra\": null\n}\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarshalLine(t *testing.T) {
	v := struct {
		OK       bool    `json:"ok"`
		Result   *Object `json:"result"`
		Warnings []any   `json:"warnings"`
	}{true, mustParse(t, `{"b": 1, "a": ["<x>", "\u2028"]}`), nil}
	got, err := MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"ok":true,"result":{"b":1,"a":["<x>","\u2028"]},"warnings":[]}` + "\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

type inner struct {
	Tags []string       `json:"tags"`
	Vars map[string]int `json:"vars"`
}

type outer struct {
	Inner    inner            `json:"inner"`
	Ptr      *inner           `json:"ptr"`
	NilPtr   *inner           `json:"nil_ptr"`
	List     []inner          `json:"list"`
	ByName   map[string]inner `json:"by_name"`
	Any      any              `json:"any"`
	Details  map[string]any   `json:"details"`
	Bytes    []byte           `json:"bytes"`
	Raw      json.RawMessage  `json:"raw"`
	Object   Object           `json:"object"`
	Omitted  []string         `json:"omitted,omitempty"`
	Kept     []string         `json:"kept"`
	internal []string
}

// Nil slices and maps become empty at every level, through structs,
// pointers, slices, maps, and interfaces; byte slices and values that marshal
// themselves are left alone; the value passed in is never changed.
func TestMarshalNonNil(t *testing.T) {
	v := outer{
		Ptr:     &inner{},
		List:    []inner{{Tags: []string{"a"}}, {}},
		ByName:  map[string]inner{"k": {}},
		Any:     []any{inner{}, nil},
		Details: map[string]any{"ids": []int(nil)},
		Kept:    []string{"x"},
	}
	before, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"inner":{"tags":[],"vars":{}},"ptr":{"tags":[],"vars":{}},"nil_ptr":null,` +
		`"list":[{"tags":["a"],"vars":{}},{"tags":[],"vars":{}}],"by_name":{"k":{"tags":[],"vars":{}}},` +
		`"any":[{"tags":[],"vars":{}},null],"details":{"ids":[]},"bytes":null,"raw":null,"object":{},"kept":["x"]}` + "\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	after, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("input changed:\n%s\n%s", before, after)
	}
}

// A value with no nil collections is encoded as given, not copied.
func TestNonNilUnchanged(t *testing.T) {
	v := &inner{Tags: []string{}, Vars: map[string]int{}}
	if got := nonNil(v); got != any(v) {
		t.Errorf("copied a value with nothing to fill")
	}
	if got := nonNil(nil); got != nil {
		t.Errorf("nonNil(nil) = %v", got)
	}
	if got := nonNil(inner{}); !reflect.DeepEqual(got, inner{Tags: []string{}, Vars: map[string]int{}}) {
		t.Errorf("nonNil(inner{}) = %#v", got)
	}
}

// Numbers in a tree round-trip character for character.
func TestNumbersRoundTrip(t *testing.T) {
	in := `{"a":1.10,"b":-0,"c":1e400,"d":12345678901234567890,"e":-0.0E+00,"f":9007199254740993}`
	got, err := MarshalLine(mustParse(t, in))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != in+"\n" {
		t.Errorf("got %s, want %s", got, in)
	}
}

// A settings.json tree is written back in its order, with raw <>& and
// non-ASCII and escaped U+2028, and empty {} and [].
func TestMarshalFileTree(t *testing.T) {
	o := mustParse(t, `{"statusLine":{"type":"command","command":"sesshin-hook statusline"},"hooks":{},"permissions":{"allow":[]},"env":{"A":"<&> é \u2028"},"cleanupPeriodDays":30.0}`)
	got, err := MarshalFile(o)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "statusLine": {
    "type": "command",
    "command": "sesshin-hook statusline"
  },
  "hooks": {},
  "permissions": {
    "allow": []
  },
  "env": {
    "A": "<&> é \u2028"
  },
  "cleanupPeriodDays": 30.0
}
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestObjectSetDelete(t *testing.T) {
	o := mustParse(t, `{"status":"a","owner":"me","n":1}`)
	o.Set("owner", "you") // existing key keeps its position
	o.Set("new1", 1.5)
	o.Set("new2", nil) // new keys appended in the order given
	o.Delete("status")
	o.Delete("absent")
	got, err := MarshalLine(o)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"owner":"you","n":1,"new1":1.5,"new2":null}` + "\n"; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if o.Len() != 4 {
		t.Errorf("Len %d", o.Len())
	}
}

// Set and Delete leave a shallow copy's members as they were, even when the
// members slice has spare capacity.
func TestObjectSetDeleteKeepCopy(t *testing.T) {
	members := make([]Member, 3, 8)
	copy(members, []Member{{"a", "1"}, {"b", "2"}, {"c", "3"}})
	o := &Object{Members: members}
	for _, change := range []func(){
		func() { o.Delete("a") },
		func() { o.Set("b", "x") },
		func() { o.Set("d", "4") },
	} {
		old := *o
		want, err := MarshalLine(old)
		if err != nil {
			t.Fatal(err)
		}
		change()
		got, err := MarshalLine(old)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("copy changed from %s to %s", want, got)
		}
	}
	if got, _ := MarshalLine(o); string(got) != `{"b":"x","c":"3","d":"4"}`+"\n" {
		t.Errorf("got %s", got)
	}
}

func TestPointer(t *testing.T) {
	for _, tc := range [][3]string{
		{"", "a", "/a"},
		{"/hooks", "Stop", "/hooks/Stop"},
		{"", "a/b", "/a~1b"},
		{"", "~1", "/~01"},
		{"", "", "/"},
	} {
		if got := Pointer(tc[0], tc[1]); got != tc[2] {
			t.Errorf("Pointer(%q, %q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
	}
}

// An Object stored by value encodes like a *Object; a nil *Object is null.
func TestMarshalObjectValue(t *testing.T) {
	o := &Object{Members: []Member{{"v", Object{Members: []Member{{"k", 1}}}}, {"nil", (*Object)(nil)}, {"e", Object{}}}}
	got, err := MarshalLine(o)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"v":{"k":1},"nil":null,"e":{}}` + "\n"; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
