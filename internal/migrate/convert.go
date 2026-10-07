package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

// ErrorKind says why a file couldn't be converted.
type ErrorKind int

const (
	// Corrupt: not one JSON object, or no usable integer `schema`. The caller
	// leaves the file alone and reports nothing: hooks replace it.
	Corrupt ErrorKind = iota + 1
	// Newer: the file's schema is past this binary's. The caller leaves it
	// alone, as unusable (Found is the version).
	Newer
	// Unconverted: the file is in an older format that the steps can't read,
	// or the result isn't usable. Detail says why; the caller lists it.
	Unconverted
)

// Error is Convert's failure. Match with errors.As and switch on Kind.
type Error struct {
	Kind ErrorKind
	// Found is the file's schema, for Newer and Unconverted.
	Found int64
	// Detail is human-readable.
	Detail string
}

func (e *Error) Error() string { return e.Detail }

// Current is the schema this binary supports for kind, and whether any step
// can cover the kind.
func Current(kind Kind) (int64, bool) {
	switch kind {
	case State:
		return model.StateSchema, true
	case Sesshin:
		return model.SesshinSchema, true
	}
	return 0, false
}

// Convert brings the content of one file of the given kind to this binary's
// format. changed is false, with out nil, when the file is already current
// (and for a kind no step covers); then nothing is to be written. Otherwise
// out is the converted file in sesshin's File format, which model's reader
// found usable. Failures are *Error.
func Convert(kind Kind, data []byte) (out []byte, changed bool, err error) {
	current, ok := Current(kind)
	if !ok {
		return nil, false, nil
	}
	obj, repeated, perr := jsonio.ParseObject(data)
	if perr != nil {
		return nil, false, &Error{Kind: Corrupt, Detail: perr.Error()}
	}
	if slices.Contains(repeated, "/schema") {
		return nil, false, &Error{Kind: Corrupt, Detail: "/schema: repeated key"}
	}
	v, ok := obj.Get("schema")
	if !ok {
		return nil, false, &Error{Kind: Corrupt, Detail: "/schema: required"}
	}
	found, ok := schemaLiteral(v)
	if !ok {
		return nil, false, &Error{Kind: Corrupt, Detail: "/schema: not an integer literal"}
	}
	switch {
	case found == current:
		return nil, false, nil
	case found > current:
		return nil, false, &Error{Kind: Newer, Found: found,
			Detail: fmt.Sprintf("in format %d, newer than %d", found, current)}
	}

	cur := found
	for _, s := range Steps {
		fs, ok := s.Files[kind]
		if !ok || fs.From != cur {
			continue
		}
		next, aerr := fs.Apply(obj)
		if aerr != nil {
			return nil, false, &Error{Kind: Unconverted, Found: found,
				Detail: fmt.Sprintf("migration %d (%s): %v", s.N, s.Name, aerr)}
		}
		obj, cur = next, fs.From+1
	}
	if cur != current {
		return nil, false, &Error{Kind: Unconverted, Found: found,
			Detail: fmt.Sprintf("no migration takes it from format %d", cur)}
	}

	out, merr := jsonio.MarshalFile(obj)
	if merr != nil {
		return nil, false, &Error{Kind: Unconverted, Found: found, Detail: merr.Error()}
	}
	if r := readBack(kind, out); !r.Usable {
		return nil, false, &Error{Kind: Unconverted, Found: found, Detail: "result not usable: " + r.Reason()}
	}
	return out, true, nil
}

func readBack(kind Kind, data []byte) model.FileResult {
	switch kind {
	case State:
		_, r := model.ReadState(data)
		return r
	default:
		_, r := model.ReadSesshin(data)
		return r
	}
}

// schemaLiteral reads v as an integer literal.
func schemaLiteral(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok || strings.ContainsAny(string(n), ".eE") {
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || i > model.MaxSafe || i < -model.MaxSafe {
		return 0, false
	}
	return i, true
}

func jsonNumber(s string) json.Number { return json.Number(s) }

// IsKind reports whether err is a *Error of kind k.
func IsKind(err error, k ErrorKind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == k
}
