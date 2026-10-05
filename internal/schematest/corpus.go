package schematest

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Candidates are the values Mutations puts in place of a field. They leave
// out integral numbers not written as integer literals (2.0), which the
// schemas accept and the validators reject, and repeated keys, which the
// library cannot see. Every other rule beyond a schema — real dates, a
// session_id naming its directory, pid and pid_started_at null together,
// end_reason only with ended_at, a number past float64's range — is marked by
// the validator and left out of the comparison (model.Problems.SchemaList), so
// candidates may break it freely.
var Candidates = []string{
	`null`, `true`, `false`,
	`0`, `-0`, `1`, `-1`, `1.5`, `-0.5`, `1e400`, `-1e400`, `1e-400`, `-1e-400`,
	`9007199254740991`, `9007199254740992`, `-9007199254740991`,
	`9223372036854775807`, `9223372036854775808`,
	`""`, `"x"`, `" x"`, `"a\nb"`, `"a\rb"`, `"a\tb"`, `"a\u0000b"`, `"a\u001fb"`, `"a\u007fb"`, `"a\u0085b"`, "\"a\u2028b\"", "\"a\u2029b\"", "\"a\u200db\"", `"é"`, `"😀"`,
	`"a"`, `"A"`, `"aB"`, `"_a"`, `"1a"`, `"a_1"`, `"a-1"`, `"a:b"`, `"a:"`, `":a"`, `"a:b:c"`, `"a:B"`, `"a:b-c"`, `"acceptEdits"`, `"sdk-cli"`,
	`"` + strings.Repeat("a", 64) + `"`, `"` + strings.Repeat("a", 65) + `"`,
	`"a:` + strings.Repeat("b", 64) + `"`, `"a:` + strings.Repeat("b", 65) + `"`,
	`"` + strings.Repeat("é", 128) + `"`, `"` + strings.Repeat("é", 129) + `"`,
	`"` + strings.Repeat(`😀`, 128) + `"`, `"` + strings.Repeat(`😀`, 129) + `"`, // 128, 129 code points
	`"api-review"`, `"12"`, `"0a"`, `"1-2"`, `"a-"`, `"-a"`, `"a--b"`, `"A-b"`, `"a_b"`, `"api\n"`,
	`"3fa85f6457174562b3fc2c963f66afa6"`, `"3fa85f6457174562b3fc2c963f66afa"`, `"3fa85f6457174562b3fc2c963f66afa6a"`, `"3FA85F6457174562B3FC2C963F66AFA6"`, `"3fa85f6457174562b3fc2c963f66afg6"`,
	`"` + strings.Repeat("a", 62) + `-a"`, `"` + strings.Repeat("a", 63) + `-a"`, `"` + strings.Repeat("1", 64) + `"`,
	`"/"`, `"/a b"`, `"a/b"`, `"./a"`, `"/a\nb"`,
	`"3fa85f64-5717-4562-b3fc-2c963f66afa6"`, `"3FA85F64-5717-4562-B3FC-2C963F66AFA6"`,
	`"3fa85f64-5717-4562-b3fc-2c963f66afa"`, `"3fa85f64-5717-4562-b3fc-2c963f66afa6x"`, `"3fa85f64_5717-4562-b3fc-2c963f66afa6"`, `"3fa85f64-5717-4562-b3fc-2c963f66afg6"`,
	`"2026-10-03T18:31:51Z"`, `"2026-10-03T18:31:51"`, `"2026-10-03 18:31:51Z"`, `"2026-10-03T18:31:51z"`,
	`"2026-10-03T18:31:51.5Z"`, `"2026-10-03T18:31:51+00:00"`, `"26-10-03T18:31:51Z"`,
	`"2026-02-30T00:00:00Z"`, `"2026-10-03T24:00:00Z"`, `"2026-10-03T23:59:60Z"`, `"2026-13-01T00:00:00Z"`,
	`[]`, `[1]`, `["x"]`, `[null]`,
	`{}`, `{"a": 1}`, `{"terminal": "kitty"}`, `{"terminal": "kitty", "window_id": 7, "user_vars": {}}`, `{"terminal": "Kitty"}`, `{"terminal": 1}`, `{"terminal": null}`,
	`{"at": "2026-10-03T18:31:51Z", "usd": 1.25}`, `{"at": "2026-10-03T18:31:51Z", "usd": -1}`, `{"at": "2026-10-03T18:31:51Z"}`, `{"at": "2026-10-03T18:31:51Z", "usd": 1, "x": 1}`,
	`{"config_dir": "/a", "state_dir": "/b", "claude_settings": "/c"}`, `{"config_dir": "a", "state_dir": "/b", "claude_settings": "/c"}`,
}

// Mutations returns variants of base, a valid JSON object, each differing
// from it in one place: for every member of every object (nested ones
// included, but not objects inside arrays), the member removed, its value
// replaced by each candidate (Candidates, then extra), and, for an array
// value, its last item replaced by each candidate; plus each object with an
// unknown member added.
func Mutations(t testing.TB, base string, extra ...string) []string {
	t.Helper()
	root, _, err := jsonio.ParseObject([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	var values []any
	for _, c := range append(slices.Clone(Candidates), extra...) {
		v, _, err := jsonio.ParseValue([]byte(c))
		if err != nil {
			t.Fatalf("candidate %s: %v", c, err)
		}
		values = append(values, v)
	}
	var out []string
	emit := func(doc any) {
		b, err := jsonio.MarshalLine(doc)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.TrimSuffix(string(b), "\n"))
	}
	var visit func(obj *jsonio.Object, path []string)
	visit = func(obj *jsonio.Object, path []string) {
		for _, m := range obj.Members {
			at := append(slices.Clone(path), m.Key)
			emit(edit(root, at, nil, true))
			for _, v := range values {
				emit(edit(root, at, v, false))
				if arr, ok := m.Value.([]any); ok && len(arr) > 0 {
					items := slices.Clone(arr)
					items[len(items)-1] = v
					emit(edit(root, at, items, false))
				}
			}
			if child, ok := m.Value.(*jsonio.Object); ok {
				visit(child, at)
			}
		}
		emit(edit(root, append(slices.Clone(path), "unknown"), true, false))
	}
	visit(root, nil)
	emit(edit(root, []string{"Schema"}, json.Number("1"), false))
	return out
}

// edit returns a copy of obj with the member at path (a key per object
// level) set to v, or deleted; obj itself is unchanged, since Set and Delete
// never write to the members slice they share with it.
func edit(obj *jsonio.Object, path []string, v any, del bool) *jsonio.Object {
	cp := &jsonio.Object{Members: obj.Members}
	if len(path) == 1 {
		if del {
			cp.Delete(path[0])
		} else {
			cp.Set(path[0], v)
		}
		return cp
	}
	child, _ := obj.Get(path[0])
	cp.Set(path[0], edit(child.(*jsonio.Object), path[1:], v, del))
	return cp
}
