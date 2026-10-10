package ops

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// launchFile writes a launch file last modified ago before the fixture's now.
func (f *pruneFixture) launchFile(name string, ago time.Duration) {
	f.t.Helper()
	if err := os.MkdirAll(f.loc.LaunchesDir(), 0o700); err != nil {
		f.t.Fatal(err)
	}
	p := filepath.Join(f.loc.LaunchesDir(), name)
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	at := f.ago(ago)
	if err := os.Chtimes(p, at, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *pruneFixture) launchNames() []string {
	ents, _ := os.ReadDir(f.loc.LaunchesDir())
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// Launch files more than 120 seconds old are removed and counted, whatever
// they hold; younger ones, and hidden temp files, are kept.
func TestPruneLaunchFiles(t *testing.T) {
	f := newPruneFixture(t)
	if err := os.MkdirAll(f.loc.SessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	f.launchFile("a.json", 121*time.Second)
	f.launchFile("b.json", time.Hour)
	f.launchFile("c.json", 120*time.Second)
	f.launchFile("d.json", time.Second)
	f.launchFile(".sesshin-tmp-x", time.Hour)

	out, _ := f.output(PruneInput{DryRun: true})
	if out.LaunchFilesRemoved != 2 || len(f.launchNames()) != 5 {
		t.Errorf("dry run: %d removed, left %v", out.LaunchFilesRemoved, f.launchNames())
	}
	out, _ = f.output(PruneInput{})
	if want := []string{".sesshin-tmp-x", "c.json", "d.json"}; out.LaunchFilesRemoved != 2 || !slices.Equal(f.launchNames(), want) {
		t.Errorf("%d removed, left %v, want %v", out.LaunchFilesRemoved, f.launchNames(), want)
	}
	out, _ = f.output(PruneInput{})
	if out.LaunchFilesRemoved != 0 {
		t.Errorf("again: %d", out.LaunchFilesRemoved)
	}
}

// No launches/ is none, and an unusable clock removes nothing.
func TestPruneLaunchFilesEdges(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 400*day)
	if out, _ := f.output(PruneInput{}); out.LaunchFilesRemoved != 0 {
		t.Errorf("no directory: %d", out.LaunchFilesRemoved)
	}
	if _, err := os.Stat(f.loc.LaunchesDir()); !os.IsNotExist(err) {
		t.Errorf("prune created launches/: %v", err)
	}

	g := newPruneFixture(t)
	g.session(pidB, -time.Hour) // a session from the future: the clock is unusable
	g.launchFile("old.json", time.Hour)
	if out, _ := g.output(PruneInput{}); out.LaunchFilesRemoved != 0 || !slices.Equal(g.launchNames(), []string{"old.json"}) {
		t.Errorf("unusable clock: %d, %v", out.LaunchFilesRemoved, g.launchNames())
	}
}
