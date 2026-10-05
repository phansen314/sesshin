package jsonio

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// MarshalFile encodes v in the design spec's File format: two-space indent,
// one member or item per line, empty objects and arrays as {} and [], minimal
// string escaping, and a single trailing newline. Key order comes from struct
// field order, from an Object's stored order, and from a stored payload as
// received (see Payload). Every file sesshin writes goes through it.
func MarshalFile(v any) ([]byte, error) {
	return marshal(v, "  ")
}

// MarshalLine encodes v compactly on one line, followed by a newline: the
// CLI's output envelope.
func MarshalLine(v any) ([]byte, error) {
	return marshal(v, "")
}

// With HTML escaping off, the encoder escapes exactly ", \, U+0000–U+001F,
// U+2028, and U+2029 in the strings it encodes: the File format's list.
func marshal(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(nonNil(v)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// marshalerType is a function, not a package-level variable: sesshin-hook links
// this package, and a package-level initializer that does work would run in
// every hook.
func marshalerType() reflect.Type { return reflect.TypeFor[json.Marshaler]() }

// nonNil returns v with every nil slice and map replaced by an empty one, so
// each encodes as [] or {}, never null (implementation-spec.md, JSON writing).
// v itself is never modified: structs, slices, maps, and pointers on the way
// to a replacement are copied. Values that marshal themselves (an Object, a
// stored payload) and byte slices, which encode as strings, are left alone.
// v must not contain a pointer cycle.
func nonNil(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if out, changed := fill(rv); changed {
		return out.Interface()
	}
	return v
}

// fill returns v with nil collections replaced, and whether anything was; when
// nothing was, the returned value is v itself.
func fill(v reflect.Value) (reflect.Value, bool) {
	t := v.Type()
	if t.Implements(marshalerType()) || reflect.PointerTo(t).Implements(marshalerType()) && t.Kind() != reflect.Pointer {
		return v, false
	}
	switch t.Kind() {
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return v, false
		}
		if v.IsNil() {
			return reflect.MakeSlice(t, 0, 0), true
		}
		var out reflect.Value
		for i := range v.Len() {
			e, changed := fill(v.Index(i))
			if !changed {
				continue
			}
			if !out.IsValid() {
				out = reflect.MakeSlice(t, v.Len(), v.Len())
				reflect.Copy(out, v)
			}
			out.Index(i).Set(e)
		}
		if out.IsValid() {
			return out, true
		}
		return v, false
	case reflect.Map:
		if v.IsNil() {
			return reflect.MakeMap(t), true
		}
		var out reflect.Value
		iter := v.MapRange()
		for iter.Next() {
			e, changed := fill(iter.Value())
			if !changed {
				continue
			}
			if !out.IsValid() {
				out = reflect.MakeMapWithSize(t, v.Len())
				for it := v.MapRange(); it.Next(); {
					out.SetMapIndex(it.Key(), it.Value())
				}
			}
			out.SetMapIndex(iter.Key(), e)
		}
		if out.IsValid() {
			return out, true
		}
		return v, false
	case reflect.Pointer:
		if v.IsNil() {
			return v, false
		}
		e, changed := fill(v.Elem())
		if !changed {
			return v, false
		}
		out := reflect.New(t.Elem())
		out.Elem().Set(e)
		return out, true
	case reflect.Interface:
		if v.IsNil() {
			return v, false
		}
		e, changed := fill(v.Elem())
		if !changed {
			return v, false
		}
		out := reflect.New(t).Elem()
		out.Set(e)
		return out, true
	case reflect.Struct:
		var out reflect.Value
		for i := range t.NumField() {
			if !t.Field(i).IsExported() {
				continue
			}
			e, changed := fill(v.Field(i))
			if !changed {
				continue
			}
			if !out.IsValid() {
				out = reflect.New(t).Elem()
				out.Set(v)
			}
			out.Field(i).Set(e)
		}
		if out.IsValid() {
			return out, true
		}
		return v, false
	}
	return v, false
}
