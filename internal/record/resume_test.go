package record

import (
	"slices"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Hooks-spec, session-start: the backend is told when the event is a resume,
// for it to keep the sync-only keys of another window; any other source is
// not.
func TestSessionStartTellsTheBackendOfAResume(t *testing.T) {
	for _, tc := range []struct {
		source  string
		resumed bool
	}{{"resume", true}, {"startup", false}, {"clear", false}, {"fork", false}, {"compact", false}, {"", false}} {
		t.Run(tc.source, func(t *testing.T) {
			f := newFix(t)
			var got []bool
			f.env.Placement = func(_ *jsonio.Object, resumed bool) *jsonio.Object {
				got = append(got, resumed)
				return kitty(1)
			}
			f.rec(Event{Kind: SessionStart, Source: tc.source}) // a new file
			f.rec(Event{Kind: SessionStart, Source: tc.source}) // an existing one
			if want := []bool{tc.resumed, tc.resumed}; !slices.Equal(got, want) {
				t.Errorf("resumed %v, want %v", got, want)
			}
		})
	}
}
