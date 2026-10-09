package hook

import (
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// terminalSync is the `terminal-sync` verb (hooks-spec.md, terminal-sync):
// the detected backend's tab title and the window's user variables, into
// sesshin.json's placement. A backend that isn't a placement.Syncer has
// nothing to sync. The one question to the terminal runs with no lock held;
// the lock is taken after it returns, to write. Everything but a timeout
// fails silently.
func terminalSync(c *Call) {
	testhook.At("verb:terminal-sync")
	b, p := c.Backend()
	s, ok := b.(placement.Syncer)
	if !ok {
		return
	}
	update, err := s.Sync(p)
	testhook.At("sync:returned")
	if err != nil {
		if placement.IsTimeout(err) {
			c.Log(err.Error())
		}
		return
	}
	// A stored placement for another window means the session
	// moved: the next session-start replaces it.
	_ = record.SetPlacementSync(c.RecordEnv(), update)
}
