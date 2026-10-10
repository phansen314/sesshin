// Package iterm2 is the iTerm2 terminal backend (design-spec.md, The iTerm2
// backend), as a placement.Backend with every optional ability but sync
// (Backend): it recognizes iTerm2 from a hook's environment, replaces and
// validates the two keys of a session's placement, and, through fixed
// AppleScript run by osascript, launches a window for spawn (through a launch
// file, Store), says which panes exist, finds a session's pane by its tty,
// pastes into it for send, and focuses it. sesshin-hook calls only Recognize
// and Replace; it never starts osascript.
//
// sesshin-hook links it, so it imports the standard library and sesshin's own
// hook-path packages only, starts no goroutine, and has no init function or
// package-level initializer that does work (implementation-spec.md, The hook
// binary).
package iterm2
