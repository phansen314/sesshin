package hook

import (
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// postToolUse is the `post-tool-use` verb. An unreadable hook_event_name
// records as PostToolUse: the two rows differ only in last_event_type.
func postToolUse(c *Call) {
	testhook.At("verb:post-tool-use")
	kind := record.PostToolUse
	if c.Payload.HookEventName == "PostToolUseFailure" {
		kind = record.PostToolUseFailure
	}
	_ = record.Record(c.RecordEnv(), record.FromPayload(kind, c.Payload))
}
