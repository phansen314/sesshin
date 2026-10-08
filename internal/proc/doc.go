// Package proc finds Claude's process from a hook or the statusline: its
// pid, its start time with the boot it belongs to, and whether another
// session started it (design-spec.md, Liveness; implementation-spec.md,
// Process lookup). It reads the process table through fsys, from /proc on
// Linux, and never starts a process. On other systems it finds nothing
// until macOS is built.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package proc
