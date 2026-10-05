package cli

import (
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
)

// Type is how an option's value becomes its input field
// (implementation-spec.md, The CLI).
type Type int

const (
	// Bool is a flag: --x sets true, --x=false sets false.
	Bool Type = iota
	// Int becomes a number if shaped like a JSON integer, else stays a
	// string, which the operation rejects as the wrong type.
	Int
	// String is the value as a string.
	String
	// List is a comma-separated list of strings, which the option may repeat:
	// its lists are joined in order. The empty string is the empty list.
	List
	// Map is one KEY=VALUE per option, split at the first "=", into the
	// object at the field; the option may repeat. A token without "=", or a
	// KEY twice, is invalid-input at the field.
	Map
	// File reads the file named by the value, exactly, into a string field;
	// "-" is stdin, which is then read for nothing else.
	File
	// Dir is a directory path, resolved by resolveDir; when the option isn't
	// given, the working directory is the value (not with --input).
	Dir
)

// Command is one CLI command: the operation it runs and how its options map
// onto that operation's input fields.
type Command struct {
	Name    string
	Summary string // one line, for help
	// Arguments are the positional arguments, all required unless --input is
	// given.
	Arguments []Argument
	Options   []Option
	// OneOf names options of which one is required unless --input is given
	// (when they set one field, at most one may be).
	OneOf []string
	// Rest is the JSON Pointer of the field that the tokens after "--" set,
	// as a list of strings, in order; "" for a command that takes none. The
	// field is always set, to the empty list when there are none.
	Rest string
	// RestName names the tokens after "--" in the usage line.
	RestName string
	Example  string
	// Run checks the input and runs the operation (see operation).
	Run func(src Source, env Env) ops.Envelope
}

// Argument is a required positional argument. Help names it in the usage
// line, as <name>.
type Argument struct {
	Name  string
	Field string // JSON Pointer of the input field it sets
}

// Option is a command-specific option. Help may name the value in
// backquotes, as pflag shows it: "keep the last `days`".
type Option struct {
	Name     string // long form, without "--"
	Field    string // JSON Pointer of the input field it sets
	Type     Type
	Required bool // unless --input is given
	Help     string
}

// Source is the input the CLI hands an operation: Data, as read from --input,
// when FromFile; otherwise Object, built from the options given, and the
// problems found while building it.
type Source struct {
	Object   *jsonio.Object
	Problems []model.Problem
	Data     []byte
	FromFile bool
}

// operation binds an operation's input decoder to the function that runs it:
// the input is checked as --input's, strictly, or as built from options, and
// every problem is reported before the operation runs.
func operation[T any](decode func(*model.Fields, *model.Problems) T, run func(T, Env) ops.Envelope) func(Source, Env) ops.Envelope {
	return func(src Source, env Env) ops.Envelope {
		var in T
		var e *ops.Error
		if src.FromFile {
			in, e = ops.DecodeInput(src.Data, decode)
		} else {
			p := &model.Problems{}
			for _, pr := range src.Problems {
				p.Add(pr.Field, pr.Reason)
			}
			in, e = ops.CheckInput(src.Object, p, decode)
		}
		if e != nil {
			return ops.Failed(e)
		}
		return run(in, env)
	}
}
