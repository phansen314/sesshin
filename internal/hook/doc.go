// Package hook runs sesshin-hook's verbs under the hooks contract (hooks-spec.md,
// The contract). It never writes to stdout or stderr except the statusline's
// one final write, and never calls os.Exit.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package hook
