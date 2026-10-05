// Package testhook is the seam for injecting a panic or a fatal error at a
// chosen point of a hook (implementation-spec.md, Test hooks). In the
// shipped build At is empty; in a build with -tags sesshintest it reads
// SESSHIN_TEST_PANIC and fails when the point matches.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package testhook
