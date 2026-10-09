package ops

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A session directory without a lifecycle.json is warned of once its
// directory is more than 60 seconds old, and not before.
func TestLostSession(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 5, "")
	for _, id := range []string{uuidB, uuidC, uuidD} {
		if err := os.MkdirAll(f.loc.SessionDir(id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	age := func(id string, ago time.Duration) {
		t.Helper()
		when := f.now.Add(-ago)
		if err := os.Chtimes(f.loc.SessionDir(id), when, when); err != nil {
			t.Fatal(err)
		}
	}
	age(uuidB, 5*time.Second) // being created
	age(uuidC, 59*time.Second)
	age(uuidD, 2*time.Minute) // lost

	check := func(what string, ws []Warning) {
		t.Helper()
		if len(ws) != 1 {
			t.Fatalf("%s: %+v", what, ws)
		}
		w := ws[0]
		if w.Kind != KindUnusableFile || w.Details["reason"] != ReasonMissing ||
			w.Details["path"] != filepath.Join(f.loc.SessionDir(uuidD), "lifecycle.json") {
			t.Errorf("%s: %+v", what, w)
		}
	}

	env, l := f.list(`{"liveness":"all"}`)
	if !slices.Equal(l.uuids(), []string{uuidA}) {
		t.Errorf("%v", l.uuids())
	}
	check("list", env.Warnings)

	env = f.show("5", false)
	if !env.OK {
		t.Fatalf("%+v", env.Error)
	}
	check("show", env.Warnings)
}
