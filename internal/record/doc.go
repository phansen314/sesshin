// Package record is Recording an event (hooks-spec.md): the one place
// lifecycle.json is written by an event, and the place sesshin.json is created,
// completed, and given its sesshin ID. A lifecycle verb maps its payload to an
// Event and calls Record.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package record
