// Package placement holds what every terminal backend shares: the rule that
// rules a session out of placement before any backend is asked.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package placement
