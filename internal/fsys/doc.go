// Package fsys is a thin interface over os.Root and flock, with a real
// implementation (OS) and a fault-injecting one (Fault). It is the only
// package that touches the disk (implementation-spec.md, Mechanism).
//
// Errors are returned as the OS gives them — *os.PathError or *os.LinkError
// around a syscall.Errno — because each call site decides what an errno
// means there (implementation-spec.md, OS errors); ErrnoOf and ErrnoName
// extract and name it, and Describe words it for the log. Only the errno is
// meaningful. An error's Path may be relative to the root or joined with it,
// and its Op differs between OS and Fault; callers report their own path.
// Nothing here creates a directory unless asked to.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package fsys
