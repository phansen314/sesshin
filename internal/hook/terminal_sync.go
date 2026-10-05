package hook

import (
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/kitty"
	"github.com/phansen314/sesshin/internal/record"
	"github.com/phansen314/sesshin/internal/testhook"
)

// terminalSync is the `terminal-sync` verb (hooks-spec.md, terminal-sync):
// kitty's tab title and the window's user variables, into sesshin.json's
// placement. The one `kitten @ ls` runs with no lock held; the lock is taken
// after it returns, to write. Everything but a timeout fails silently.
func terminalSync(c *Call) {
	testhook.At("verb:terminal-sync")
	if placement.Multiplexed(c.Getenv) {
		return
	}
	want, ok := kitty.RecognizeParsed(c.Getenv)
	if !ok {
		return
	}
	synced, err := kitty.Sync(want.Socket, want.WindowID)
	testhook.At("sync:returned")
	if err != nil {
		if kitty.IsTimeout(err) {
			c.Log(err.Error())
		}
		return
	}
	// A stored placement for another socket or window means the session
	// moved: the next session-start replaces it.
	_ = record.SetPlacementSync(c.RecordEnv(), func(old *jsonio.Object) (*jsonio.Object, bool) {
		if got, ok := kitty.Parse(old); !ok || got != want {
			return old, false
		}
		return kitty.Apply(old, synced)
	})
}
