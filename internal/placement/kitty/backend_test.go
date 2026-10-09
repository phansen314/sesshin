package kitty

import (
	"errors"
	"slices"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
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
	if !b.Valid(p) {
		t.Fatalf("%s is not valid", enc(t, p))
	}
	synced := obj(t, `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"api","user_vars":{"p":"1"}}`)
	if !b.Valid(synced) || enc(t, b.Address(synced)) != enc(t, p) {
		t.Errorf("Address %s, Recognize %s", enc(t, b.Address(synced)), enc(t, p))
	}
	if b.Address(nil) != nil {
		t.Error("Address of nil")
	}
	if b.Recognize(env(nil)) != nil {
		t.Error("recognized outside kitty")
	}
	if b.Valid(nil) {
		t.Error("nil is valid")
	}
	if !b.(placement.Launcher).UserVars() {
		t.Error("kitty sets user variables")
	}
}

// Locate asks the stored socket, then the caller's when it differs, and a
// failing socket just moves on.
func TestLocateVia(t *testing.T) {
	stored := PlacementOf("unix:/old", 4)
	for name, tc := range map[string]struct {
		own     string
		answers map[string]int64
		want    string
		asked   []string
		err     string
	}{
		"stored answers":    {"unix:/own", map[string]int64{"unix:/old": 21, "unix:/own": 22}, enc(t, PlacementOf("unix:/old", 21)), []string{"unix:/old"}, ""},
		"caller's after no": {"unix:/own", map[string]int64{"unix:/own": 22}, enc(t, PlacementOf("unix:/own", 22)), []string{"unix:/old", "unix:/own"}, ""},
		"same socket once":  {"unix:/old", nil, "", []string{"unix:/old"}, "unix:/old: none"},
		"no caller socket":  {"", nil, "", []string{"unix:/old"}, "unix:/old: none"},
		"neither answers":   {"unix:/own", nil, "", []string{"unix:/old", "unix:/own"}, "unix:/old: none; unix:/own: none"},
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
			if enc(t, got) != tc.want && tc.want != "" || got != nil && tc.want == "" || !slices.Equal(asked, tc.asked) {
				t.Errorf("got %+v after %v, want %+v after %v", got, asked, tc.want, tc.asked)
			}
		})
	}
}

// Exist asks once per distinct socket; a window is gone only when its socket
// answered without it, and a failing socket or a foreign placement is unknown.
func TestExistVia(t *testing.T) {
	ps := []*jsonio.Object{
		PlacementOf("unix:/a", 1), PlacementOf("unix:/a", 2), PlacementOf("unix:/b", 1),
		PlacementOf("unix:/c", 1), obj(t, `{"terminal":"other"}`), nil,
	}
	asked := map[string]int{}
	got := ExistVia(ps, func(w Parsed) ([]int64, error) {
		asked[w.Socket]++
		switch w.Socket {
		case "unix:/a":
			return []int64{1}, nil
		case "unix:/b":
			return []int64{}, nil
		}
		return nil, errors.New("refused")
	})
	want := []placement.Existence{placement.Present, placement.Gone, placement.Gone, placement.Unknown, placement.Unknown, placement.Unknown}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if asked["unix:/a"] != 1 || asked["unix:/b"] != 1 || asked["unix:/c"] != 1 || len(asked) != 3 {
		t.Errorf("asked %v", asked)
	}
}
