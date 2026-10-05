package hook

import (
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// notification is the `notification` verb. Any type but the three that have a
// row in the effects table returns before taking a lock or creating anything,
// idle_prompt included, and is not logged.
func notification(c *Call) {
	testhook.At("verb:notification")
	var kind record.Kind
	switch c.Payload.NotificationType {
	case "permission_prompt":
		kind = record.PermissionPrompt
	case "elicitation_dialog":
		kind = record.ElicitationDialog
	case "elicitation_complete":
		kind = record.ElicitationComplete
	default:
		return
	}
	_ = record.Record(c.RecordEnv(), record.FromPayload(kind, c.Payload))
}
