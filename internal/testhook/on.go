//go:build sesshintest

package testhook

import (
	"os"
	"runtime/debug"
	"strings"
)

// fired is set by the first point that matches: a point fails once.
var fired bool

// At is the injection point named point. With SESSHIN_TEST_PANIC=<point> it
// panics, the first time it is reached; with SESSHIN_TEST_PANIC=fatal:<point> it
// ends the process with a fatal error no recover can catch. The variable is
// read here, on the recovered path, not at package initialization.
func At(point string) {
	want := os.Getenv("SESSHIN_TEST_PANIC")
	if want == "" || fired {
		return
	}
	fatal, isFatal := strings.CutPrefix(want, "fatal:")
	switch {
	case isFatal && fatal == point:
		fired = true
		overflow()
	case !isFatal && want == point:
		fired = true
		panic("sesshintest: " + point)
	}
}

// overflow ends the process with a stack overflow, a fatal error that
// recover can't catch: the runtime prints its trace and, with the traceback
// level main sets, aborts with SIGABRT.
func overflow() {
	debug.SetMaxStack(1 << 20)
	recurse(0)
}

// recurse never returns. Each frame holds an array that the result depends
// on, so the compiler can neither drop the frame nor turn the call into a
// loop.
func recurse(n int) int {
	var pad [128]int
	pad[n%len(pad)] = n
	return recurse(n+1) + pad[(n+1)%len(pad)]
}
