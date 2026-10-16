package templates

import (
	"errors"
	"slices"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
)

// The built-in defaults, applied after the merge (operations.md, Merging a
// template).
const (
	defaultType         = "tab"
	defaultStartTimeout = 15
)

// ErrNoCwd is Merge's failure when neither the input, the template, nor the
// caller's working directory gives a cwd: invalid-input at /cwd.
var ErrNoCwd = errors.New("no cwd")

// WorkingDirError is Merge's failure when the caller's working directory is
// needed and can't be determined: io.
type WorkingDirError struct{ Err error }

func (e *WorkingDirError) Error() string { return "the working directory: " + e.Err.Error() }
func (e *WorkingDirError) Unwrap() error { return e.Err }

// Merged is spawn's input after the merge, with the defaults applied.
type Merged struct {
	// Template is the input's template name; "" for none.
	Template         string
	Job              string
	Cwd              string
	Type             string
	Name             string
	Prompt           string
	Args             []string
	Vars             []placement.Var
	Extra            *jsonio.Object
	StartTimeoutSecs int64
}

// Merge is the table in operations.md, Merging a template: the input over t.
// A nil t merges with nothing. callerCwd is the caller's working directory,
// asked for only when neither the input nor the template gives cwd; nil for
// input from -i, which has none.
func Merge(in model.SpawnInput, t *Template, callerCwd func() (string, error)) (Merged, error) {
	var tt Template
	if t != nil {
		tt = *t
	}
	m := Merged{
		Template:         in.Template,
		Job:              first(in.Job, tt.Job),
		Cwd:              first(in.Cwd, tt.Cwd),
		Type:             first(in.Type, tt.Type),
		Name:             first(in.Name, tt.Name),
		Prompt:           firstPtr(in.Prompt, tt.Prompt),
		Args:             append(slices.Clone(tt.Args), in.Args...),
		Vars:             slices.Clone(tt.Vars),
		Extra:            in.Extra,
		StartTimeoutSecs: defaultStartTimeout,
	}
	if m.Args == nil {
		m.Args = []string{}
	}
	for _, v := range in.Vars {
		if i := slices.IndexFunc(m.Vars, func(w placement.Var) bool { return w.Name == v.Name }); i >= 0 {
			m.Vars[i].Value = v.Value
		} else {
			m.Vars = append(m.Vars, v)
		}
	}
	switch {
	case in.StartTimeoutSecs != nil:
		m.StartTimeoutSecs = *in.StartTimeoutSecs
	case tt.StartTimeoutSecs != nil:
		m.StartTimeoutSecs = *tt.StartTimeoutSecs
	}
	if m.Cwd == "" && callerCwd != nil {
		wd, err := callerCwd()
		if err != nil {
			return Merged{}, &WorkingDirError{Err: err}
		}
		m.Cwd = wd
	}
	if m.Cwd == "" {
		return Merged{}, ErrNoCwd
	}
	if m.Type == "" {
		m.Type = defaultType
	}
	if m.Name == "" {
		m.Name = m.Job
	}
	return m, nil
}

// firstPtr is the input's value when it gave one, even "", else the
// template's, else "": a given prompt replaces the template's whole.
func firstPtr(in, t *string) string {
	switch {
	case in != nil:
		return *in
	case t != nil:
		return *t
	}
	return ""
}

// first is the input's value when it gave one, else the template's, else "".
func first(in string, t *string) string {
	switch {
	case in != "":
		return in
	case t != nil:
		return *t
	}
	return ""
}
