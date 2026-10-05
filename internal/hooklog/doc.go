// Package hooklog appends to hooks.log, the hooks' only channel to a person
// (hooks-spec.md, Log), and rotates it at 1 MiB (implementation-spec.md, Log
// rotation).
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package hooklog
