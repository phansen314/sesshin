//go:build !darwin

package iterm2

import "syscall"

// rdevOf is st_rdev.
func rdevOf(st *syscall.Stat_t) uint64 { return uint64(st.Rdev) }
