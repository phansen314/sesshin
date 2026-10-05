package hook

import (
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// compact is the `compact` verb. An unreadable hook_event_name records as
// PreCompact: the clock moves, and the count misses one.
func compact(c *Call) {
	testhook.At("verb:compact")
	kind := record.PreCompact
	if c.Payload.HookEventName == "PostCompact" {
		kind = record.PostCompact
	}
	_ = record.Record(c.RecordEnv(), record.FromPayload(kind, c.Payload))
}
