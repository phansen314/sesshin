// Package hookconf reads hooks.properties, sesshin-hook's one setting
// (design-spec.md, Hook settings; implementation-spec.md, Settings). It is
// parsed by hand, with no TOML.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package hookconf
