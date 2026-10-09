package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
)

// UUIDs for list and show, apart from prune's, with prefixes to share.
const (
	uuidA = "0b6c5a3e-1f7e-4c2b-9a51-6d2f0e8b7c11"
	uuidB = "0b6c5a3e-1f7e-4c2b-9a51-6d2f0e8b7c22"
	uuidC = "7d1e0000-0000-4000-8000-000000000003"
	uuidD = "7d1e0000-0000-4000-8000-000000000004"
	uuidE = "a0000000-0000-4000-8000-000000000005"
)

// sesshin writes a usable sesshin.json; id 0 is a null ID.
func (f *pruneFixture) sesshin(session string, id int64, placement string) {
	f.t.Helper()
	idJSON := "null"
	if id != 0 {
		idJSON = fmt.Sprint(id)
	}
	if placement == "" {
		placement = "null"
	}
	b := fmt.Sprintf(`{"schema": 2,"id":%s,"job":null,"source":"hook","placement":%s,"extra":{}}`, idJSON, placement)
	if _, r := model.ReadSesshin([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture sesshin.json unusable: %s", r.Reason())
	}
	f.write(session, "sesshin.json", []byte(b))
}

// tick writes a usable statusline.json with the payload.
func (f *pruneFixture) tick(session string, receivedAt time.Time, payload string, branch string, burn string, pid int64) {
	f.t.Helper()
	pidJSON, startedJSON := "null", "null"
	if pid != 0 {
		pidJSON, startedJSON = fmt.Sprint(pid), fmt.Sprintf(`"s%d"`, pid)
	}
	if branch == "" {
		branch = "null"
	} else {
		branch = `"` + branch + `"`
	}
	if burn == "" {
		burn = "null"
	}
	b := fmt.Sprintf(`{"schema":1,"received_at":%q,"received_ns":1,"payload":%s,"git_branch":%s,"cost_sample":null,"burn_usd_per_hour":%s,"pid":%s,"pid_started_at":%s}`,
		model.FormatTimestamp(receivedAt), payload, branch, burn, pidJSON, startedJSON)
	if _, r := model.ReadStatusline([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture statusline.json unusable: %s", r.Reason())
	}
	f.write(session, "statusline.json", []byte(b))
}

// running is a live session: its process is in the fake table.
func (f *pruneFixture) running(id string, ago time.Duration, pid int64, mod ...func(*model.LifecycleFile)) string {
	f.t.Helper()
	f.table[pid] = fmt.Sprintf("s%d", pid)
	return f.session(id, ago, append([]func(*model.LifecycleFile){notEnded, withPID(pid, fmt.Sprintf("s%d", pid))}, mod...)...)
}

// listed is a list result, with the sessions as generic JSON.
type listed struct {
	Sessions  []map[string]any
	Raw       []json.RawMessage
	Total     int
	Truncated bool
}

func (f *pruneFixture) list(in string) (Envelope, listed) {
	f.t.Helper()
	li, e := DecodeInput([]byte(in), DecodeListInput)
	if e != nil {
		f.t.Fatalf("%s: %+v", in, e)
	}
	env := List(li, f.env())
	checkEnvelope(f.t, env, "list-output")
	if !env.OK {
		f.t.Fatalf("list failed: %+v", env.Error)
	}
	b, err := json.Marshal(env.Result)
	if err != nil {
		f.t.Fatal(err)
	}
	var l listed
	var raw struct {
		Sessions  []json.RawMessage `json:"sessions"`
		Total     int               `json:"total"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		f.t.Fatal(err)
	}
	l.Raw, l.Total, l.Truncated = raw.Sessions, raw.Total, raw.Truncated
	for _, r := range raw.Sessions {
		var m map[string]any
		if err := json.Unmarshal(r, &m); err != nil {
			f.t.Fatal(err)
		}
		l.Sessions = append(l.Sessions, m)
	}
	return env, l
}

func (l listed) uuids() []string {
	out := []string{}
	for _, s := range l.Sessions {
		out = append(out, s["session_id"].(string))
	}
	return out
}

// keys are an object's member names, in order.
func keys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var out []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// show runs show on a selector, through the input decoder, and validates the
// envelope.
func (f *pruneFixture) show(selector string, payload bool) Envelope {
	f.t.Helper()
	b, _ := json.Marshal(map[string]any{"session": selector, "include_payload": payload})
	return f.showInput(string(b))
}

func (f *pruneFixture) showInput(in string) Envelope {
	f.t.Helper()
	si, e := DecodeInput([]byte(in), DecodeShowInput)
	var env Envelope
	if e != nil {
		env = Failed(e)
	} else {
		env = Show(si, f.env())
	}
	checkEnvelope(f.t, env, "show-output")
	return env
}

// shown is the session view of a show that must have succeeded.
func (f *pruneFixture) shown(selector string) map[string]any {
	f.t.Helper()
	env := f.show(selector, false)
	if !env.OK {
		f.t.Fatalf("show %s failed: %+v", selector, env.Error)
	}
	return asMap(f.t, env.Result.(ShowOutput).Session)
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func wantKind(t *testing.T, env Envelope, kind string) {
	t.Helper()
	if env.OK || env.Error.Kind != kind {
		t.Fatalf("got %+v, want error %s", env, kind)
	}
}

func warnKinds(env Envelope) []string {
	out := []string{}
	for _, w := range env.Warnings {
		out = append(out, w.Kind)
	}
	return out
}

func TestListInputChecks(t *testing.T) {
	for _, tc := range []struct {
		in    string
		field string // "": accepted
	}{
		{`{}`, ""},
		{`{"liveness":"live","include_headless":true,"fields":["name","status"],"limit":0}`, ""},
		{`{"fields":[]}`, ""},
		{`{"limit":9007199254740991}`, ""},
		{`{"liveness":"dead"}`, "/liveness"},
		{`{"liveness":"ALL"}`, "/liveness"},
		{`{"liveness":1}`, "/liveness"},
		{`{"include_headless":"yes"}`, "/include_headless"},
		{`{"fields":"name"}`, "/fields"},
		{`{"fields":[1]}`, "/fields/0"},
		{`{"fields":["nope"]}`, "/fields/0"},
		{`{"fields":["Name"]}`, "/fields/0"},
		{`{"fields":["name","name"]}`, "/fields/1"},
		{`{"limit":-1}`, "/limit"},
		{`{"limit":1.5}`, "/limit"},
		{`{"limit":9007199254740992}`, "/limit"},
		{`{"limit":"3"}`, "/limit"},
		{`{"other":1}`, "/other"},
	} {
		_, e := DecodeInput([]byte(tc.in), DecodeListInput)
		switch {
		case tc.field == "" && e != nil:
			t.Errorf("%s: %+v", tc.in, e)
		case tc.field != "":
			if e == nil {
				t.Errorf("%s: accepted", tc.in)
				continue
			}
			checkEnvelope(t, Failed(e), "")
			if ps := e.Details["problems"].([]model.Problem); e.Kind != KindInvalidInput || ps[0].Field != tc.field {
				t.Errorf("%s: %+v", tc.in, e)
			}
		}
	}
	// Every view field may be named.
	for _, name := range viewFields {
		if _, e := DecodeInput([]byte(`{"fields":["`+name+`"]}`), DecodeListInput); e != nil {
			t.Errorf("%s: %+v", name, e)
		}
	}
	if len(viewFields) != 34 || viewFields[0] != "id" || viewFields[len(viewFields)-1] != "transcript_exists" {
		t.Errorf("view fields %v", viewFields)
	}
}

func TestShowInputChecks(t *testing.T) {
	for _, tc := range []struct {
		in    string
		field string // "": accepted
	}{
		{`{"session":"12"}`, ""},
		{`{"session":"9007199254740991"}`, ""},
		{`{"session":"0b6c5a3e"}`, ""},
		{`{"session":"0B6C5A3E-1F7E"}`, ""},
		{`{"session":"` + uuidA + `"}`, ""},
		{`{"session":"12","include_payload":true}`, ""},
		{`{"session":"api"}`, ""},
		{`{"session":"job:api"}`, ""},
		{`{"session":"job:deadbeef"}`, ""},
		{`{"session":"0b6c5a3"}`, ""},
		{`{"session":"0b6c5a3g"}`, ""},
		{`{"session":"` + strings.Repeat("a", 64) + `"}`, ""},
		{`{}`, "/session"},
		{`{"session":12}`, "/session"},
		{`{"session":""}`, "/session"},
		{`{"session":"012"}`, "/session"},
		{`{"session":"0"}`, "/session"},
		{`{"session":"9007199254740992"}`, "/session"},
		{`{"session":"99999999999999999999999"}`, "/session"},
		{`{"session":"#12"}`, "/session"},
		{`{"session":"api-"}`, "/session"},
		{`{"session":"-api"}`, "/session"},
		{`{"session":"job:"}`, "/session"},
		{`{"session":"job:12"}`, "/session"},
		{`{"session":"job:Api_"}`, "/session"},
		{`{"session":"job:job:api"}`, "/session"},
		{`{"session":"api review"}`, "/session"},
		{`{"session":"` + strings.Repeat("a", 65) + `"}`, "/session"},
		{`{"session":"Job:api"}`, "/session"},
		{`{"session":"12","include_payload":"yes"}`, "/include_payload"},
	} {
		_, e := DecodeInput([]byte(tc.in), DecodeShowInput)
		switch {
		case tc.field == "" && e != nil:
			t.Errorf("%s: %+v", tc.in, e)
		case tc.field != "":
			if e == nil {
				t.Errorf("%s: accepted", tc.in)
				continue
			}
			checkEnvelope(t, Failed(e), "")
			if ps := e.Details["problems"].([]model.Problem); e.Kind != KindInvalidInput || ps[0].Field != tc.field {
				t.Errorf("%s: %+v", tc.in, e)
			}
		}
	}
}

func TestListErrors(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		f := newPruneFixture(t)
		f.home = ""
		li, _ := DecodeInput([]byte(`{}`), DecodeListInput)
		env := List(li, f.env())
		checkEnvelope(t, env, "list-output")
		wantKind(t, env, "environment")
		if env.Error.Details["variable"] != "HOME" {
			t.Errorf("%+v", env)
		}
		env = f.show("12", false)
		wantKind(t, env, "environment")
	})
	t.Run("a corrupt config is not read", func(t *testing.T) {
		f := newPruneFixture(t)
		f.config("retain_days = -1")
		f.session(uuidA, time.Hour)
		li, _ := DecodeInput([]byte(`{}`), DecodeListInput)
		env := List(li, f.env())
		checkEnvelope(t, env, "list-output")
		if !env.OK {
			t.Errorf("%+v", env)
		}
		wantKind(t, f.show("99", false), "not-found")
	})
	t.Run("io on sessions", func(t *testing.T) {
		f := newPruneFixture(t)
		if err := os.MkdirAll(f.loc.StateDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.loc.SessionsDir(), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		li, _ := DecodeInput([]byte(`{}`), DecodeListInput)
		env := List(li, f.env())
		checkEnvelope(t, env, "list-output")
		wantKind(t, env, KindIO)
		if env.Error.Details["code"] != "ENOTDIR" {
			t.Errorf("%+v", env)
		}
	})
	t.Run("invalid input comes first", func(t *testing.T) {
		f := newPruneFixture(t)
		f.home = ""
		env := f.showInput(`{"session":"012"}`)
		wantKind(t, env, KindInvalidInput)
	})
}

func TestListMissingDirectories(t *testing.T) {
	f := newPruneFixture(t) // no state directory at all
	env, l := f.list(`{}`)
	if l.Total != 0 || l.Truncated || len(l.Sessions) != 0 || len(env.Warnings) != 0 {
		t.Errorf("%+v %+v", env, l)
	}
	if b, _ := json.Marshal(env.Result); string(b) != `{"sessions":[],"total":0,"truncated":false}` {
		t.Errorf("%s", b)
	}
	if err := os.MkdirAll(f.loc.StateDir, 0o700); err != nil { // state, but no sessions/
		t.Fatal(err)
	}
	if _, l := f.list(`{"liveness":"all"}`); l.Total != 0 {
		t.Errorf("%+v", l)
	}
	wantKind(t, f.show("1", false), KindNotFound)
	if _, err := os.Stat(f.loc.SessionsDir()); err == nil {
		t.Error("list created sessions/")
	}
}

func TestListIgnoresEntries(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 10)
	for _, name := range []string{".hidden", "not-a-uuid", strings.ToUpper(uuidB)} {
		if err := os.MkdirAll(filepath.Join(f.loc.SessionsDir(), name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.loc.SessionsDir(), uuidC), nil, 0o600); err != nil { // a file
		t.Fatal(err)
	}
	env, l := f.list(`{"liveness":"all"}`)
	if !slices.Equal(l.uuids(), []string{uuidA}) || len(env.Warnings) != 0 {
		t.Errorf("%v %+v", l.uuids(), env.Warnings)
	}
}

func TestListOrder(t *testing.T) {
	f := newPruneFixture(t)
	// Live: A and B seen alike, with IDs 2 and 1; C seen alike with no ID;
	// D seen alike with no ID and a later name; E seen earlier, ID 9. Then an
	// unknown (its process table fails) seen between, and two ended.
	f.running(uuidA, 5*time.Minute, 11)
	f.sesshin(uuidA, 2, "")
	f.running(uuidB, 5*time.Minute, 12)
	f.sesshin(uuidB, 1, "")
	f.running(uuidC, 5*time.Minute, 13)
	f.running(uuidD, 5*time.Minute, 14)
	f.running(uuidE, 10*time.Minute, 15)
	f.sesshin(uuidE, 9, "")
	unk := "00000000-0000-4000-8000-0000000000f1"
	f.session(unk, 7*time.Minute, notEnded, withPID(16, "s16"))
	f.tableErr[16] = syscall.EIO
	endNew := "00000000-0000-4000-8000-0000000000f2"
	endOld := "00000000-0000-4000-8000-0000000000f3"
	f.session(endNew, time.Minute)
	f.session(endOld, time.Hour)

	_, l := f.list(`{"liveness":"all"}`)
	want := []string{uuidB, uuidA, uuidC, uuidD, unk, uuidE, endNew, endOld}
	if !slices.Equal(l.uuids(), want) {
		t.Errorf("order %v\nwant  %v", l.uuids(), want)
	}
	var states []string
	for _, s := range l.Sessions {
		states = append(states, s["liveness"].(string))
	}
	if !slices.Equal(states, []string{"live", "live", "live", "live", "unknown", "live", "ended", "ended"}) {
		t.Errorf("liveness %v", states)
	}
	// The default is live, unknown included.
	_, l = f.list(`{}`)
	if !slices.Equal(l.uuids(), want[:6]) || l.Total != 6 {
		t.Errorf("live: %v", l.uuids())
	}
	_, l = f.list(`{"liveness":"ended"}`)
	if !slices.Equal(l.uuids(), want[6:]) || l.Total != 2 {
		t.Errorf("ended: %v", l.uuids())
	}
	// A statusline tick makes it seen later.
	f.tick(uuidE, f.ago(30*time.Second), `{}`, "", "", 0)
	_, l = f.list(`{}`)
	if l.uuids()[0] != uuidE {
		t.Errorf("after a tick: %v", l.uuids())
	}
}

func TestListNarrowing(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.running(uuidB, 2*time.Minute, 12, nested)
	f.session(uuidC, 3*time.Minute)
	f.session(uuidD, 4*time.Minute, sdk)
	f.sesshin(uuidA, 7, "")
	f.write(uuidE, "lifecycle.json", []byte(`{`)) // an unusable one, for its warning

	for _, tc := range []struct {
		in    string
		want  []string
		total int
		trunc bool
	}{
		{`{}`, []string{uuidA}, 1, false},
		{`{"include_headless":true}`, []string{uuidA, uuidB}, 2, false},
		{`{"liveness":"ended"}`, []string{uuidC}, 1, false},
		{`{"liveness":"ended","include_headless":true}`, []string{uuidC, uuidD}, 2, false},
		{`{"liveness":"all"}`, []string{uuidA, uuidC}, 2, false},
		{`{"liveness":"all","include_headless":true}`, []string{uuidA, uuidB, uuidC, uuidD}, 4, false},
		{`{"liveness":"all","include_headless":true,"limit":3}`, []string{uuidA, uuidB, uuidC}, 4, true},
		{`{"liveness":"all","include_headless":true,"limit":4}`, []string{uuidA, uuidB, uuidC, uuidD}, 4, false},
		{`{"liveness":"all","include_headless":true,"limit":100}`, []string{uuidA, uuidB, uuidC, uuidD}, 4, false},
		{`{"liveness":"all","include_headless":true,"limit":0}`, []string{}, 4, true},
		{`{"limit":0}`, []string{}, 1, true},
	} {
		env, l := f.list(tc.in)
		if got := l.uuids(); !slices.Equal(got, tc.want) || l.Total != tc.total || l.Truncated != tc.trunc {
			t.Errorf("%s: %v total %d truncated %v", tc.in, got, l.Total, l.Truncated)
		}
		// The unusable lifecycle.json is warned of, whatever was left out.
		if len(env.Warnings) != 1 || env.Warnings[0].Kind != KindUnusableFile {
			t.Errorf("%s: warnings %+v", tc.in, env.Warnings)
		}
	}
}

func TestListFields(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 7, "")
	f.tick(uuidA, f.ago(time.Second), `{"cost":{"total_cost_usd":1.25}}`, "main", "", 0)

	_, l := f.list(`{"fields":["status","name"]}`)
	if got := keys(t, l.Raw[0]); !slices.Equal(got, []string{"id", "session_id", "name", "status"}) {
		t.Errorf("keys %v", got) // the view's order, not the request's
	}
	if l.Sessions[0]["id"] != float64(7) || l.Sessions[0]["status"] != "idle" {
		t.Errorf("%+v", l.Sessions[0])
	}
	_, l = f.list(`{"fields":[]}`)
	if got := keys(t, l.Raw[0]); !slices.Equal(got, []string{"id", "session_id"}) {
		t.Errorf("empty list: %v", got)
	}
	_, l = f.list(`{"fields":["metrics","placement","prompt_cache","pending","model"]}`)
	if got := keys(t, l.Raw[0]); !slices.Equal(got, []string{"id", "session_id", "pending", "model", "metrics", "prompt_cache", "placement"}) {
		t.Errorf("keys %v", got)
	}
	m := l.Sessions[0]["metrics"].(map[string]any)
	if m["cost_usd"] != 1.25 || l.Sessions[0]["pending"] != nil || l.Sessions[0]["placement"] != nil {
		t.Errorf("%+v", l.Sessions[0])
	}
	// A projection is the full view's members, as they are.
	_, full := f.list(`{}`)
	_, l = f.list(`{"fields":["metrics","placement","name","pending","liveness"]}`)
	for k, v := range l.Sessions[0] {
		if fmt.Sprint(v) != fmt.Sprint(full.Sessions[0][k]) {
			t.Errorf("%s: %v, want %v", k, v, full.Sessions[0][k])
		}
	}
	// No fields: every one, in the view's order.
	_, l = f.list(`{}`)
	if got := keys(t, l.Raw[0]); !slices.Equal(got, viewFields) {
		t.Errorf("keys %v", got)
	}
	// A projected null stays null, a missing sesshin ID included.
	f.session(uuidB, 2*time.Minute, notEnded, withPID(12, "s12"))
	f.table[12] = "s12"
	_, l = f.list(`{"fields":["name"]}`)
	if l.Sessions[1]["id"] != nil || l.Sessions[1]["name"] != uuidB[:8] {
		t.Errorf("%+v", l.Sessions[1])
	}
}

func TestListWarnings(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.write(uuidA, "sesshin.json", []byte(`{"schema": 2,"id":0,"job":null,"source":"hook","placement":null,"extra":{}}`)) // id below 1
	f.running(uuidB, 2*time.Minute, 12)
	f.write(uuidB, "statusline.json", []byte(`{"schema":2}`))
	f.write(uuidC, "lifecycle.json", []byte(`{"schema":1`)) // unusable: left out
	f.write(uuidD, "sesshin.json", []byte(`{"schema": 2,"id":3,"job":null,"source":"hook","placement":null,"extra":{}}`))
	// A directory in the place of a file.
	if err := os.MkdirAll(filepath.Join(f.loc.SessionDir(uuidE), "lifecycle.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	env, l := f.list(`{"liveness":"all"}`)
	if !slices.Equal(l.uuids(), []string{uuidA, uuidB}) {
		t.Errorf("%v", l.uuids())
	}
	var paths []string
	for _, w := range env.Warnings {
		if w.Kind != KindUnusableFile {
			t.Errorf("%+v", w)
		}
		paths = append(paths, w.Details["path"].(string))
	}
	dir := f.loc.SessionDir
	want := []string{
		filepath.Join(dir(uuidA), "sesshin.json"),
		filepath.Join(dir(uuidB), "statusline.json"),
		filepath.Join(dir(uuidC), "lifecycle.json"),
		filepath.Join(dir(uuidE), "lifecycle.json"),
	}
	if !slices.Equal(paths, want) {
		t.Errorf("paths %v\nwant  %v", paths, want)
	}
	if l.Sessions[0]["id"] != nil || l.Sessions[0]["placement"] != nil || l.Sessions[1]["metrics"] != nil {
		t.Errorf("%+v", l.Sessions)
	}
	// A hidden session's files are warned of as well.
	f.running(uuidD, time.Minute, 13, nested)
	f.write(uuidD, "statusline.json", []byte(`x`))
	env, l = f.list(`{"limit":0}`)
	if len(l.Sessions) != 0 || len(env.Warnings) != 5 {
		t.Errorf("%d warnings: %+v", len(env.Warnings), env.Warnings)
	}
	// A directory that vanishes, and a missing lifecycle.json, raise nothing.
	f2 := newPruneFixture(t)
	f2.write(uuidA, "statusline.json", []byte(`{"schema":1}`)) // no lifecycle.json: the file is bad, the session skipped
	f2.write(uuidB, "sesshin.json", []byte(`{"schema": 2,"id":1,"job":null,"source":"hook","placement":null,"extra":{}}`))
	env, l = f2.list(`{"liveness":"all"}`)
	if len(l.Sessions) != 0 || len(env.Warnings) != 1 || env.Warnings[0].Details["path"] != filepath.Join(f2.loc.SessionDir(uuidA), "statusline.json") {
		t.Errorf("%+v", env.Warnings)
	}
}

func TestListDuplicateIDs(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 5, "")
	f.running(uuidB, 2*time.Minute, 12)
	f.sesshin(uuidB, 5, "")
	f.running(uuidC, 3*time.Minute, 13)
	f.sesshin(uuidC, 2, "")
	f.running(uuidD, 4*time.Minute, 14)
	f.sesshin(uuidD, 2, "")
	f.running(uuidE, 5*time.Minute, 15)
	f.sesshin(uuidE, 9, "")
	// An ended one sharing 9 isn't listed by default, so it isn't one of them.
	ended := "00000000-0000-4000-8000-0000000000f2"
	f.session(ended, time.Hour)
	f.sesshin(ended, 9, "")

	env, l := f.list(`{"fields":["name"]}`)
	if len(l.Sessions) != 5 {
		t.Fatalf("%v", l.uuids())
	}
	if len(env.Warnings) != 2 {
		t.Fatalf("%+v", env.Warnings)
	}
	for i, w := range env.Warnings {
		if w.Kind != KindDuplicateID {
			t.Errorf("%+v", w)
		}
		wantID, wantSessions := []int64{2, 5}[i], [][]string{{uuidC, uuidD}, {uuidA, uuidB}}[i]
		if w.Details["id"] != wantID {
			t.Errorf("warning %d: %+v", i, w.Details)
		}
		var got []string
		for _, r := range w.Details["sessions"].([]SessionRef) {
			got = append(got, r.SessionID)
			if r.ID == nil || *r.ID != wantID || r.Name != fmt.Sprintf("#%d", wantID) {
				t.Errorf("ref %+v", r)
			}
		}
		if !slices.Equal(got, wantSessions) {
			t.Errorf("warning %d: sessions %v, want %v", i, got, wantSessions)
		}
	}
	// With all sessions listed, the ended one joins 9; a limit changes nothing.
	env, _ = f.list(`{"liveness":"all","limit":1}`)
	if len(env.Warnings) != 3 {
		t.Errorf("%+v", env.Warnings)
	}
}

func TestListSupersededAndEnded(t *testing.T) {
	f := newPruneFixture(t)
	// Two sessions of one process: the later start ranks above, and the
	// other, whose SessionEnd never came, is superseded.
	f.table[50] = "s50"
	f.session(uuidA, 10*time.Minute, notEnded, withPID(50, "s50"))
	f.session(uuidB, time.Minute, notEnded, withPID(50, "s50"))
	reason := "logout"
	f.session(uuidC, 2*time.Hour, func(l *model.LifecycleFile) { l.EndReason = &reason })
	gone := "00000000-0000-4000-8000-0000000000f2"
	f.session(gone, 3*time.Hour, notEnded, withPID(51, "s51")) // its process is gone

	_, l := f.list(`{"liveness":"all"}`)
	byID := map[string]map[string]any{}
	for _, s := range l.Sessions {
		byID[s["session_id"].(string)] = s
	}
	a, b, c, g := byID[uuidA], byID[uuidB], byID[uuidC], byID[gone]
	if b["liveness"] != "live" || b["end_reason"] != nil || b["ended_at"] != nil {
		t.Errorf("b: %+v", b)
	}
	if a["liveness"] != "ended" || a["end_reason"] != "superseded" || a["ended_at"] != nil {
		t.Errorf("a: %+v", a)
	}
	if c["liveness"] != "ended" || c["end_reason"] != "logout" || c["ended_at"] == nil {
		t.Errorf("c: %+v", c)
	}
	if g["liveness"] != "ended" || g["end_reason"] != nil || g["ended_at"] != nil {
		t.Errorf("gone: %+v", g)
	}
}

func TestViewDerivations(t *testing.T) {
	t.Run("name", func(t *testing.T) {
		f := newPruneFixture(t)
		title := "My title"
		f.running(uuidA, time.Minute, 11, func(l *model.LifecycleFile) { l.SessionTitle = &title })
		f.sesshin(uuidA, 3, "")
		f.tick(uuidA, f.ago(time.Second), `{"session_name":"from the line"}`, "", "", 0)
		f.running(uuidB, time.Minute, 12)
		f.sesshin(uuidB, 4, "")
		f.tick(uuidB, f.ago(time.Second), `{"session_name":"from the line"}`, "", "", 0)
		f.running(uuidC, time.Minute, 13)
		f.sesshin(uuidC, 5, "")
		f.tick(uuidC, f.ago(time.Second), `{"session_name":5}`, "", "", 0)
		f.running(uuidD, time.Minute, 14)
		empty := ""
		f.running(uuidE, time.Minute, 15, func(l *model.LifecycleFile) { l.SessionTitle = &empty })
		for sel, want := range map[string]string{"3": title, "4": "from the line", "5": "#5", uuidD: uuidD[:8], uuidE: uuidE[:8]} {
			if got := f.shown(sel)["name"]; got != want {
				t.Errorf("%s: name %v, want %q", sel, got, want)
			}
		}
	})
	t.Run("model, pid, git branch", func(t *testing.T) {
		f := newPruneFixture(t)
		sm := "lifecycle-model"
		f.running(uuidA, time.Minute, 11, func(l *model.LifecycleFile) { l.Model = &sm })
		start := model.FormatTimestamp(f.ago(time.Minute))
		f.sesshin(uuidA, 1, "")
		// A tick from the current life: its model wins; the lifecycle's pid
		// stays.
		f.tick(uuidA, f.ago(30*time.Second), `{"model":{"id":"claude-x"}}`, "main", "", 99)
		// A tick from before the last start: the lifecycle's model.
		f.running(uuidB, time.Minute, 12, func(l *model.LifecycleFile) { l.Model = &sm })
		f.sesshin(uuidB, 2, "")
		f.tick(uuidB, f.ago(2*time.Minute), `{"model":{"id":"stale"}}`, "", "", 0)
		// A tick at exactly the last start counts as the current life.
		f.running(uuidC, time.Minute, 13)
		f.sesshin(uuidC, 3, "")
		f.tick(uuidC, f.ago(time.Minute), `{"model":{"id":"claude-y"}}`, "", "", 0)
		// A tick with no model, a model that isn't an object with an id.
		f.running(uuidD, time.Minute, 14, func(l *model.LifecycleFile) { l.Model = &sm })
		f.sesshin(uuidD, 4, "")
		f.tick(uuidD, f.ago(time.Second), `{"model":{"id":7}}`, "", "", 0)
		// No pid in the lifecycle: the statusline's, which liveness judged.
		f.table[77] = "s77"
		f.session(uuidE, time.Minute, notEnded)
		f.sesshin(uuidE, 5, "")
		f.tick(uuidE, f.ago(time.Second), `{}`, "", "", 77)

		a := f.shown("1")
		if a["model"] != "claude-x" || a["pid"] != float64(11) || a["git_branch"] != "main" || a["last_start_at"] != string(start) {
			t.Errorf("a: %+v", a)
		}
		if b := f.shown("2"); b["model"] != sm || b["git_branch"] != nil {
			t.Errorf("b: %+v", b)
		}
		if c := f.shown("3"); c["model"] != "claude-y" || c["pid"] != float64(13) {
			t.Errorf("c: %+v", c)
		}
		if d := f.shown("4"); d["model"] != sm {
			t.Errorf("d: %+v", d)
		}
		if e := f.shown("5"); e["pid"] != float64(77) || e["liveness"] != "live" || e["model"] != nil {
			t.Errorf("e: %+v", e)
		}
	})
	t.Run("pending", func(t *testing.T) {
		f := newPruneFixture(t)
		f.running(uuidA, time.Minute, 11, func(l *model.LifecycleFile) { l.BackgroundTasks, l.SessionCrons = i64(2), i64(1) })
		f.sesshin(uuidA, 1, "")
		f.running(uuidB, time.Minute, 12)
		f.sesshin(uuidB, 2, "")
		if p := f.shown("1")["pending"]; fmt.Sprint(p) != "map[background_tasks:2 session_crons:1]" {
			t.Errorf("%v", p)
		}
		if p := f.shown("2")["pending"]; p != nil {
			t.Errorf("%v", p)
		}
	})
	t.Run("attention", func(t *testing.T) {
		f := newPruneFixture(t)
		f.running(uuidA, time.Minute, 11, func(l *model.LifecycleFile) {
			l.Status, l.BackgroundTasks, l.SessionCrons = model.StatusWaiting, i64(0), i64(1)
		})
		f.sesshin(uuidA, 1, "")
		f.running(uuidB, time.Minute, 12, func(l *model.LifecycleFile) { l.Status = model.StatusNeedsApproval })
		f.sesshin(uuidB, 2, "")
		if a := f.shown("1")["attention"]; a != "self_waking" {
			t.Errorf("1: %v", a)
		}
		if a := f.shown("2")["attention"]; a != "blocked" {
			t.Errorf("2: %v", a)
		}
	})
	t.Run("headless, stored fields, and placement", func(t *testing.T) {
		f := newPruneFixture(t)
		f.running(uuidA, time.Minute, 11, nested, sdk, func(l *model.LifecycleFile) {
			l.Cwd, l.Compactions, l.StallReason = ptrTo("/work"), 3, ptrTo("rate_limit")
			l.PermissionMode, l.TranscriptPath = ptrTo("plan"), ptrTo("/nope/x.jsonl")
		})
		f.sesshin(uuidA, 1, `{"terminal":"kitty","window_id":4}`)
		v := f.shown("1")
		if v["headless"] != true || v["nested"] != true || v["entrypoint"] != "sdk-cli" || v["cwd"] != "/work" || v["compactions"] != float64(3) ||
			v["stall_reason"] != "rate_limit" || v["permission_mode"] != "plan" || v["status"] != "idle" || v["event_seq"] != float64(1) {
			t.Errorf("%+v", v)
		}
		if p := v["placement"].(map[string]any); p["terminal"] != "kitty" || p["window_id"] != float64(4) {
			t.Errorf("placement %+v", p)
		}
		// Only the keys the schema knows, in its order.
		env := f.show("1", false)
		b, _ := json.Marshal(env.Result.(ShowOutput).Session)
		if got := keys(t, b); !slices.Equal(got, viewFields) {
			t.Errorf("%v", got)
		}
	})
}

func TestViewMetrics(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 1, "")
	f.tick(uuidA, f.ago(10*time.Second), `{
		"cost":{"total_cost_usd":1.5,"total_api_duration_ms":2000},
		"context_window":{"total_input_tokens":1234,"context_window_size":200000,"used_percentage":0.6},
		"rate_limits":{"five_hour":{"used_percentage":10,"resets_at":1790000000}}}`, "", "2.5", 0)
	f.running(uuidB, time.Minute, 12)
	f.sesshin(uuidB, 2, "")
	// A payload with the wrong types reads as null; integers must be whole.
	f.tick(uuidB, f.ago(10*time.Second), `{
		"cost":{"total_cost_usd":"3","total_api_duration_ms":2.5},
		"context_window":{"total_input_tokens":null,"context_window_size":2e5,"used_percentage":"x"},
		"rate_limits":[1]}`, "", "", 0)
	f.running(uuidC, time.Minute, 13)
	f.sesshin(uuidC, 3, "")
	f.tick(uuidC, f.ago(10*time.Second), `{}`, "", "", 0)
	f.running(uuidD, time.Minute, 14)
	f.sesshin(uuidD, 4, "")

	m := f.shown("1")["metrics"].(map[string]any)
	want := map[string]any{
		"received_at": string(model.FormatTimestamp(f.ago(10 * time.Second))), "cost_usd": 1.5, "burn_usd_per_hour": 2.5,
		"api_duration_ms": float64(2000), "context_tokens": float64(1234), "context_window": float64(200000),
		"context_percent": 0.6, "rate_limits": map[string]any{"five_hour": map[string]any{"used_percentage": float64(10), "resets_at": float64(1790000000)}},
	}
	if fmt.Sprint(m) != fmt.Sprint(want) {
		t.Errorf("metrics %v\nwant    %v", m, want)
	}
	m = f.shown("2")["metrics"].(map[string]any)
	for _, k := range []string{"cost_usd", "burn_usd_per_hour", "api_duration_ms", "context_tokens", "context_percent", "rate_limits"} {
		if m[k] != nil {
			t.Errorf("%s: %v, want null", k, m[k])
		}
	}
	if m["context_window"] != float64(200000) { // 2e5 is a whole number
		t.Errorf("context_window %v", m["context_window"])
	}
	m = f.shown("3")["metrics"].(map[string]any)
	if m["received_at"] == nil || m["cost_usd"] != nil || m["rate_limits"] != nil {
		t.Errorf("%v", m)
	}
	if v := f.shown("4"); v["metrics"] != nil || v["prompt_cache"] != nil || v["git_branch"] != nil {
		t.Errorf("%+v", v)
	}
}

func TestViewPromptCache(t *testing.T) {
	f := newPruneFixture(t)
	expires := f.now.Unix() + 600
	cases := []struct {
		payload string
		want    map[string]any
	}{
		{`{}`, map[string]any{"state": "unknown", "expires_at": nil, "recache_tokens": nil, "hit_ratio": nil, "misses": nil, "last_miss_cause": nil}},
		{fmt.Sprintf(`{"prompt_cache":{"warm":true,"caching_observed":true,"expires_at":%d.75,"recache_tokens_if_cold":45000,"hit_ratio":0.91,"misses":2,"last_miss_cause":{"causes":["ttl","edit"]}}}`, expires),
			map[string]any{"state": "warm", "expires_at": string(model.FormatTimestamp(time.Unix(int64(expires), 0))), "recache_tokens": float64(45000), "hit_ratio": 0.91, "misses": float64(2), "last_miss_cause": []any{"ttl", "edit"}}},
		// Stored warm, but expired: cold, and no expires_at.
		{fmt.Sprintf(`{"prompt_cache":{"warm":true,"expires_at":%d,"recache_tokens_if_cold":100}}`, f.now.Unix()-1),
			map[string]any{"state": "cold", "expires_at": nil, "recache_tokens": float64(100), "hit_ratio": nil, "misses": nil, "last_miss_cause": nil}},
		{`{"prompt_cache":{"warm":false,"recache_tokens_if_cold":-1,"last_miss_cause":{"causes":[]}}}`,
			map[string]any{"state": "cold", "expires_at": nil, "recache_tokens": nil, "hit_ratio": nil, "misses": nil, "last_miss_cause": []any{}}},
		{`{"prompt_cache":{"warm":false,"recache_tokens_if_cold":1.5}}`,
			map[string]any{"state": "cold", "expires_at": nil, "recache_tokens": nil, "hit_ratio": nil, "misses": nil, "last_miss_cause": nil}},
		{`{"prompt_cache":{"warm":true,"caching_observed":false,"expires_at":9999999999}}`,
			map[string]any{"state": "unknown", "expires_at": nil, "recache_tokens": nil, "hit_ratio": nil, "misses": nil, "last_miss_cause": nil}},
		{`{"prompt_cache":{"warm":"yes","hit_ratio":"high","misses":1.5,"last_miss_cause":{"causes":[1]}}}`,
			map[string]any{"state": "unknown", "expires_at": nil, "recache_tokens": nil, "hit_ratio": nil, "misses": nil, "last_miss_cause": nil}},
		{`{"prompt_cache":{"warm":true}}`,
			map[string]any{"state": "unknown", "expires_at": nil, "recache_tokens": nil, "hit_ratio": nil, "misses": nil, "last_miss_cause": nil}},
		// A time no timestamp can hold, still in the future: warm, with no expires_at.
		{`{"prompt_cache":{"warm":true,"expires_at":1e12}}`,
			map[string]any{"state": "warm", "expires_at": nil, "recache_tokens": nil, "hit_ratio": nil, "misses": nil, "last_miss_cause": nil}},
		{`{"prompt_cache":7}`,
			map[string]any{"state": "unknown", "expires_at": nil, "recache_tokens": nil, "hit_ratio": nil, "misses": nil, "last_miss_cause": nil}},
	}
	for i, tc := range cases {
		id := fmt.Sprintf("00000000-0000-4000-8000-0000000001%02d", i)
		f.running(id, time.Minute, int64(100+i))
		f.sesshin(id, int64(i+1), "")
		f.tick(id, f.ago(time.Second), tc.payload, "", "", 0)
		got := f.shown(fmt.Sprint(i + 1))["prompt_cache"]
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s\ngot  %v\nwant %v", tc.payload, got, tc.want)
		}
	}
}

func TestViewTranscriptExists(t *testing.T) {
	f := newPruneFixture(t)
	exists := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(exists, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		path *string
		want any
	}{
		{&exists, true},
		{ptrTo(exists + ".gone"), false},
		{ptrTo(filepath.Join(exists, "below")), nil}, // ENOTDIR: not ENOENT
		{nil, nil},
		{ptrTo("relative.jsonl"), nil},
	} {
		id := fmt.Sprintf("00000000-0000-4000-8000-0000000002%02d", i)
		f.running(id, time.Minute, int64(200+i), func(l *model.LifecycleFile) { l.TranscriptPath = tc.path })
		f.sesshin(id, int64(i+1), "")
		v := f.shown(fmt.Sprint(i + 1))
		if v["transcript_exists"] != tc.want {
			t.Errorf("%v: %v, want %v", str(tc.path), v["transcript_exists"], tc.want)
		}
		if tc.path != nil && v["transcript_path"] != *tc.path {
			t.Errorf("%v", v["transcript_path"])
		}
	}
	// Any other stat failure reads as unknown too.
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpStat && op.Path == exists {
			return syscall.EACCES
		}
		return nil
	}
	if v := f.shown("1"); v["transcript_exists"] != nil {
		t.Errorf("EACCES: %v", v["transcript_exists"])
	}
}

func TestShowSelectors(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 12, "")
	f.running(uuidB, 2*time.Minute, 12)
	f.sesshin(uuidB, 13, "")
	f.session(uuidC, time.Hour) // ended
	f.sesshin(uuidC, 14, "")
	f.running(uuidD, time.Minute, 14, nested) // headless
	f.sesshin(uuidD, 15, "")
	// A session with no sesshin ID, and one whose UUID is all digits.
	digits := "12345678-1234-4234-8234-123456789012"
	f.running(digits, time.Minute, 15)
	f.sesshin(digits, 0, "")
	noSesshin := "00000000-0000-4000-8000-0000000000f9"
	f.running(noSesshin, time.Minute, 16)

	for _, tc := range []struct {
		name, sel, want string
	}{
		{"sesshin ID", "12", uuidA},
		{"ended session by ID", "14", uuidC},
		{"headless by ID", "15", uuidD},
		{"full UUID", uuidB, uuidB},
		{"uppercase UUID", strings.ToUpper(uuidB), uuidB},
		{"full UUID, an ended session", uuidC, uuidC},
		{"prefix, uppercase", "0B6C5A3E-1F7E-4C2B-9A51-6D2F0E8B7C1", uuidA},
		{"UUID of a session with no ID", noSesshin, noSesshin},
		{"prefix of one", "00000000-0000-4000", noSesshin},
		{"all digits is an ID, though 8 long", "12345678", ""},
		{"a longer all-digit UUID prefix", "12345678-1", digits},
	} {
		env := f.show(tc.sel, false)
		if tc.want == "" {
			wantKind(t, env, KindNotFound)
			continue
		}
		if !env.OK || env.Result.(ShowOutput).Session.SessionID != tc.want {
			t.Errorf("%s: %+v", tc.name, env)
		}
	}
	// The session view is the one list gives.
	_, l := f.list(`{"liveness":"all","include_headless":true}`)
	v := f.shown("12")
	for _, s := range l.Sessions {
		if s["session_id"] == uuidA && fmt.Sprint(s) != fmt.Sprint(v) {
			t.Errorf("list %v\nshow %v", s, v)
		}
	}
}

func TestShowNotFound(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 1, "")
	f.write(uuidB, "lifecycle.json", []byte(`{`)) // unusable: no selector matches it
	f.write(uuidB, "sesshin.json", []byte(`{"schema": 2,"id":2,"job":null,"source":"hook","placement":null,"extra":{}}`))
	for _, sel := range []string{"2", "99", uuidB, "ffffffff", "0b6c5a3E-1f7e-4c2b-9a51-6d2f0e8b7c1f"} {
		env := f.show(sel, false)
		wantKind(t, env, KindNotFound)
		if got := env.Error.Details["selectors"].([]string); !slices.Equal(got, []string{sel}) {
			t.Errorf("%s: %+v", sel, env.Error.Details)
		}
	}
}

func TestShowAmbiguous(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 1, "")
	f.session(uuidB, 5*time.Minute) // ended: listed after the live one
	f.sesshin(uuidB, 2, "")
	env := f.show("0b6c5a3e", false)
	wantKind(t, env, KindAmbiguous)
	if env.Error.Details["selector"] != "0b6c5a3e" || env.Error.Details["candidates_truncated"] != nil {
		t.Errorf("%+v", env.Error.Details)
	}
	cands := env.Error.Details["candidates"].([]SessionRef)
	if len(cands) != 2 || cands[0].SessionID != uuidA || cands[1].SessionID != uuidB || *cands[1].ID != 2 || cands[1].Name != "#2" {
		t.Errorf("%+v", cands)
	}
	// An uppercase selector is reported as given.
	if env := f.show("0B6C5A3E", false); env.Error.Details["selector"] != "0B6C5A3E" {
		t.Errorf("%+v", env.Error.Details)
	}
	// A sesshin ID duplicated by an outside change.
	f.sesshin(uuidB, 1, "")
	env = f.show("1", false)
	wantKind(t, env, KindAmbiguous)
	if got := env.Error.Details["candidates"].([]SessionRef); len(got) != 2 {
		t.Errorf("%+v", got)
	}
}

func TestShowAmbiguousTruncated(t *testing.T) {
	f := newPruneFixture(t)
	for i := range 23 {
		id := fmt.Sprintf("abcdef00-0000-4000-8000-%012d", i)
		f.running(id, time.Duration(i+1)*time.Minute, int64(300+i))
	}
	env := f.show("abcdef00", false)
	wantKind(t, env, KindAmbiguous)
	cands := env.Error.Details["candidates"].([]SessionRef)
	if len(cands) != 20 || env.Error.Details["candidates_truncated"] != true {
		t.Fatalf("%d candidates: %+v", len(cands), env.Error.Details)
	}
	// In session order: the most recently seen first.
	if cands[0].SessionID != "abcdef00-0000-4000-8000-000000000000" || cands[19].SessionID != "abcdef00-0000-4000-8000-000000000019" {
		t.Errorf("%v … %v", cands[0], cands[19])
	}
	f.running("abcdef01-0000-4000-8000-000000000000", time.Minute, 400)
	env = f.show("abcdef00-0000-4000-8000-00000000000", false) // 23 sessions, 10 of them
	wantKind(t, env, KindAmbiguous)
	if env.Error.Details["candidates_truncated"] != nil || len(env.Error.Details["candidates"].([]SessionRef)) != 10 {
		t.Errorf("%+v", env.Error.Details)
	}
}

func TestShowPayload(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 1, "")
	raw := `{"zeta":1,"alpha":{"b":2.50,"a":[1,"x"]},"model":{"id":"m"},"unicode":"é"}`
	f.tick(uuidA, f.ago(time.Second), raw, "", "", 0)
	f.running(uuidB, time.Minute, 12)
	f.sesshin(uuidB, 2, "")
	f.running(uuidC, time.Minute, 13)
	f.sesshin(uuidC, 3, "")
	f.write(uuidC, "statusline.json", []byte(`{"schema":9}`))

	// Absent without include_payload; the stored payload verbatim with it,
	// key order and number text kept; null without a usable statusline.json.
	env := f.show("1", false)
	if b, _ := json.Marshal(env.Result); strings.Contains(string(b), "statusline_payload") {
		t.Errorf("%s", b)
	}
	for sel, want := range map[string]string{"1": raw, "2": "null", "3": "null"} {
		env := f.show(sel, true)
		b, err := json.Marshal(env.Result)
		if err != nil {
			t.Fatal(err)
		}
		var o struct {
			Payload json.RawMessage `json:"statusline_payload"`
		}
		if err := json.Unmarshal(b, &o); err != nil {
			t.Fatal(err)
		}
		if string(o.Payload) != want {
			t.Errorf("%s: payload %s, want %s", sel, o.Payload, want)
		}
		if !strings.Contains(string(b), `"statusline_payload"`) {
			t.Errorf("%s: no statusline_payload in %s", sel, b)
		}
	}
}

func TestShowWarnings(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 1, "")
	f.write(uuidA, "statusline.json", []byte(`x`)) // the selected session's own
	f.running(uuidC, time.Minute, 12)
	f.write(uuidC, "sesshin.json", []byte(`{`))    // might have matched an ID
	f.write(uuidC, "statusline.json", []byte(`{`)) // another session's statusline: never
	f.write(uuidD, "lifecycle.json", []byte(`{`))  // might have matched either
	f.write(uuidE, "lifecycle.json", []byte(`{`))

	paths := func(env Envelope) []string {
		var out []string
		for _, w := range env.Warnings {
			if w.Kind != KindUnusableFile {
				t.Errorf("%+v", w)
			}
			p := w.Details["path"].(string)
			out = append(out, filepath.Base(filepath.Dir(p))[:8]+"/"+filepath.Base(p))
		}
		return out
	}
	// By ID: every session's lifecycle.json and sesshin.json might have matched.
	env := f.show("1", false)
	if !env.OK {
		t.Fatalf("%+v", env.Error)
	}
	want := []string{"0b6c5a3e/statusline.json", "7d1e0000/sesshin.json", "7d1e0000/lifecycle.json", "a0000000/lifecycle.json"}
	if got := paths(env); !slices.Equal(got, want) {
		t.Errorf("by ID: %v, want %v", got, want)
	}
	// By UUID: only the sessions the prefix could name.
	env = f.show(uuidA[:12], false)
	if got := paths(env); !slices.Equal(got, []string{"0b6c5a3e/statusline.json"}) {
		t.Errorf("by UUID: %v", got)
	}
	// A failure carries them too: the unusable lifecycle.json is why.
	env = f.show(uuidD, false)
	wantKind(t, env, KindNotFound)
	if got := paths(env); !slices.Equal(got, []string{"7d1e0000/lifecycle.json"}) {
		t.Errorf("not found: %v", got)
	}
}

// A fault in a read of a session's file is io, not a warning.
func TestListIOFault(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshin(uuidA, 1, "")
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpReadFile && filepath.Base(op.Path) == "sesshin.json" {
			return syscall.EACCES
		}
		return nil
	}
	li, _ := DecodeInput([]byte(`{}`), DecodeListInput)
	env := List(li, f.env())
	checkEnvelope(t, env, "list-output")
	wantKind(t, env, KindIO)
	if env.Error.Details["code"] != "EACCES" || filepath.Base(env.Error.Details["path"].(string)) != "sesshin.json" {
		t.Errorf("%+v", env.Error)
	}
	wantKind(t, f.show("1", false), KindIO)
}
