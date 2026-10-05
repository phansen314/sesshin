// Package kitty is the kitty terminal backend (design-spec.md, Placement):
// it recognizes kitty from a hook's environment, replaces and validates the
// kitty keys of a session's placement, and asks kitty for the keys only its
// sync writes, says which windows exist, launches one for spawn (Launch), and
// finds a session's window and pastes into it for send (WindowForPID,
// SendText). sesshin-hook never calls Launch, WindowForPID, or SendText.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package kitty
