// Package proc finds Claude's process from a hook or the statusline: its
// pid, its start time with the boot it belongs to, and whether another
// session started it (design-spec.md, Liveness; implementation-spec.md,
// Process lookup). The rules are one implementation over a process table
// (procTable); only the table differs by system: /proc through fsys on
// Linux (procfs), sysctl through golang.org/x/sys/unix on macOS
// (sysctlTable). Both tables' parsing builds everywhere, so it is tested on
// any system. It never starts a process. On other systems it finds nothing.
//
// sesshin-hook links it, so it imports the standard library, sesshin's own
// hook-path packages, and golang.org/x/sys/unix only, starts no goroutine,
// and has no init function or package-level initializer that does work
// (implementation-spec.md, The hook binary).
package proc
