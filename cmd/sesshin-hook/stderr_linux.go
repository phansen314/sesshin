package main

import "syscall"

// dup makes newfd a copy of oldfd. Dup2 doesn't exist on linux/arm64.
func dup(oldfd, newfd int) error { return syscall.Dup3(oldfd, newfd, 0) }
