// Package live derives each session's liveness: live, ended, or unknown
// (design-spec.md, Liveness; implementation-spec.md, Liveness). It judges
// all sessions together, since one process runs one live session (rule 3),
// and it is a pure function over files already read: the only lookup it
// makes is the start-time function it is given. Reading the session
// directories is the caller's job: the operations that read sessions
// (operations.md, Reading the sessions).
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package live
