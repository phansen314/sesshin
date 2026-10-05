package hook

import (
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// userPrompt is the `user-prompt` verb: a turn began (hooks-spec.md, user-prompt).
func userPrompt(c *Call) {
	testhook.At("verb:user-prompt")
	_ = record.Record(c.RecordEnv(), record.FromPayload(record.UserPromptSubmit, c.Payload))
}
