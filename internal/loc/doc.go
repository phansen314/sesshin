// Package loc resolves sesshin's locations: the config directory, the state
// directory, and Claude Code's settings.json (design-spec.md, Locations).
//
// Resolution is a pure function of the platform and the environment: it reads
// HOME directly, never os/user or /etc/passwd, cleans the paths it returns
// without resolving symlinks, and creates nothing.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package loc
