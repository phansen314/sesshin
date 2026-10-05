package model

import (
	"encoding/json"
	"strconv"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Problem is one thing wrong with a file or an operation's input: Field is a
// JSON Pointer into it, "" for the whole (operations.md, Error kinds:
// invalid-input's problems).
type Problem struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// Problems collects every problem a validator finds, each at the JSON Pointer
// of the field it concerns. A validator checks each field once, so no field
// is reported twice for one rule.
type Problems struct {
	list []Problem
	// additional is, per problem, whether it is from a rule no schema
	// expresses. Bookkeeping for the schema agreement tests only.
	additional []bool
}

// Add records a problem at field.
func (p *Problems) Add(field, reason string) {
	p.add(field, reason, false)
}

// AddAdditional records a problem found by a rule the published schema can't
// express: one stated only in prose (design-spec.md, File fields), or an
// operation's Additional validation. Such problems are reported like any
// other; the schema agreement tests leave them out (see SchemaList), which
// is all the marking is for.
func (p *Problems) AddAdditional(field, reason string) {
	p.add(field, reason, true)
}

func (p *Problems) add(field, reason string, additional bool) {
	p.list = append(p.list, Problem{Field: field, Reason: reason})
	p.additional = append(p.additional, additional)
}

// OK reports whether no problem was found.
func (p *Problems) OK() bool { return len(p.list) == 0 }

// List returns the problems in the order found.
func (p *Problems) List() []Problem { return p.list }

// SchemaList returns the problems the published schema also finds: every
// problem not added with AddAdditional. Test bookkeeping for the agreement
// tests; nothing at run time reads it.
func (p *Problems) SchemaList() []Problem {
	var out []Problem
	for i, pr := range p.list {
		if !p.additional[i] {
			out = append(out, pr)
		}
	}
	return out
}

// Reasons shared by every validator, so one mistake reads the same everywhere.
const (
	reasonObject   = "expected a JSON object"
	reasonString   = "expected a string"
	reasonBool     = "expected a boolean"
	reasonInteger  = "expected an integer"
	reasonNumber   = "expected a number"
	reasonRequired = "required"
	reasonUnknown  = "unknown field"
	reasonLiteral  = "must be an integer written without a fraction or exponent (2, not 2.0 or 2e0)"
	reasonRepeated = "repeated key"
)

// Fields walks one JSON object's members for a validator. Missing and unknown
// fields are reported at the field's own pointer.
type Fields struct {
	obj  *jsonio.Object
	ptr  string
	p    *Problems
	used []bool // per member
}

// Object checks that v, at ptr, is an object, and returns a walker over it.
func (p *Problems) Object(v any, ptr string) (*Fields, bool) {
	o, ok := v.(*jsonio.Object)
	if !ok {
		p.Add(ptr, reasonObject)
		return nil, false
	}
	return &Fields{obj: o, ptr: ptr, p: p, used: make([]bool, len(o.Members))}, true
}

// Ptr returns the pointer of the member key.
func (f *Fields) Ptr(key string) string { return jsonio.Pointer(f.ptr, key) }

// Required returns the member key, reporting it when missing.
func (f *Fields) Required(key string) (any, bool) {
	v, ok := f.Optional(key)
	if !ok {
		f.p.Add(f.Ptr(key), reasonRequired)
	}
	return v, ok
}

// Optional returns the member key, if present. A repeated key's first value
// is returned, and every copy is marked asked for: the repeat itself is
// reported by whoever read the tree.
func (f *Fields) Optional(key string) (any, bool) {
	var v any
	found := false
	for i, m := range f.obj.Members {
		if m.Key == key {
			if !found {
				v, found = m.Value, true
			}
			f.used[i] = true
		}
	}
	return v, found
}

// Done reports every member not asked for as unknown, once per key. Call it
// after asking for every allowed field.
func (f *Fields) Done() {
	for i, m := range f.obj.Members {
		if f.used[i] {
			continue
		}
		for j := i; j < len(f.obj.Members); j++ {
			if f.obj.Members[j].Key == m.Key {
				f.used[j] = true
			}
		}
		f.p.Add(f.Ptr(m.Key), reasonUnknown)
	}
}

// String checks that v, at ptr, is a string.
func (p *Problems) String(v any, ptr string) (string, bool) {
	s, ok := v.(string)
	if !ok {
		p.Add(ptr, reasonString)
	}
	return s, ok
}

// Bool checks that v, at ptr, is a boolean.
func (p *Problems) Bool(v any, ptr string) (bool, bool) {
	b, ok := v.(bool)
	if !ok {
		p.Add(ptr, reasonBool)
	}
	return b, ok
}

// nullable checks v, at ptr, as null or by check: a field whose schema allows
// null beside another type. It returns nil for null.
func nullable[T any](p *Problems, v any, ptr string, check func(*Problems, any, string) (T, bool)) (*T, bool) {
	if v == nil {
		return nil, true
	}
	x, ok := check(p, v, ptr)
	if !ok {
		return nil, false
	}
	return &x, true
}

// Int checks that v, at ptr, is an integer written as an integer literal,
// within [lo, hi] (implementation-spec.md, Integer literals).
func (p *Problems) Int(v any, ptr string, lo, hi int64) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		p.Add(ptr, reasonInteger)
		return 0, false
	}
	if !isIntegerLiteral(string(n)) {
		p.Add(ptr, reasonLiteral)
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || i < lo || i > hi {
		p.Add(ptr, "must be between "+strconv.FormatInt(lo, 10)+" and "+strconv.FormatInt(hi, 10))
		return 0, false
	}
	return i, true
}

// NonNegative checks that v, at ptr, is a number ≥ 0, written any way JSON
// allows: statusline.json's two non-integer numbers. Whether it is negative
// is read from its text, since a negative number too small for a float64
// parses as -0. One past float64's range is a rule beyond the schema, which
// accepts any number.
func (p *Problems) NonNegative(v any, ptr string) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		p.Add(ptr, reasonNumber)
		return 0, false
	}
	if negative(string(n)) {
		p.Add(ptr, "must be at least 0")
		return 0, false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		p.AddAdditional(ptr, "must be within a float64's range")
		return 0, false
	}
	return f, true
}

// isIntegerLiteral reports whether s, a JSON number's text, is written as
// -?(0|[1-9][0-9]*): no fraction, no exponent.
func isIntegerLiteral(s string) bool {
	if len(s) > 0 && s[0] == '-' {
		s = s[1:]
	}
	if s == "" || s[0] == '0' && len(s) > 1 {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// negative reports whether s, a JSON number's text, is below zero: a minus
// sign and a nonzero digit before any exponent.
func negative(s string) bool {
	if len(s) == 0 || s[0] != '-' {
		return false
	}
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == 'e' || c == 'E':
			return false
		case c >= '1' && c <= '9':
			return true
		}
	}
	return false
}
