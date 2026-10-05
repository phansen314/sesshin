package hook

import (
	"time"

	"github.com/phansen314/sesshin/internal/statusline"
	"github.com/phansen314/sesshin/internal/testhook"
)

// runStatusline runs the statusline verb in its own frame (hooks-spec.md,
// Rendering): it prints its line once, from a buffer, and prints the fallback
// line when the buffer wasn't written, whatever panicked, decoding the
// payload included. A write to a closed stdout fails and is ignored.
func runStatusline(env Process, start, began time.Time) {
	printed := false
	defer func() {
		_ = recover()
		if !printed {
			_, _ = env.Stdout.Write([]byte(statusline.FallbackLine))
		}
	}()
	buf := []byte(statusline.FallbackLine)
	if c, ok := prepare(env, "statusline", start, began); ok {
		buf = tick(c)
	}
	printed = true
	_, _ = env.Stdout.Write(buf)
}

// tick records the tick and renders its line (hooks-spec.md, statusline,
// steps 1–6) through statusline.Run, and returns the line to print.
func tick(c *Call) []byte {
	testhook.At("verb:statusline")
	return statusline.Run(statusline.Tick{
		FS:         c.FS,
		SessionDir: c.Loc.SessionDir(c.Payload.SessionID),
		Now:        c.Now,
		Stdin:      c.Stdin,
		Payload:    c.Payload,
		ClaudePID:  c.Getenv("CLAUDE_PID"),
		Log:        c.Log,
	}, time.Local)
}
