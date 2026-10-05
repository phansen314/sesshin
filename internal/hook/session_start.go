package hook

import (
	"github.com/phansen314/sesshin/internal/proc"
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// sessionStart is the `session-start` verb. Claude's process is looked up
// first, before any lock: it reads only the process table, and record, handed
// the result, makes no lookup of its own. record does the rest: the session
// lock, lifecycle.json, sesshin.json, and the placement, which only this event
// replaces.
func sessionStart(c *Call) {
	testhook.At("verb:session-start")
	claude := proc.Find(c.FS, c.Getenv("CLAUDE_PID"))
	ev := record.FromPayload(record.SessionStart, c.Payload)
	ev.Claude = &claude
	_ = record.Record(c.RecordEnv(), ev)
}
