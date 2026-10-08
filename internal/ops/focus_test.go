package ops

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
	"github.com/phansen314/sesshin/internal/schematest"
)

type focusCall struct {
	Socket string
	Window int64
}

// focusFixture is send's fixture with a fake focus call.
type focusFixture struct {
	*sendFixture
	focuses  []focusCall
	focusErr error
}

func newFocusFixture(t *testing.T) *focusFixture {
	t.Helper()
	return &focusFixture{sendFixture: newSendFixture(t)}
}

func (f *focusFixture) focusEnv() FocusEnv {
	se := f.sendEnv()
	return FocusEnv{
		ReadEnv:    se.ReadEnv,
		FindWindow: se.FindWindow,
		Focus: func(socket string, window int64) error {
			f.focuses = append(f.focuses, focusCall{socket, window})
			return f.focusErr
		},
	}
}

func (f *focusFixture) focusRaw(in string) Envelope {
	f.t.Helper()
	fi, e := DecodeInput([]byte(in), DecodeFocusInput)
	if e != nil {
		env := Failed(e)
		checkEnvelope(f.t, env, "focus-output")
		return env
	}
	env := Focus(fi, f.focusEnv())
	checkEnvelope(f.t, env, "focus-output")
	return env
}

func (f *focusFixture) focus(session string) Envelope {
	f.t.Helper()
	return f.focusRaw(`{"session":` + jsonString(session) + `}`)
}

func (f *focusFixture) focused(session string) FocusOutput {
	f.t.Helper()
	env := f.focus(session)
	if !env.OK {
		f.t.Fatalf("focus failed: %+v", env.Error)
	}
	return env.Result.(FocusOutput)
}

func TestFocusInputChecks(t *testing.T) {
	for _, tc := range []struct {
		in    string
		field string // "": accepted
	}{
		{`{"session":"12"}`, ""},
		{`{"session":"api"}`, ""},
		{`{"session":"job:deadbeef"}`, ""},
		{`{}`, "/session"},
		{`{"session":"012"}`, "/session"},
		{`{"session":"Bad Selector"}`, "/session"},
		{`{"session":12}`, "/session"},
		{`{"session":"12","text":"x"}`, "/text"},
		{`{"session":"12","force":true}`, "/force"},
	} {
		_, e := DecodeInput([]byte(tc.in), DecodeFocusInput)
		if tc.field == "" {
			if e != nil {
				t.Errorf("%s: %+v", tc.in, e)
			}
		} else if e == nil {
			t.Errorf("%s: accepted", tc.in)
		} else if ps := e.Details["problems"].([]model.Problem); e.Kind != KindInvalidInput || ps[0].Field != tc.field {
			t.Errorf("%s: %+v", tc.in, e)
		}
		if ok, _ := schematest.Check(t, "focus-input", []byte(tc.in)); ok != (tc.field == "") {
			t.Errorf("%s: the schema says %v", tc.in, ok)
		}
	}
}

// The Errors table's order: invalid-input, environment, not-found, ambiguous,
// conflict (not-live, no-placement), and terminal focus-failed.
func TestFocusErrorOrder(t *testing.T) {
	f := newFocusFixture(t)
	f.endedSession(uuidA, 1, "", "", status("working")) // ended, and no placement
	f.live(uuidB, 2, "web", "")
	home := f.home

	f.home = ""
	wantKind(t, f.focusRaw(`{"session":"Bad Selector"}`), KindInvalidInput)
	wantKind(t, f.focusRaw(`{"session":"1"}`), KindEnvironment)
	f.home = home
	env := f.focus("99")
	wantKind(t, env, KindNotFound)
	if !reflect.DeepEqual(env.Error.Details, map[string]any{"sessions": []string{"99"}, "paths": []string{}}) {
		t.Errorf("details %+v", env.Error.Details)
	}
	wantKind(t, f.focus("nope"), KindNotFound)
	wantKind(t, f.focus("0b6c5a3e"), KindAmbiguous) // uuidA and uuidB
	wantRule(t, f.focus("1"), "not-live", uuidA)
	wantRule(t, f.focus(uuidA), "not-live", uuidA)
	wantRule(t, f.focus("2"), "no-placement", uuidB)
	wantRule(t, f.focus("web"), "no-placement", uuidB)
	if len(f.finds) != 0 || len(f.focuses) != 0 {
		t.Errorf("kitty was asked: %v %v", f.finds, f.focuses)
	}

	f.sesshinFile(uuidB, 2, "web", sendPlacement)
	f.focusErr = errors.New("kitten @ focus-window: exit status 1")
	env = f.focus("2")
	wantReason(t, env, "focus-failed")
	if !strings.Contains(env.Error.Message, "exit status 1") {
		t.Errorf("%+v", env.Error)
	}
}

func TestFocusVerifiedStoredSocket(t *testing.T) {
	f := newFocusFixture(t)
	f.live(uuidA, 1, "api", sendPlacement)
	out := f.focused("1")
	if !out.Verified || enc(t, out.Placement) != enc(t, kitty.PlacementOf("unix:/old", 21)) || out.Session.SessionID != uuidA {
		t.Errorf("%s", enc(t, out))
	}
	if !reflect.DeepEqual(f.finds, []findCall{{"unix:/old", 11}}) || !reflect.DeepEqual(f.focuses, []focusCall{{"unix:/old", 21}}) {
		t.Errorf("finds %+v focuses %+v", f.finds, f.focuses)
	}
	if got := enc(t, out); !strings.Contains(got, `"verified":true`) || !strings.Contains(got, `"attention":`) {
		t.Errorf("%s", got)
	}
}

func TestFocusVerifiedCallerSocket(t *testing.T) {
	f := newFocusFixture(t)
	f.live(uuidA, 1, "", sendPlacement)
	f.windows = map[string]map[int64]int64{"unix:/kitty": {11: 22}}
	out := f.focused("1")
	if !out.Verified || enc(t, out.Placement) != enc(t, kitty.PlacementOf("unix:/kitty", 22)) {
		t.Errorf("%s", enc(t, out))
	}
	if !reflect.DeepEqual(f.focuses, []focusCall{{"unix:/kitty", 22}}) || len(f.finds) != 2 {
		t.Errorf("finds %+v focuses %+v", f.finds, f.focuses)
	}
}

// Nothing verified: the stored socket and window are focused, verified false.
func TestFocusUnverified(t *testing.T) {
	t.Run("no window", func(t *testing.T) {
		f := newFocusFixture(t)
		f.live(uuidA, 1, "", sendPlacement)
		f.windows = nil
		out := f.focused("1")
		if out.Verified || enc(t, out.Placement) != enc(t, kitty.PlacementOf("unix:/old", 4)) {
			t.Errorf("%s", enc(t, out))
		}
		if !reflect.DeepEqual(f.focuses, []focusCall{{"unix:/old", 4}}) || len(f.finds) != 2 {
			t.Errorf("finds %+v focuses %+v", f.finds, f.focuses)
		}
		if !strings.Contains(enc(t, out), `"verified":false`) {
			t.Errorf("%s", enc(t, out))
		}
	})
	t.Run("unknown pid", func(t *testing.T) {
		f := newFocusFixture(t)
		f.live(uuidA, 1, "", sendPlacement, func(l *model.LifecycleFile) { l.PID, l.PIDStartedAt = nil, nil })
		out := f.focused("1")
		if out.Verified || len(f.finds) != 0 || !reflect.DeepEqual(f.focuses, []focusCall{{"unix:/old", 4}}) {
			t.Errorf("%+v finds %+v focuses %+v", out, f.finds, f.focuses)
		}
	})
	t.Run("focus fails after no window", func(t *testing.T) {
		f := newFocusFixture(t)
		f.live(uuidA, 1, "", sendPlacement)
		f.windows = nil
		f.focusErr = errors.New("no such window")
		env := f.focus("1")
		wantReason(t, env, "focus-failed")
		if !strings.Contains(env.Error.Message, "no such window") || !strings.Contains(env.Error.Message, "pid 11") {
			t.Errorf("%q", env.Error.Message)
		}
	})
}

// Any status will do, and a session whose liveness is unknown is focused.
func TestFocusStatusAndLiveness(t *testing.T) {
	for _, st := range []string{"working", "needs_approval", "waiting", "idle", "unknown", "starting"} {
		f := newFocusFixture(t)
		f.live(uuidA, 1, "", sendPlacement, status(st))
		if !f.focus("1").OK {
			t.Errorf("%s refused", st)
		}
	}
	f := newFocusFixture(t)
	f.live(uuidA, 1, "", sendPlacement)
	f.tableErr = map[int64]error{11: errors.New("no permission")}
	if !f.focus("1").OK || len(f.focuses) != 1 {
		t.Errorf("focuses %v", f.focuses)
	}
}

func TestFocusSelectors(t *testing.T) {
	f := newFocusFixture(t)
	f.live(uuidA, 1, "api", sendPlacement)
	f.endedSession(uuidD, 4, "api", "")
	f.endedSession(uuidE, 5, "old", "")
	for _, sel := range []string{"1", "api", "job:api", uuidA, uuidA[:8]} {
		f.focuses = nil
		if out := f.focused(sel); out.Session.SessionID != uuidA || len(f.focuses) != 1 {
			t.Errorf("%s: %+v %v", sel, out, f.focuses)
		}
	}
	wantKind(t, f.focus("old"), KindNotFound)
	wantRule(t, f.focus("5"), "not-live", uuidE)
}

func TestFocusNoPlacement(t *testing.T) {
	for name, placement := range map[string]string{
		"null":         "",
		"another":      `{"terminal":"wezterm","pane":3}`,
		"no socket":    `{"terminal":"kitty","window_id":4}`,
		"empty socket": `{"terminal":"kitty","socket":"","window_id":4}`,
		"bad window":   `{"terminal":"kitty","socket":"unix:/old","window_id":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFocusFixture(t)
			f.live(uuidA, 1, "", placement)
			wantRule(t, f.focus(uuidA), "no-placement", uuidA)
			if len(f.finds) != 0 || len(f.focuses) != 0 {
				t.Errorf("kitty was asked: %v %v", f.finds, f.focuses)
			}
		})
	}
}

func TestFocusWritesNothingAndWarns(t *testing.T) {
	f := newFocusFixture(t)
	f.live(uuidA, 1, "api", sendPlacement)
	f.write(uuidC, "lifecycle.json", []byte("{"))
	before := f.entries()
	env := f.focus("api")
	if !env.OK || !slices.Equal(warnKinds(env), []string{"unusable-file"}) {
		t.Errorf("%+v", env)
	}
	if after := f.entries(); !slices.Equal(before, after) {
		t.Errorf("entries %v, was %v", after, before)
	}
	env = f.focus("99")
	wantKind(t, env, KindNotFound)
	if !slices.Equal(warnKinds(env), []string{"unusable-file"}) {
		t.Errorf("warnings on a failure: %+v", env.Warnings)
	}
}
