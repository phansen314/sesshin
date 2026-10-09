package kitty

import (
	"errors"
	"slices"
	"testing"

	"github.com/phansen314/sesshin/internal/placement"
)

// The kitty backend recognizes, validates, and places as the functions do.
func TestBackend(t *testing.T) {
	var b placement.Backend = Backend{}
	if b.Tag() != "kitty" {
		t.Errorf("tag %q", b.Tag())
	}
	getenv := env(map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "7"})
	p := b.Recognize(getenv)
	w, ok := b.Valid(p)
	if !ok || w != (placement.Window{Socket: "unix:/x", WindowID: 7}) {
		t.Fatalf("valid %+v, %v", w, ok)
	}
	if enc(t, b.Place(w)) != enc(t, p) {
		t.Errorf("Place %s, Recognize %s", enc(t, b.Place(w)), enc(t, p))
	}
	if b.Recognize(env(nil)) != nil {
		t.Error("recognized outside kitty")
	}
	if _, ok := b.Valid(nil); ok {
		t.Error("nil is valid")
	}
	if !b.(placement.Launcher).UserVars() {
		t.Error("kitty sets user variables")
	}
}

// Locate asks the stored socket, then the caller's when it differs, and a
// failing socket just moves on.
func TestLocateVia(t *testing.T) {
	stored := placement.Window{Socket: "unix:/old", WindowID: 4}
	for name, tc := range map[string]struct {
		own     string
		answers map[string]int64
		want    placement.Window
		asked   []string
		err     string
	}{
		"stored answers":    {"unix:/own", map[string]int64{"unix:/old": 21, "unix:/own": 22}, placement.Window{Socket: "unix:/old", WindowID: 21}, []string{"unix:/old"}, ""},
		"caller's after no": {"unix:/own", map[string]int64{"unix:/own": 22}, placement.Window{Socket: "unix:/own", WindowID: 22}, []string{"unix:/old", "unix:/own"}, ""},
		"same socket once":  {"unix:/old", nil, placement.Window{}, []string{"unix:/old"}, "unix:/old: none"},
		"no caller socket":  {"", nil, placement.Window{}, []string{"unix:/old"}, "unix:/old: none"},
		"neither answers":   {"unix:/own", nil, placement.Window{}, []string{"unix:/old", "unix:/own"}, "unix:/old: none; unix:/own: none"},
	} {
		t.Run(name, func(t *testing.T) {
			var asked []string
			find := func(socket string, pid int64) (int64, error) {
				asked = append(asked, socket)
				if id, ok := tc.answers[socket]; ok && pid == 11 {
					return id, nil
				}
				return 0, errors.New("none")
			}
			got, err := LocateVia(find, env(map[string]string{"KITTY_LISTEN_ON": tc.own}), stored, 11)
			if (err == nil) != (tc.err == "") || err != nil && err.Error() != tc.err {
				t.Errorf("error %v, want %q", err, tc.err)
			}
			if got != tc.want || !slices.Equal(asked, tc.asked) {
				t.Errorf("got %+v after %v, want %+v after %v", got, asked, tc.want, tc.asked)
			}
		})
	}
}
