//go:build !sesshintest

package testhook

// At is the injection point named point. In the shipped build it does
// nothing, and the compiler removes the call.
func At(point string) {}
