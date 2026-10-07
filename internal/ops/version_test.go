package ops

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/phansen314/sesshin/internal/buildinfo"
)

func TestVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		info buildinfo.Info
		want string
	}{
		{
			"checkout",
			buildinfo.Info{Version: "v0.0.0-20261003190000-0a2ed27a1b2c+dirty", Commit: "0a2ed27a1b2c", Modified: true, Go: "go1.26.8"},
			`{"ok":true,"result":{"version":"v0.0.0-20261003190000-0a2ed27a1b2c+dirty","commit":"0a2ed27a1b2c","modified":true,"go":"go1.26.8","formats":{"state":2,"lifecycle":1,"statusline":1,"sesshin":2,"reservation":1,"install":1},"migration":1},"warnings":[]}`,
		},
		{
			"no vcs",
			buildinfo.Info{Version: buildinfo.Devel, Go: "go1.26.8"},
			`{"ok":true,"result":{"version":"(devel)","commit":null,"modified":false,"go":"go1.26.8","formats":{"state":2,"lifecycle":1,"statusline":1,"sesshin":2,"reservation":1,"install":1},"migration":1},"warnings":[]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := Version(tc.info)
			checkEnvelope(t, env, "version-output")
			b, err := json.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got  %s\nwant %s", b, tc.want)
			}
		})
	}
}

func TestFailedDetails(t *testing.T) {
	env := Failed(&Error{Kind: KindInternal, Message: "x"})
	checkEnvelope(t, env, "")
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"ok":false,"error":{"kind":"internal","message":"x","details":{}},"warnings":[]}`; string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func TestDecodeVersionInput(t *testing.T) {
	if _, e := DecodeInput([]byte(`{}`), DecodeVersionInput); e != nil {
		t.Errorf("{}: %+v", e)
	}
	_, e := DecodeInput([]byte(`{"x": 1}`), DecodeVersionInput)
	if e == nil || e.Kind != KindInvalidInput || e.Message != `invalid input at "/x": unknown field` {
		t.Errorf(`{"x":1}: %+v`, e)
	}
}

func TestIOError(t *testing.T) {
	_, err := os.Open("/nonexistent/x")
	e := IOError("/nonexistent/x", err)
	if e.Kind != KindIO || e.Message != "/nonexistent/x: ENOENT" || e.Details["path"] != "/nonexistent/x" || e.Details["code"] != "ENOENT" {
		t.Errorf("%+v", e)
	}
	checkEnvelope(t, Failed(e), "")
	// An error with no errno is a bug, never an invented code.
	if e := IOError("p", errors.New("boom")); e.Kind != KindInternal {
		t.Errorf("%+v", e)
	}
}
