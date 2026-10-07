package cli

import (
	"reflect"
	"testing"
)

func TestSpawnExtra(t *testing.T) {
	r := spawnRun{}.run(t, "--cwd", "/w", "--extra", `{"koan-task": 57, "l": [1.10, null], "n": {"a": "b"}}`)
	if !r.OK {
		t.Fatalf("%+v", r)
	}
	if want := map[string]any{"koan-task": 57.0, "l": []any{1.1, nil}, "n": map[string]any{"a": "b"}}; !reflect.DeepEqual(r.Result["extra"], want) {
		t.Errorf("extra %v", r.Result["extra"])
	}
	if r = (spawnRun{}).run(t, "--cwd", "/w"); r.Result["extra"] != nil {
		t.Errorf("extra %v without --extra", r.Result["extra"])
	}

	for _, tc := range []struct {
		name  string
		value string
		field []string
	}{
		{"not JSON", `{"a":`, []string{"/extra"}},
		{"empty", ``, []string{"/extra"}},
		{"trailing data", `{} {}`, []string{"/extra"}},
		{"repeated key", `{"a":1,"a":2}`, []string{"/extra/a"}},
		{"repeated nested key", `{"n":{"a":1,"a":2}}`, []string{"/extra/n/a"}},
		{"lone surrogate", `{"a":"\ud800"}`, []string{"/extra"}},
		{"an array", `[1]`, []string{"/extra"}},
		{"null", `null`, []string{"/extra"}},
	} {
		r := spawnRun{}.run(t, "--cwd", "/w", "--extra", tc.value)
		if r.OK || r.Error.Kind != "invalid-input" || !reflect.DeepEqual(problemFields(r), tc.field) {
			t.Errorf("%s: %+v, problems %v", tc.name, r, problemFields(r))
		}
	}
}
