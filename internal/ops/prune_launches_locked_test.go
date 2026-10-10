package ops

import (
	"slices"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

// Launch files take no lock: with the state lock held, and the reservations
// left for the next run, the old ones are still removed and counted.
func TestPruneLaunchFilesWithReservationsLocked(t *testing.T) {
	f := newPruneFixture(t).sweeping()
	f.session(pidA, 40*day)
	f.reserve("old", 2*day, "")
	f.launchFile("old.json", time.Hour)
	f.launchFile("new.json", time.Second)
	root, err := fsys.OS{}.OpenRoot(f.loc.SessionsDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	out, _ := f.output(PruneInput{})
	if !out.ReservationsLocked || out.LaunchFilesRemoved != 1 || !slices.Equal(f.launchNames(), []string{"new.json"}) {
		t.Errorf("%+v, left %v", out, f.launchNames())
	}
}
