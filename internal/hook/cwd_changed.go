package hook

import (
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// cwdChanged is the `cwd-changed` verb. It does not go through Record: the
// clocks do not move. An empty new_cwd writes nothing, and takes no lock.
func cwdChanged(c *Call) {
	testhook.At("verb:cwd-changed")
	if c.Payload.NewCwd == "" {
		return
	}
	_ = record.SetCwd(c.RecordEnv(), c.Payload.NewCwd)
}
