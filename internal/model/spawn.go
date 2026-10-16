package model

import (
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/placement"
)

// SpawnInput is spawn's input (spawn-input) as given: nothing is defaulted,
// since a template's value must win over a default (operations.md, Merging a
// template). It lives here so that templates, which ops imports, can merge it.
type SpawnInput struct {
	// Template is the template to merge over; "" for none.
	Template string
	// Job is the job to reserve; "" for none.
	Job string
	Cwd string
	// Type is tab, split, or os-window; "" when not given.
	Type string
	// Name is the session's name, for claude --name and the tab title; ""
	// for the job, else neither.
	Name string
	// Prompt is the first prompt; nil for none given. A given "" replaces
	// a template's prompt, leaving none (operations.md, Merging a template).
	Prompt *string
	Args   []string
	// Vars are the new window's user variables, in the order given.
	Vars []placement.Var
	// Extra is the session's user-owned extra, handed over in the
	// reservation; nil for {}.
	Extra *jsonio.Object
	// StartTimeoutSecs is nil when the input gave none.
	StartTimeoutSecs *int64
}

// IsVarKey is vars' key rule: ^[A-Za-z_][A-Za-z0-9_]{0,63}$.
func IsVarKey(k string) bool {
	if k == "" || len(k) > 64 {
		return false
	}
	for i := range len(k) {
		c := k[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
