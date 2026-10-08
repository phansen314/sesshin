package jsonio

import (
	"strings"
	"testing"
)

func TestEqual(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		// Numbers, by exact value.
		{`1`, `1`, true},
		{`1`, `1.0`, true},
		{`100`, `1e2`, true},
		{`100`, `1E+2`, true},
		{`0.5`, `5e-1`, true},
		{`-0`, `0`, true},
		{`1`, `2`, false},
		{`1e400`, `2e400`, false},
		{`1e400`, `10e399`, true},
		{`12345678901234567890`, `12345678901234567891`, false},
		{`0.1`, `0.10000000000000001`, false},
		{`0`, `0.000`, true},
		{`0`, `-0e5`, true},
		{`0e99999999`, `0`, true},
		{`1.50`, `15e-1`, true},
		{`-1.5`, `1.5`, false},
		{`-1.5`, `-15e-1`, true},
		{`0.001`, `1e-3`, true},
		{`0.001`, `1e-4`, false},
		{`1000`, `1e3`, true},
		{`1000`, `1e4`, false},
		{`1.2e3`, `1200`, true},
		{`123`, `1.23e2`, true},
		{`123`, `1.23e3`, false},
		{`1`, `10`, false},
		// Exponents are compared, never expanded.
		{`1e99999999`, `1e99999999`, true},
		{`1e99999999`, `10e99999998`, true},
		{`1e99999999`, `0.1e100000000`, true},
		{`1e99999999`, `1e99999998`, false},
		{`1e99999999`, `1`, false},
		{`1e-99999999`, `0.01e-99999997`, true},
		{`1e-99999999`, `1e99999999`, false},
		{`1e999999999999999999999999`, `10e999999999999999999999998`, true},
		{`1e999999999999999999999999`, `1e999999999999999999999998`, false},
		{`1e-999999999999999999999999`, `1e999999999999999999999999`, false},
		// Other scalars.
		{`"a"`, `"a"`, true},
		{`"a"`, `"b"`, false},
		{`"1"`, `1`, false},
		{`true`, `true`, true},
		{`true`, `false`, false},
		{`null`, `null`, true},
		{`null`, `false`, false},
		{`null`, `0`, false},
		// Objects, regardless of member order.
		{`{}`, `{}`, true},
		{`{"a": 1, "b": 2}`, `{"b": 2.0, "a": 1}`, true},
		{`{"a": 1}`, `{"a": 1, "b": 2}`, false},
		{`{"a": 1, "b": 2}`, `{"a": 1}`, false},
		{`{"a": 1}`, `{"b": 1}`, false},
		{`{"a": {"x": [1, {"y": null}]}}`, `{"a": {"x": [1.0, {"y": null}]}}`, true},
		{`{"a": {"x": [1, {"y": null}]}}`, `{"a": {"x": [1, {"y": false}]}}`, false},
		// Arrays, in order.
		{`[1, 2]`, `[1, 2]`, true},
		{`[1, 2]`, `[2, 1]`, false},
		{`[1]`, `[1, 1]`, false},
		{`[]`, `{}`, false},
	} {
		a, _, err := ParseValue([]byte(tc.a))
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := ParseValue([]byte(tc.b))
		if err != nil {
			t.Fatal(err)
		}
		if got := Equal(a, b); got != tc.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := Equal(b, a); got != tc.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", tc.b, tc.a, got, tc.want)
		}
	}
}

// A 64 KiB exponent, or as many digits, is compared at once: nothing is
// expanded to the exponent's size.
func TestEqualHugeNumbers(t *testing.T) {
	exp := strings.Repeat("9", 64<<10)
	digits := strings.Repeat("1", 64<<10)
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"1e" + exp, "1e" + exp, true},
		{"1e" + exp, "10e" + exp[:len(exp)-1] + "8", true},
		{"1e" + exp, "1e-" + exp, false},
		{"1e" + exp, "2e" + exp, false},
		{digits + "e" + exp, digits + "0e" + exp[:len(exp)-1] + "8", true},
		{"0." + digits, "0." + digits + "0", true},
		{"0." + digits, "0." + digits + "1", false},
	} {
		a, _, err := ParseValue([]byte(tc.a))
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := ParseValue([]byte(tc.b))
		if err != nil {
			t.Fatal(err)
		}
		if got := Equal(a, b); got != tc.want {
			t.Errorf("Equal(%.20s…, %.20s…) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
