// Package payload reads a hook's JSON payload into one struct
// (hooks-spec.md, Reading the payload; implementation-spec.md, JSON
// reading). Every field is optional: one absent or of the wrong type is
// empty, and the rest still decode. Strings come out scrubbed and guarded,
// ready to store, and the session UUID lowercased, or empty when it isn't
// one.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package payload
