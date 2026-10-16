package model

import (
	"strings"
	"testing"
)

// A file's template is a template name or null, in sesshin.json and in the
// reservation (design-spec.md, Templates); the key is required.
func TestTemplateField(t *testing.T) {
	long := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name  string
		value string // JSON text; "" for the key left out
		field string // "": usable
	}{
		{"null", `null`, ""},
		{"a name", `"review"`, ""},
		{"digits and hyphens", `"a-1-b"`, ""},
		{"64 characters", `"` + long + `"`, ""},
		{"missing", ``, "/template"},
		{"65 characters", `"` + long + `a"`, "/template"},
		{"empty", `""`, "/template"},
		{"capitals", `"Review"`, "/template"},
		{"all digits", `"12"`, "/template"},
		{"an underscore", `"a_b"`, "/template"},
		{"a leading hyphen", `"-a"`, "/template"},
		{"a trailing hyphen", `"a-"`, "/template"},
		{"a number", `1`, "/template"},
		{"an object", `{}`, "/template"},
	} {
		member := ""
		if tc.value != "" {
			member = `, "template": ` + tc.value
		}
		doc := `{"schema": 3, "id": 3, "job": null, "source": "hook", "placement": null, "extra": {}` + member + `}`
		h, r := ReadSesshin([]byte(doc))
		check(t, "sesshin.json "+tc.name, r, tc.field)
		if tc.field == "" && (tc.value == "null") != (h.Template == nil) {
			t.Errorf("sesshin.json %s: template %v", tc.name, h.Template)
		}
		if tc.field == "" && tc.value != "null" && `"`+*h.Template+`"` != tc.value {
			t.Errorf("sesshin.json %s: template %q", tc.name, *h.Template)
		}

		rdoc := `{"schema": 3, "job": null, "token": "` + fixtureToken + `", "created_at": "2026-10-03T18:31:51Z", "placement": null, "extra": {}` + member + `}`
		rf, rr := ReadReservation([]byte(rdoc), fixtureToken+".json")
		check(t, "reservation "+tc.name, rr, tc.field)
		if tc.field == "" && (tc.value == "null") != (rf.Template == nil) {
			t.Errorf("reservation %s: template %v", tc.name, rf.Template)
		}
	}
}

func check(t *testing.T, name string, r FileResult, field string) {
	t.Helper()
	switch {
	case field == "" && !r.Usable:
		t.Errorf("%s: unusable: %v", name, r.Problems)
	case field != "" && (r.Usable || r.Problems[0].Field != field):
		t.Errorf("%s: %+v, want a problem at %s", name, r.Problems, field)
	}
}

func TestIsVarKey(t *testing.T) {
	for k, want := range map[string]bool{
		"A": true, "_": true, "a1": true, "A_b9": true, strings.Repeat("K", 64): true,
		"": false, "1a": false, "a-b": false, "a b": false, "é": false, strings.Repeat("K", 65): false,
	} {
		if got := IsVarKey(k); got != want {
			t.Errorf("IsVarKey(%q) = %v", k, got)
		}
	}
}
