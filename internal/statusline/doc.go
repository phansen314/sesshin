// Package statusline is the statusline's tick (hooks-spec.md, statusline, and
// Rendering): Run collects what steps 1–4 find (the previous statusline.json,
// Claude's process, git_branch, and the burn rate), renders the line (step 5),
// and writes statusline.json (step 6), in that order. Each step recovers its
// own panic and logs it, so a panic costs that step's result and never the
// line.
//
// Rendering reads sesshin.json for the sesshin ID, for display only: nothing written
// to statusline.json comes from sesshin.json, and recording never reads it
// (design-spec.md, Two tiers).
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package statusline
