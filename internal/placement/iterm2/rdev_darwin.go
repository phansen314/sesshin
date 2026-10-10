package iterm2

import "syscall"

// rdevOf is st_rdev as proc.ControllingTTY reports a device: macOS's is a
// signed 32-bit integer, and a device with its top bit set must not be
// sign-extended.
func rdevOf(st *syscall.Stat_t) uint64 { return uint64(uint32(st.Rdev)) }
