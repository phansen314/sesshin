package model

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// MaxSafe is the largest integer every JSON reader holds exactly, 2^53 − 1:
// the maximum of the files' integer fields but received_ns.
const MaxSafe = 1<<53 - 1

// FileResult is the outcome of reading one of sesshin's files. A file that
// isn't usable is treated as missing, and the next write of it replaces it
// (design-spec.md, File schemas and Format versions).
type FileResult struct {
	// Usable: the file passed every check. Its content is meaningful only
	// then.
	Usable bool
	// Early: the file failed before its fields were checked. It couldn't be
	// read as one JSON object, or its schema isn't this binary's version;
	// Problems holds that one problem. Test bookkeeping: the agreement tests
	// compare such a file by Problems alone.
	Early bool
	// OtherFormat: the file is in another format, its schema an integer
	// literal within ±MaxSafe but not the supported version, Found. It is
	// Early too; callers that leave such files alone tell it from corrupt by
	// this (implementation-spec.md, Validation). A repeated schema key never
	// sets it.
	OtherFormat bool
	// Found is the schema version found, set only with OtherFormat.
	Found int64
	// Problems say why the file isn't usable, each at a JSON Pointer into it:
	// repeated keys first, then fields in schema order.
	Problems []Problem
	// SchemaProblems are those of a file that failed at its fields that its
	// published schema also finds: not repeated keys, nor the rules beyond the
	// schema (Problems.SchemaList). Integer-literal problems are included,
	// though the schema accepts an integral 2.0, since one reason covers 2.0
	// and 2.5 alike; the agreement tests leave such numbers out
	// (implementation-spec.md, Integer literals). Test bookkeeping for the
	// agreement tests; nothing at run time reads it.
	SchemaProblems []Problem
}

// Reason describes why the file isn't usable, for the log: its first
// problem, and how many more there are.
func (r FileResult) Reason() string {
	if len(r.Problems) == 0 {
		return ""
	}
	pr := r.Problems[0]
	s := pr.Reason
	if pr.Field != "" {
		s = pr.Field + ": " + s
	}
	if n := len(r.Problems) - 1; n > 0 {
		s += " (and " + strconv.Itoa(n) + " more)"
	}
	return s
}

// integerLiteral reads v as a schema version: a number whose text is an
// integer literal (no '.', 'e', 'E') within ±MaxSafe.
func integerLiteral(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok || strings.ContainsAny(string(n), ".eE") {
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || i > MaxSafe || i < -MaxSafe {
		return 0, false
	}
	return i, true
}

func early(field, reason string) FileResult {
	return FileResult{Early: true, Problems: []Problem{{Field: field, Reason: reason}}}
}

// readFile reads data as one of sesshin's files, in the order of
// implementation-spec.md, Validation: read as one JSON object, into the
// ordered tree; schema the integer literal of supported; no repeated key;
// then every field, by fields, which checks each member it asks for and
// copies it into the file's struct as it passes. Members fields doesn't ask
// for are unknown. The struct is the zero value unless the result is usable.
func readFile[T any](data []byte, supported int64, fields func(v *T, f *Fields, p *Problems)) (T, FileResult) {
	var zero T
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		return zero, early("", err.Error())
	}
	want := strconv.FormatInt(supported, 10)
	schemaRepeated := slices.Contains(repeated, "/schema")
	if v, ok := obj.Get("schema"); !ok {
		return zero, early("/schema", reasonRequired)
	} else if n, ok := v.(json.Number); !ok || string(n) != want {
		// A repeated schema has an ambiguous version: corrupt, found below.
		if !schemaRepeated {
			if found, isInt := integerLiteral(v); isInt {
				r := early("/schema", "in format "+strconv.FormatInt(found, 10)+", not "+want)
				r.OtherFormat, r.Found = true, found
				return zero, r
			}
			return zero, early("/schema", "must be "+want+", the version this binary supports")
		}
	}

	var p Problems
	for _, ptr := range repeated {
		p.AddAdditional(ptr, reasonRepeated)
	}
	f, _ := p.Object(obj, "")
	f.Required("schema") // checked above
	var v T
	fields(&v, f, &p)
	f.Done()
	if p.OK() {
		return v, FileResult{Usable: true}
	}
	return zero, FileResult{Problems: p.List(), SchemaProblems: p.SchemaList()}
}
