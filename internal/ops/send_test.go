package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
	"github.com/phansen314/sesshin/internal/schematest"
)

// A kitty placement whose socket differs from the caller's (the fixture's
// KITTY_LISTEN_ON is unix:/kitty), and whose window is a stale one.
const sendPlacement = `{"terminal":"kitty","socket":"unix:/old","window_id":4}`

type findCall struct {
	Socket string
	PID    int64
}

type sendCall struct {
	Socket string
	Window int64
	Text   string
	Submit bool
}

// sendFixture is spawn's fixture with a fake kitty: the windows each socket
// lists by pid, and what sending is told to do.
type sendFixture struct {
	*spawnFixture
	// windows maps a socket to the window of each pid it lists; a socket
	// missing from it, or a pid missing from its map, answers no.
	windows map[string]map[int64]int64
	finds   []findCall
	sends   []sendCall
	// sendErr is what Send answers.
	sendErr error
}

func newSendFixture(t *testing.T) *sendFixture {
	t.Helper()
	return &sendFixture{
		spawnFixture: newSpawnFixture(t),
		windows:      map[string]map[int64]int64{"unix:/old": {11: 21}, "unix:/kitty": {11: 22}},
	}
}

func (f *sendFixture) sendEnv() SendEnv {
	return SendEnv{
		ReadEnv: f.spawnEnv().ReadEnv,
		FindWindow: func(socket string, pid int64) (int64, error) {
			f.finds = append(f.finds, findCall{socket, pid})
			if w, ok := f.windows[socket][pid]; ok {
				return w, nil
			}
			return 0, errors.New("no window on " + socket)
		},
		Send: func(socket string, window int64, text string, submit bool) error {
			f.sends = append(f.sends, sendCall{socket, window, text, submit})
			return f.sendErr
		},
	}
}

// live is a live session of pid 11 at the end of its turn, in the placement
// (JSON, "" for null) with sesshin ID 1, and the job.
func (f *sendFixture) live(id string, sesshinID int64, job, placement string, mod ...func(*model.LifecycleFile)) {
	f.t.Helper()
	f.running(id, time.Minute, 11, append([]func(*model.LifecycleFile){func(l *model.LifecycleFile) { l.Status = "waiting" }}, mod...)...)
	f.sesshinFile(id, sesshinID, job, placement)
}

func status(s string) func(*model.LifecycleFile) {
	return func(l *model.LifecycleFile) { l.Status = s }
}

func (f *sendFixture) sendRaw(in string) Envelope {
	f.t.Helper()
	si, e := DecodeInput([]byte(in), DecodeSendInput)
	if e != nil {
		env := Failed(e)
		checkEnvelope(f.t, env, "send-output")
		return env
	}
	env := Send(si, f.sendEnv())
	checkEnvelope(f.t, env, "send-output")
	return env
}

func (f *sendFixture) send(session string, members ...string) Envelope {
	f.t.Helper()
	s, _ := json.Marshal(session)
	all := append([]string{`"session":` + string(s)}, members...)
	if !slices.ContainsFunc(members, func(m string) bool { return strings.HasPrefix(m, `"text"`) }) {
		all = append(all, `"text":"hello"`)
	}
	return f.sendRaw("{" + strings.Join(all, ",") + "}")
}

// sent is a send that must have succeeded.
func (f *sendFixture) sent(session string, members ...string) SendOutput {
	f.t.Helper()
	env := f.send(session, members...)
	if !env.OK {
		f.t.Fatalf("send failed: %+v", env.Error)
	}
	return env.Result.(SendOutput)
}

func wantRule(t *testing.T, env Envelope, rule string, session string) {
	t.Helper()
	wantKind(t, env, KindConflict)
	if env.Error.Details["rule"] != rule {
		t.Errorf("details %+v, want rule %s", env.Error.Details, rule)
	}
	if refs := env.Error.Details["sessions"].([]SessionRef); len(refs) != 1 || refs[0].SessionID != session {
		t.Errorf("sessions %+v, want %s", refs, session)
	}
}

func wantReason(t *testing.T, env Envelope, reason string) {
	t.Helper()
	wantKind(t, env, KindTerminal)
	if env.Error.Details["reason"] != reason || env.Error.Details["terminal"] != "kitty" {
		t.Errorf("details %+v, want reason %s", env.Error.Details, reason)
	}
}

// Every input check, at its field; and what the schema also says.
func TestSendInputChecks(t *testing.T) {
	// 3 bytes each: 349525 of them and an "a" make exactly 1 MiB.
	euro := strings.Repeat("€", 349525)
	for _, tc := range []struct {
		in     string
		field  string // "": accepted
		schema bool   // the published schema rejects it too
	}{
		{`{"session":"12","text":"x"}`, "", false},
		{`{"session":"api","text":"a\tb\nc\r\nd","submit":false,"force":true}`, "", false},
		{`{"session":"job:deadbeef","text":"日本語 $(x) 'y' \\    "}`, "", false},
		{`{"session":"12","text":"` + strings.Repeat("a", 1<<20) + `"}`, "", false},
		{`{"session":"12","text":"` + euro + `a"}`, "", false},
		{`{}`, "/session", true},
		{`{"session":"12"}`, "/text", true},
		{`{"session":"012","text":"x"}`, "/session", true},
		{`{"session":12,"text":"x"}`, "/session", true},
		{`{"session":"12","text":1}`, "/text", true},
		{`{"session":"12","text":""}`, "/text", true},
		{`{"session":"12","text":null}`, "/text", true},
		{`{"session":"12","text":"x","submit":"yes"}`, "/submit", true},
		{`{"session":"12","text":"x","force":null}`, "/force", true},
		{`{"session":"12","text":"x","job":"api"}`, "/job", true},
		{`{"session":"12","text":"` + strings.Repeat("a", 1<<20+1) + `"}`, "/text", false},
		{`{"session":"12","text":"` + euro + `aa"}`, "/text", false}, // 1 MiB + 1 byte, in fewer than 1 MiB characters
		{`{"session":"12","text":"` + euro + `€"}`, "/text", false},
		{`{"session":"12","text":"a\u0000b"}`, "/text", false},
		{`{"session":"12","text":"a\u0001b"}`, "/text", false},
		{`{"session":"12","text":"a\u0007b"}`, "/text", false},
		{`{"session":"12","text":"a\u0008b"}`, "/text", false},
		{`{"session":"12","text":"a\u000bb"}`, "/text", false},
		{`{"session":"12","text":"a\u001bb"}`, "/text", false},
		{`{"session":"12","text":"a\u001b[201~b"}`, "/text", false},
		{`{"session":"12","text":"a\u001fb"}`, "/text", false},
		{`{"session":"12","text":"a\u007fb"}`, "/text", false},
		{`{"session":"12","text":"a\u0080b"}`, "/text", false},
		{`{"session":"12","text":"a\u0085b"}`, "/text", false},
		{`{"session":"12","text":"a\u009fb"}`, "/text", false},
	} {
		_, e := DecodeInput([]byte(tc.in), DecodeSendInput)
		in := tc.in
		if len(in) > 100 {
			in = in[:100] + "..."
		}
		switch {
		case tc.field == "" && e != nil:
			t.Errorf("%s: %+v", in, e)
		case tc.field != "":
			if e == nil {
				t.Errorf("%s: accepted", in)
				continue
			}
			checkEnvelope(t, Failed(e), "")
			if ps := e.Details["problems"].([]model.Problem); e.Kind != KindInvalidInput || ps[0].Field != tc.field {
				t.Errorf("%s: %+v", in, e)
			}
		}
		if ok, _ := schematest.Check(t, "send-input", []byte(tc.in)); ok == (tc.field != "" && tc.schema) {
			t.Errorf("%s: the schema says %v", in, ok)
		}
	}
}

// The reason names the character class.
func TestSendInputControlReasons(t *testing.T) {
	for _, tc := range []struct{ text, class string }{
		{"\\u001b", "ESC"},
		{"\\u0000", "C0"},
		{"\\u0003", "C0"},
		{"\\u001f", "C0"},
		{"\\u007f", "DEL"},
		{"\\u0080", "C1"},
		{"\\u009f", "C1"},
	} {
		_, e := DecodeInput([]byte(`{"session":"12","text":"a`+tc.text+`b"}`), DecodeSendInput)
		if e == nil {
			t.Errorf("%s accepted", tc.text)
			continue
		}
		if r := e.Details["problems"].([]model.Problem)[0].Reason; !strings.Contains(r, tc.class) {
			t.Errorf("%s: reason %q does not name %s", tc.text, r, tc.class)
		}
	}
	_, e := DecodeInput([]byte(`{"session":"12","text":"`+strings.Repeat("a", 1<<20+1)+`"}`), DecodeSendInput)
	if r := e.Details["problems"].([]model.Problem)[0].Reason; !strings.Contains(r, "1048576") {
		t.Errorf("size reason %q", r)
	}
	if r := controlIn("tab\there\nand\rthere"); r != "" {
		t.Errorf("tab, line feed, and carriage return refused: %s", r)
	}
}

func TestSendInputDefaults(t *testing.T) {
	in, e := DecodeInput([]byte(`{"session":"api","text":"x"}`), DecodeSendInput)
	if e != nil {
		t.Fatal(e)
	}
	want := SendInput{Selector: Selector{Raw: "api", Job: "api"}, Text: "x", Submit: true}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("%+v, want %+v", in, want)
	}
	in, _ = DecodeInput([]byte(`{"session":"3","text":"x","submit":false,"force":true}`), DecodeSendInput)
	want = SendInput{Selector: Selector{Raw: "3", ID: 3}, Text: "x", Force: true}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("%+v, want %+v", in, want)
	}
}

// A text that is not UTF-8 is invalid-input at /text: the command line can
// supply one, which the JSON reader never lets through.
func TestSendInputNotUTF8(t *testing.T) {
	obj, _, err := jsonio.ParseObject([]byte(`{"session":"12","text":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	obj.Set("text", "a\xffb")
	var p model.Problems
	_, e := CheckInput(obj, &p, DecodeSendInput)
	if e == nil || e.Details["problems"].([]model.Problem)[0].Field != "/text" {
		t.Errorf("%+v", e)
	}
}

// The Errors table's order: invalid-input, environment, not-found, ambiguous,
// conflict (not-live, busy, no-placement), and terminal unreachable.
func TestSendErrorOrder(t *testing.T) {
	f := newSendFixture(t)
	f.endedSession(uuidA, 1, "", "", status("working")) // ended, busy, and no placement
	f.live(uuidB, 2, "web", "", status("working"), func(l *model.LifecycleFile) { l.PID, l.PIDStartedAt = nil, nil })
	home := f.home

	f.home = ""
	wantKind(t, f.sendRaw(`{"session":"Bad Selector","text":"x"}`), KindInvalidInput)
	wantKind(t, f.sendRaw(`{"session":"1","text":"x"}`), KindEnvironment)
	f.home = home
	env := f.send("99")
	wantKind(t, env, KindNotFound)
	if !reflect.DeepEqual(env.Error.Details, map[string]any{"sessions": []string{"99"}, "paths": []string{}}) {
		t.Errorf("details %+v", env.Error.Details)
	}
	wantKind(t, f.send("nope"), KindNotFound)
	wantKind(t, f.send("0b6c5a3e"), KindAmbiguous) // uuidA and uuidB
	wantRule(t, f.send("1"), "not-live", uuidA)
	wantRule(t, f.send(uuidA), "not-live", uuidA)
	wantRule(t, f.send("2"), "mid-turn", uuidB)
	wantRule(t, f.send("web"), "mid-turn", uuidB)
	wantRule(t, f.send("2", `"force":true`), "no-placement", uuidB)

	f.sesshinFile(uuidB, 2, "web", sendPlacement)
	env = f.send("2", `"force":true`)
	wantReason(t, env, "unreachable") // its pid is unknown
	if len(f.finds) != 0 || len(f.sends) != 0 {
		t.Errorf("kitty was asked: %v %v", f.finds, f.sends)
	}

	f.writeLifecycle(f.lifecycle(uuidB, f.ago(time.Minute), notEnded, withPID(11, "s11"), status("working")))
	f.windows = nil
	wantReason(t, f.send("2", `"force":true`), "unreachable") // no window
	if len(f.sends) != 0 {
		t.Errorf("sent: %v", f.sends)
	}
	f.windows = map[string]map[int64]int64{"unix:/old": {11: 5}}
	if _, ok := f.send("2", `"force":true`).Result.(SendOutput); !ok {
		t.Error("not sent")
	}
}

func TestSendSuccess(t *testing.T) {
	f := newSendFixture(t)
	mode := "acceptEdits"
	f.live(uuidA, 1, "api", sendPlacement, func(l *model.LifecycleFile) { l.PermissionMode, l.EventSeq = &mode, 17 })
	out := f.sent("1")
	want := SendOutput{
		Session:        SessionRef{ID: ptrTo(int64(1)), SessionID: uuidA, Name: "#1"},
		Placement:      kitty.PlacementOf("unix:/old", 21),
		Submitted:      true,
		Status:         "waiting",
		PermissionMode: &mode,
		EventSeq:       17,
	}
	if enc(t, out) != enc(t, want) {
		t.Errorf("%s\nwant %s", enc(t, out), enc(t, want))
	}
	// The window is the one found, not the stored 4; sent as given.
	if wantFinds := []findCall{{"unix:/old", 11}}; !reflect.DeepEqual(f.finds, wantFinds) {
		t.Errorf("finds %+v", f.finds)
	}
	if wantSends := []sendCall{{"unix:/old", 21, "hello", true}}; !reflect.DeepEqual(f.sends, wantSends) {
		t.Errorf("sends %+v", f.sends)
	}
	// The wire form.
	if got := enc(t, out); !strings.Contains(got, `"placement":{"terminal":"kitty","socket":"unix:/old","window_id":21}`) ||
		!strings.Contains(got, `"permission_mode":"acceptEdits"`) {
		t.Errorf("%s", got)
	}
}

func TestSendOptions(t *testing.T) {
	f := newSendFixture(t)
	f.live(uuidA, 1, "", sendPlacement)
	// submit=false is one call's worth, and the output says so; the text
	// reaches Send untouched.
	text := "a\tb\n 日本語 $(x)\r\n"
	out := f.sent("1", `"submit":false`, `"text":`+jsonString(text))
	if out.Submitted || len(f.sends) != 1 || f.sends[0].Submit || f.sends[0].Text != text {
		t.Errorf("%+v %+v", out, f.sends)
	}
	// permission_mode null when unknown.
	if out.PermissionMode != nil || !strings.Contains(enc(t, out), `"permission_mode":null`) {
		t.Errorf("%s", enc(t, out))
	}
}

// Only a turn that has ended takes text without force.
func TestSendStatus(t *testing.T) {
	for _, tc := range []struct {
		status string
		force  bool
		sent   bool
	}{
		{"waiting", false, true},
		{"idle", false, true},
		{"working", false, false},
		{"needs_approval", false, false},
		{"unknown", false, false},
		{"starting", false, false},
		{"working", true, true},
		{"needs_approval", true, true},
		{"unknown", true, true},
		{"waiting", true, true},
	} {
		t.Run(fmt.Sprintf("%s force=%v", tc.status, tc.force), func(t *testing.T) {
			f := newSendFixture(t)
			f.live(uuidA, 1, "", sendPlacement, status(tc.status))
			env := f.send("1", fmt.Sprintf(`"force":%v`, tc.force))
			if tc.sent {
				if !env.OK || env.Result.(SendOutput).Status != tc.status || len(f.sends) != 1 {
					t.Errorf("%+v %v", env, f.sends)
				}
				return
			}
			wantRule(t, env, "mid-turn", uuidA)
			if len(f.finds) != 0 || len(f.sends) != 0 {
				t.Errorf("kitty was asked: %v %v", f.finds, f.sends)
			}
		})
	}
}

// A session whose liveness is unknown is sent to, as resume refuses it.
func TestSendUnknownLiveness(t *testing.T) {
	f := newSendFixture(t)
	f.live(uuidA, 1, "", sendPlacement)
	f.tableErr = map[int64]error{11: errors.New("no permission")}
	env := f.send("1")
	if !env.OK {
		t.Fatalf("%+v", env.Error)
	}
	if len(f.sends) != 1 {
		t.Errorf("sends %v", f.sends)
	}
}

func TestSendSelectors(t *testing.T) {
	f := newSendFixture(t)
	f.live(uuidA, 1, "api", sendPlacement)
	// An ended session reporting the job, seen later than the live one's
	// start, and another job's ended one: a job selects among the live.
	f.endedSession(uuidD, 4, "api", "")
	f.endedSession(uuidE, 5, "old", "")
	f.endedSession(uuidC, 3, "", "")
	for _, sel := range []string{"1", "api", "job:api", uuidA, uuidA[:8], strings.ToUpper(uuidA[:8])} {
		f.sends = nil
		out := f.sent(sel)
		if out.Session.SessionID != uuidA || len(f.sends) != 1 {
			t.Errorf("%s: %+v %v", sel, out, f.sends)
		}
	}
	// A job held only by an ended session selects nothing: not-found, not
	// not-live.
	env := f.send("old")
	wantKind(t, env, KindNotFound)
	if !reflect.DeepEqual(env.Error.Details["sessions"], []string{"old"}) {
		t.Errorf("details %+v", env.Error.Details)
	}
	// An ID or UUID names the ended session all the same.
	wantRule(t, f.send("5"), "not-live", uuidE)
	wantRule(t, f.send(uuidD), "not-live", uuidD)
	// An ended session is among the matches of a prefix.
	wantKind(t, f.send("7d1e0000"), KindAmbiguous)
}

func TestSendNoPlacement(t *testing.T) {
	for name, placement := range map[string]string{
		"null":             "",
		"another":          `{"terminal":"wezterm","pane":3}`,
		"no socket":        `{"terminal":"kitty","window_id":4}`,
		"empty socket":     `{"terminal":"kitty","socket":"","window_id":4}`,
		"bad window":       `{"terminal":"kitty","socket":"unix:/old","window_id":0}`,
		"bad tab title":    `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":1}`,
		"no sesshin.json":  "<none>",
		"unusable sesshin": "<unusable>",
	} {
		t.Run(name, func(t *testing.T) {
			f := newSendFixture(t)
			if strings.HasPrefix(placement, "<") {
				f.live(uuidA, 1, "", "")
			} else {
				f.live(uuidA, 1, "", placement)
			}
			switch placement {
			case "<none>":
				f.removeFile(uuidA, "sesshin.json")
			case "<unusable>":
				f.write(uuidA, "sesshin.json", []byte("{"))
			}
			wantRule(t, f.send(uuidA), "no-placement", uuidA)
			if len(f.finds) != 0 || len(f.sends) != 0 {
				t.Errorf("kitty was asked: %v %v", f.finds, f.sends)
			}
		})
	}
}

func (f *sendFixture) removeFile(id, name string) {
	f.t.Helper()
	if err := os.Remove(f.loc.SessionDir(id) + "/" + name); err != nil {
		f.t.Fatal(err)
	}
}

// The pid is lifecycle.json's, else the statusline's; with neither, kitty is
// not asked.
func TestSendPID(t *testing.T) {
	f := newSendFixture(t)
	noPID := func(l *model.LifecycleFile) { l.PID, l.PIDStartedAt = nil, nil }
	f.live(uuidA, 1, "", sendPlacement, noPID)
	wantReason(t, f.send(uuidA), "unreachable")
	if len(f.finds) != 0 {
		t.Errorf("kitty was asked: %v", f.finds)
	}
	f.table[11] = "s11"
	f.writeStatusline(uuidA, f.ago(time.Second), 11, "s11")
	out := f.sent(uuidA)
	if out.Placement == nil || !reflect.DeepEqual(f.finds, []findCall{{"unix:/old", 11}}) {
		t.Errorf("%+v %v", out, f.finds)
	}
}

// The placement's socket is asked first; then the caller's, when it differs;
// a socket that fails, or lists no such window, only moves on.
func TestSendSockets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		windows map[string]map[int64]int64
		caller  string
		finds   []string
		window  int64  // 0: unreachable
		socket  string // the placement's socket in the output
	}{
		{"the stored socket answers", map[string]map[int64]int64{"unix:/old": {11: 21}, "unix:/kitty": {11: 22}}, "unix:/kitty",
			[]string{"unix:/old"}, 21, "unix:/old"},
		{"the stored socket has no such window", map[string]map[int64]int64{"unix:/old": {99: 1}, "unix:/kitty": {11: 22}}, "unix:/kitty",
			[]string{"unix:/old", "unix:/kitty"}, 22, "unix:/kitty"},
		{"the stored socket is gone", map[string]map[int64]int64{"unix:/kitty": {11: 22}}, "unix:/kitty",
			[]string{"unix:/old", "unix:/kitty"}, 22, "unix:/kitty"},
		{"the same socket is asked once", map[string]map[int64]int64{}, "unix:/old",
			[]string{"unix:/old"}, 0, ""},
		{"no caller socket", map[string]map[int64]int64{"unix:/kitty": {11: 22}}, "",
			[]string{"unix:/old"}, 0, ""},
		{"neither", map[string]map[int64]int64{}, "unix:/kitty",
			[]string{"unix:/old", "unix:/kitty"}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSendFixture(t)
			f.live(uuidA, 1, "", sendPlacement)
			f.windows = tc.windows
			f.vars["KITTY_LISTEN_ON"] = tc.caller
			env := f.send("1")
			var asked []string
			for _, c := range f.finds {
				asked = append(asked, c.Socket)
				if c.PID != 11 {
					t.Errorf("pid %d", c.PID)
				}
			}
			if !slices.Equal(asked, tc.finds) {
				t.Errorf("asked %v, want %v", asked, tc.finds)
			}
			if tc.window == 0 {
				wantReason(t, env, "unreachable")
				if len(f.sends) != 0 {
					t.Errorf("sent: %v", f.sends)
				}
				return
			}
			if !env.OK {
				t.Fatalf("%+v", env.Error)
			}
			pl := kitty.PlacementOf(tc.socket, tc.window)
			if got := env.Result.(SendOutput); enc(t, got.Placement) != enc(t, pl) {
				t.Errorf("placement %s, want %s", enc(t, got.Placement), enc(t, pl))
			}
			if want := []sendCall{{tc.socket, tc.window, "hello", true}}; !reflect.DeepEqual(f.sends, want) {
				t.Errorf("sends %+v, want %+v", f.sends, want)
			}
		})
	}
}

// The paste failing is send-failed, Enter failing is submit-failed, and
// neither is retried on the caller's socket.
func TestSendFailures(t *testing.T) {
	f := newSendFixture(t)
	f.live(uuidA, 1, "", sendPlacement)
	f.sendErr = &kitty.SendError{Err: errors.New("the paste broke")}
	env := f.send("1")
	wantReason(t, env, "send-failed")
	if !strings.Contains(env.Error.Message, "the paste broke") || env.Error.Details["detail"] != "the paste broke" {
		t.Errorf("%+v", env.Error)
	}
	f.sendErr = &kitty.SendError{Submit: true, Err: errors.New("enter broke")}
	env = f.send("1")
	wantReason(t, env, "submit-failed")
	f.sendErr = errors.New("a plain error")
	wantReason(t, f.send("1"), "send-failed")
	if len(f.sends) != 3 {
		t.Errorf("sends %v", f.sends)
	}
	for _, s := range f.sends {
		if s.Socket != "unix:/old" {
			t.Errorf("sent on %s after the window was found on unix:/old", s.Socket)
		}
	}
	if len(f.finds) != 3 {
		t.Errorf("finds %v", f.finds)
	}
}

// Nothing is written: not a lock file, not a session file, not a
// reservation.
func TestSendWritesNothing(t *testing.T) {
	f := newSendFixture(t)
	f.live(uuidA, 1, "api", sendPlacement)
	before := f.entries()
	files := map[string][]byte{}
	for _, name := range []string{"lifecycle.json", "sesshin.json"} {
		b, err := os.ReadFile(f.loc.SessionDir(uuidA) + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = b
	}
	f.sent("api")
	if after := f.entries(); !slices.Equal(before, after) {
		t.Errorf("entries %v, was %v", after, before)
	}
	for name, b := range files {
		got, _ := os.ReadFile(f.loc.SessionDir(uuidA) + "/" + name)
		if string(got) != string(b) {
			t.Errorf("%s changed", name)
		}
	}
}

// The unusable files read while selecting are warnings, with a failure too.
func TestSendWarnings(t *testing.T) {
	f := newSendFixture(t)
	f.live(uuidA, 1, "", sendPlacement)
	f.write(uuidC, "lifecycle.json", []byte("{"))
	env := f.send("1")
	if !env.OK || !slices.Equal(warnKinds(env), []string{"unusable-file"}) {
		t.Errorf("%+v", env)
	}
	env = f.send("99")
	wantKind(t, env, KindNotFound)
	if !slices.Equal(warnKinds(env), []string{"unusable-file"}) {
		t.Errorf("warnings on a failure: %+v", env.Warnings)
	}
}
