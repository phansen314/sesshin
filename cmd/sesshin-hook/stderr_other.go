//go:build !linux

package main

import "syscall"

// dup makes newfd a copy of oldfd. Dup3 doesn't exist on darwin.
func dup(oldfd, newfd int) error { return syscall.Dup2(oldfd, newfd) }
