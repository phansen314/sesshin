package jsonio

import (
	"encoding/json"
	"math/big"
	"strings"
)

// Equal reports whether a and b, values as ParseValue returns them, are equal
// as JSON values (implementation-spec.md, Extra, update):
// objects regardless of member order, arrays element by element, numbers by
// exact numeric value, whatever the exponent (see sameNumber). Objects are
// compared by their first member of each key, as Get finds it; callers compare
// objects without repeated keys.
func Equal(a, b any) bool {
	switch a := a.(type) {
	case json.Number:
		b, ok := b.(json.Number)
		if !ok {
			return false
		}
		if a == b {
			return true
		}
		return sameNumber(string(a), string(b))
	case *Object:
		b, ok := b.(*Object)
		if !ok || a.Len() != b.Len() {
			return false
		}
		for _, m := range a.Members {
			v, ok := b.Get(m.Key)
			if !ok || !Equal(m.Value, v) {
				return false
			}
		}
		return true
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !Equal(a[i], b[i]) {
				return false
			}
		}
		return true
	case string, bool, nil:
		return a == b
	}
	return false
}

// sameNumber compares two JSON numbers by value without expanding them: each
// is reduced to its significant digits and a power of ten, and those are
// compared. big.Rat would expand an exponent into a number of that many
// digits, and one in an extra can be as large as the file allows.
func sameNumber(a, b string) bool {
	na, okA := normalize(a)
	nb, okB := normalize(b)
	return okA && okB && na.neg == nb.neg && na.digits == nb.digits && na.exp.Cmp(nb.exp) == 0
}

// normal is a number as ±0.digits × 10^exp: digits has no leading or trailing
// zeros, and zero is no digits, positive, with exp 0.
type normal struct {
	neg    bool
	digits string
	exp    *big.Int
}

// normalize reads a number in JSON's grammar; ok is false for text that
// isn't.
func normalize(s string) (n normal, ok bool) {
	s, n.neg = strings.CutPrefix(s, "-")
	mant, exp, _ := strings.Cut(strings.ToLower(s), "e")
	whole, frac, _ := strings.Cut(mant, ".")
	if whole == "" || strings.Trim(whole+frac, "0123456789") != "" {
		return n, false
	}
	n.exp = new(big.Int)
	if exp != "" {
		exp = strings.TrimPrefix(exp, "+")
		if _, ok := n.exp.SetString(exp, 10); !ok {
			return n, false
		}
	}
	digits := strings.TrimLeft(whole+frac, "0")
	if digits == "" {
		return normal{exp: new(big.Int)}, true
	}
	// The point sits len(frac) digits from the end, and the value is
	// 0.digits × 10^(len(digits) + exponent - len(frac)).
	n.exp.Add(n.exp, big.NewInt(int64(len(digits)-len(frac))))
	n.digits = strings.TrimRight(digits, "0")
	return n, true
}
