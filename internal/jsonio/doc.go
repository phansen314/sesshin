// Package jsonio writes the design spec's File format, and reads the JSON
// sesshin needs kept exactly: every sesshin file, settings.json, and kitty's ls
// output into an ordered tree, and the statusline payload as raw bytes
// (implementation-spec.md, JSON reading and JSON writing). Hook payloads are read by package payload.
//
// A tree value is one of: *Object, []any (an array of tree values), string,
// json.Number (the literal's exact text), bool, or nil (null).
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package jsonio
