// Package backends is the list of terminal backends this binary has, in
// detection order, the most specific variables first (design-spec.md,
// Terminal backends). It is a package of its own because placement cannot
// import the backends that import it.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package backends

import (
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// All returns the backends, in detection order: a new slice each call, so no
// caller can change another's. kitty is the only one.
func All() []placement.Backend { return []placement.Backend{kitty.Backend{}} }
