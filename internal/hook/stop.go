package hook

import (
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// stop is the `stop` verb. An unreadable hook_event_name records as Stop,
// unless the payload has an .error, which only StopFailure sends.
func stop(c *Call) {
	testhook.At("verb:stop")
	kind := record.Stop
	switch c.Payload.HookEventName {
	case "Stop":
	case "StopFailure":
		kind = record.StopFailure
	default:
		if c.Payload.Error != "" {
			kind = record.StopFailure
		}
	}
	_ = record.Record(c.RecordEnv(), record.FromPayload(kind, c.Payload))
}
