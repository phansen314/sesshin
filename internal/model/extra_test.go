package model

import (
	"strings"
	"testing"
)

func sesshinWithExtra(extra string) string {
	return `{"schema": 1, "id": 3, "job": null, "source": "hook", "placement": null, "extra": ` + extra + `}`
}

// Design-spec, User-owned extra: an object within 65,536 bytes of compact JSON
// and 32 levels, its own object counted; numbers inside are exempt from the
// integer-literal and float64 rules; a repeated key anywhere in it makes the
// file unusable.
func TestSesshinExtra(t *testing.T) {
	deep := func(n int) string { return strings.Repeat(`{"a":`, n-1) + `{}` + strings.Repeat("}", n-1) }
	val := func(n int) string { return `{"k":"` + strings.Repeat("x", n) + `"}` }
	exact := ExtraMaxBytes - len(`{"k":""}`)
	for _, tc := range []struct {
		name  string
		extra string
		field string // "": usable
	}{
		{"empty", `{}`, ""},
		{"numbers as written", `{"a":1.10,"b":1e400,"c":-0,"d":2.0,"e":[1E2,0.5]}`, ""},
		{"32 levels", deep(32), ""},
		{"33 levels", deep(33), "/extra"},
		{"nested arrays are not an object", strings.Repeat(`[`, 31) + `{}` + strings.Repeat(`]`, 31), "/extra"}, // not an object
		{"65536 bytes", val(exact), ""},
		{"65537 bytes", val(exact + 1), "/extra"},
		{"multi-byte counted in bytes", `{"k":"` + strings.Repeat("é", exact/2+1) + `"}`, "/extra"},
		{"an array", `[]`, "/extra"},
		{"null", `null`, "/extra"},
		{"a string", `"x"`, "/extra"},
		{"repeated key", `{"a":1,"a":2}`, "/extra"},
		{"repeated nested key", `{"n":{"a":1,"a":2}}`, "/extra/n"},
	} {
		h, r := ReadSesshin([]byte(sesshinWithExtra(tc.extra)))
		switch {
		case tc.field == "" && (!r.Usable || h.Extra == nil):
			t.Errorf("%s: unusable: %v", tc.name, r.Problems)
		case tc.field != "" && (r.Usable || !strings.HasPrefix(r.Problems[0].Field, tc.field)):
			t.Errorf("%s: %+v, want a problem at %s", tc.name, r.Problems, tc.field)
		}
	}

	// extra is required.
	if _, r := ReadSesshin([]byte(`{"schema": 1, "id": 3, "job": null, "source": "hook", "placement": null}`)); r.Usable || r.Problems[0].Field != "/extra" {
		t.Errorf("without extra: %+v", r)
	}
}

// An extra over a limit passes the published schema, which states only that
// it is an object: the limits are a rule beyond it.
func TestSesshinExtraLimitsBeyondSchema(t *testing.T) {
	doc := sesshinWithExtra(strings.Repeat(`{"a":`, 40) + `{}` + strings.Repeat("}", 40))
	_, r := ReadSesshin([]byte(doc))
	if r.Usable || len(r.SchemaProblems) != 0 {
		t.Errorf("%+v", r)
	}
}
