package hook

import (
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// sessionEnd is the `session-end` verb. It does not adopt: Record finds no
// lifecycle.json and writes nothing. Its lock deadline is Call.Deadline's.
func sessionEnd(c *Call) {
	testhook.At("verb:session-end")
	_ = record.Record(c.RecordEnv(), record.FromPayload(record.SessionEnd, c.Payload))
}
