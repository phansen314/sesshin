package model

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/schematest"
)

func fixture(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ptr[T any](v T) *T { return &v }

const uuid = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

// readAny reads data as the file kind named, for tests that run every kind.
func readAny(kind, data string) (any, FileResult) {
	switch kind {
	case "state.json":
		return ReadState([]byte(data))
	case "lifecycle.json":
		return ReadLifecycle([]byte(data), uuid)
	case "statusline.json":
		return ReadStatusline([]byte(data))
	case "sesshin.json":
		return ReadSesshin([]byte(data))
	case "install.json":
		return ReadInstall([]byte(data))
	case "reservation.json":
		return ReadReservation([]byte(data), "api-review")
	}
	panic(kind)
}

var kinds = []string{"state.json", "lifecycle.json", "statusline.json", "sesshin.json", "install.json", "reservation.json"}

// Each fixture is valid by both the validator and the schema, and is written
// back byte for byte: fields in schema order, in the File format.
func TestFixturesRoundTrip(t *testing.T) {
	for _, kind := range kinds {
		data := fixture(t, kind)
		v, r := readAny(kind, data)
		if !r.Usable {
			t.Errorf("%s: unusable: %v", kind, r.Problems)
			continue
		}
		if ok, f := schematest.Check(t, strings.TrimSuffix(kind, ".json")+"-file", []byte(data)); !ok {
			t.Errorf("%s: schema rejects at %s", kind, f)
		}
		out, err := jsonio.MarshalFile(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != data {
			t.Errorf("%s: written back as\n%s\nwant\n%s", kind, out, data)
		}
	}
}

// Files written from structs read back to the same structs. The payload is
// stored raw and read as a tree, so it is compared by what it writes.
func TestWriteRead(t *testing.T) {
	now := Timestamp("2026-10-03T18:31:51Z")
	lifecycles := []LifecycleFile{
		{SessionID: uuid, StartedAt: now, LastStartAt: now, Status: "working", LastEventType: "start", LastEventAt: now, EventSeq: 1},
		{
			SessionID: uuid, Cwd: ptr("/tmp/<a>&\"b\" é"), TranscriptPath: ptr("/t.jsonl"), SessionTitle: ptr("x\u00A0y"), Model: ptr("claude-opus-5-5"),
			PermissionMode: ptr("bypassPermissions"), PID: ptr(int64(MaxSafe)), PIDStartedAt: ptr("linux:b:1"), Entrypoint: ptr("sdk-cli"), Nested: ptr(true),
			StartedAt: now, LastStartAt: now, Status: "waiting", Compactions: MaxSafe, StallReason: ptr("unknown"),
			BackgroundTasks: ptr(int64(3)), SessionCrons: ptr(int64(0)), LastEventType: "end:other", LastEventAt: now, EventSeq: MaxSafe,
			EndedPromptID: ptr("p"), EndedAt: &now, EndReason: ptr("other"),
		},
	}
	for _, want := range lifecycles {
		got, r := ReadLifecycle(write(t, want), uuid)
		if !r.Usable || !reflect.DeepEqual(got, want) {
			t.Errorf("lifecycle: read %+v (%v), want %+v", got, r.Problems, want)
		}
	}

	raw, err := jsonio.Payload([]byte(`{"session_id":"` + uuid + `","cost":{"total_cost_usd":1.50},"n":1e2,"s":"<a>&é"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []StatuslineFile{
		{ReceivedAt: now, Payload: Payload{Raw: json.RawMessage(`{}`)}},
		{
			ReceivedAt: now, ReceivedNS: 1<<63 - 1, Payload: Payload{Raw: raw}, GitBranch: ptr("feature/x"),
			CostSample: &CostSample{At: now, USD: 0}, BurnUSDPerHour: ptr(12.5), PID: ptr(int64(1)), PIDStartedAt: ptr(""),
		},
	} {
		data := write(t, want)
		got, r := ReadStatusline(data)
		if !r.Usable {
			t.Errorf("statusline: unusable: %v", r.Problems)
			continue
		}
		if again := write(t, got); !bytes.Equal(again, data) {
			t.Errorf("statusline: rewritten as\n%s\nwant\n%s", again, data)
		}
		tree := got.Payload.Tree
		got.Payload, want.Payload = Payload{}, Payload{}
		if !reflect.DeepEqual(got, want) || tree == nil {
			t.Errorf("statusline: read %+v, want %+v", got, want)
		}
	}

	placement := &jsonio.Object{Members: []jsonio.Member{{Key: "terminal", Value: "kitty"}, {Key: "window_id", Value: json.Number("7")}}}
	extra := &jsonio.Object{Members: []jsonio.Member{{Key: "n", Value: json.Number("1.10")}, {Key: "big", Value: json.Number("1e400")}, {Key: "l", Value: []any{json.Number("-0"), "x"}}}}
	for _, want := range []SesshinFile{
		{Source: SourceHook, Extra: &jsonio.Object{}},
		{ID: ptr(int64(12)), Job: ptr("api-review"), Source: SourceSpawn, Placement: placement, Extra: extra},
	} {
		got, r := ReadSesshin(write(t, want))
		if !r.Usable || !reflect.DeepEqual(got, want) {
			t.Errorf("sesshin: read %+v (%v), want %+v", got, r.Problems, want)
		}
	}

	token := "3fa85f6457174562b3fc2c963f66afa6"
	for _, want := range []ReservationFile{
		{Job: "a", Token: token, CreatedAt: now},
		{Job: "api-review", Token: token, CreatedAt: now, Placement: placement},
	} {
		got, r := ReadReservation(write(t, want), want.Job)
		if !r.Usable || !reflect.DeepEqual(got, want) {
			t.Errorf("reservation: read %+v (%v), want %+v", got, r.Problems, want)
		}
	}

	for _, want := range []StateFile{{}, {LastID: MaxSafe, Migration: MaxSafe}, {LastID: 3, Migration: LatestMigration}} {
		got, r := ReadState(write(t, want))
		if !r.Usable || got != want {
			t.Errorf("state: read %+v (%v), want %+v", got, r.Problems, want)
		}
	}

	want := InstallFile{HookBinary: "/bin/sesshin-hook", Version: "(devel)", InstalledAt: now, Locations: InstallLocations{"/c", "/s", "/s.json"}}
	if got, r := ReadInstall(write(t, want)); !r.Usable || got != want {
		t.Errorf("install: read %+v (%v), want %+v", got, r.Problems, want)
	}
}

func write(t *testing.T, v any) []byte {
	t.Helper()
	b, err := jsonio.MarshalFile(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A read payload is written back with its key order and number text, but its
// strings re-escaped.
func TestPayloadReadBack(t *testing.T) {
	data := strings.Replace(fixture(t, "statusline.json"), `"hook_event_name": "Status"`, `"hook_event_name": "Status", "z": 1.50, "a": 1e2`, 1)
	s, r := ReadStatusline([]byte(data))
	if !r.Usable {
		t.Fatal(r.Problems)
	}
	out := string(write(t, s))
	if want := `"hook_event_name": "Status",` + "\n" + `    "z": 1.50,` + "\n" + `    "a": 1e2,`; !strings.Contains(out, want) {
		t.Errorf("written back as\n%s\nwant it to contain\n%s", out, want)
	}
}

// The schema field is written as the file's version whatever else is set.
func TestSchemaWritten(t *testing.T) {
	for _, v := range []any{StateFile{}, LifecycleFile{}, StatuslineFile{}, SesshinFile{}, InstallFile{}, ReservationFile{}} {
		n := "1"
		switch v.(type) {
		case StateFile, SesshinFile:
			n = "2"
		}
		if b := write(t, v); !bytes.HasPrefix(b, []byte("{\n  \"schema\": "+n+",\n")) {
			t.Errorf("%T: written as %s", v, b)
		}
	}
}

// File validity, in order: read failure, then the schema field, before any
// other field is looked at. Each such file is unusable at once.
func TestEarly(t *testing.T) {
	for _, tc := range []struct{ data, field string }{
		{``, ""},
		{"  \n", ""},
		{`{`, ""},
		{`[]`, ""},
		{`{"schema": 1} x`, ""},
		{"\xef\xbb\xbf{\"schema\": 1}", ""},
		{"{\"schema\": 1, \"x\": \"\xff\"}", ""},
		{`{"schema": 1, "x": "\ud800"}`, ""},
		{`{"last_id": 1}`, "/schema"},
		{`{"schema": 99, "last_id": 1}`, "/schema"},
		{`{"schema": "1", "last_id": 1}`, "/schema"},
		{`{"schema": 1.0, "last_id": 1}`, "/schema"},
		{`{"schema": 1e0, "last_id": 1}`, "/schema"},
		{`{"schema": 01, "last_id": 1}`, ""}, // not JSON
		{`{"Schema": 1, "last_id": 1}`, "/schema"},
	} {
		for _, kind := range kinds {
			_, r := readAny(kind, tc.data)
			if r.Usable || !r.Early || len(r.Problems) != 1 || r.Problems[0].Field != tc.field {
				t.Errorf("%s %q: %+v, want early at %q", kind, tc.data, r, tc.field)
			}
		}
	}
}

// After the schema field: a repeated key, an unknown key (keys match
// exactly, by case too), and every field's problem, all reported.
func TestFieldProblems(t *testing.T) {
	for _, tc := range []struct {
		data string
		want []Problem
	}{
		{`{"schema": 2, "last_id": 1, "migration": 0, "last_id": 2}`, []Problem{{"/last_id", reasonRepeated}}},
		{`{"schema": 2, "schema": 2, "last_id": 1, "migration": 0}`, []Problem{{"/schema", reasonRepeated}}},
		{`{"schema": 2, "last_id": 1, "migration": 0, "x": 1, "x": 2}`, []Problem{{"/x", reasonRepeated}, {"/x", reasonUnknown}}},
		{`{"schema": 2, "Last_id": 1, "migration": 0}`, []Problem{{"/last_id", reasonRequired}, {"/Last_id", reasonUnknown}}},
		{`{"schema": 2, "last_id": -1, "migration": 0, "a/b": 1}`, []Problem{{"/last_id", "must be between 0 and 9007199254740991"}, {"/a~1b", reasonUnknown}}},
	} {
		_, r := ReadState([]byte(tc.data))
		if r.Usable || r.Early || !slices.Equal(r.Problems, tc.want) {
			t.Errorf("%s: %+v, want %v", tc.data, r, tc.want)
		}
	}
	// A repeated schema key is corrupt with no version check: even one
	// that would be another format is not.
	for _, doc := range []string{`{"schema": 1, "schema": 1, "last_id": 1, "migration": 0}`, `{"schema": 9, "schema": 2, "last_id": 1, "migration": 0}`, `{"schema": 2, "schema": 9, "last_id": 1, "migration": 0}`} {
		_, r := ReadState([]byte(doc))
		if r.Usable || r.OtherFormat || r.Early {
			t.Errorf("%s: %+v, want corrupt", doc, r)
		}
	}
	// Repeated keys are beyond the schema, which can't see them.
	_, r := ReadState([]byte(`{"schema": 2, "last_id": 1, "migration": 0, "last_id": 2}`))
	if len(r.SchemaProblems) != 0 {
		t.Errorf("repeated key in SchemaProblems: %v", r.SchemaProblems)
	}
	if got, want := r.Reason(), "/last_id: repeated key"; got != want {
		t.Errorf("Reason %q, want %q", got, want)
	}
	_, r = ReadState([]byte(`{"schema": 2, "x": 1}`))
	if got, want := r.Reason(), "/last_id: required (and 2 more)"; got != want {
		t.Errorf("Reason %q, want %q", got, want)
	}
}

// A schema that is an integer literal within ±MaxSafe but not the supported
// version is another format, with the version found; any other schema is
// corrupt.
func TestOtherFormat(t *testing.T) {
	for _, tc := range []struct {
		schema string
		found  int64
	}{
		{`1`, 1}, {`3`, 3}, {`0`, 0}, {`-1`, -1}, {`-0`, 0}, {`99`, 99},
		{`9007199254740991`, MaxSafe}, {`-9007199254740991`, -MaxSafe},
	} {
		for _, kind := range kinds {
			if kind == "state.json" || kind == "sesshin.json" {
				if tc.found == 2 {
					continue
				}
			} else if tc.found == 1 {
				continue // the supported version
			}
			doc := `{"schema": ` + tc.schema + `, "x": 1}`
			_, r := readAny(kind, doc)
			if r.Usable || !r.Early || !r.OtherFormat || r.Found != tc.found || len(r.Problems) != 1 || r.Problems[0].Field != "/schema" {
				t.Errorf("%s %s: %+v, want other format %d", kind, doc, r, tc.found)
			}
		}
	}
	if _, r := ReadState([]byte(`{"schema": 1}`)); r.Reason() != "/schema: in format 1, not 2" {
		t.Errorf("Reason %q", r.Reason())
	}
	for _, schema := range []string{`2.0`, `"2"`, `1e0`, `1E0`, `10e-1`, `9007199254740992`, `-9007199254740992`, `99999999999999999999`, `null`, `true`, `[]`} {
		for _, kind := range kinds {
			_, r := readAny(kind, `{"schema": `+schema+`}`)
			if r.Usable || !r.Early || r.OtherFormat || r.Found != 0 {
				t.Errorf("%s schema %s: %+v, want corrupt", kind, schema, r)
			}
		}
	}
	for _, kind := range kinds {
		_, r := readAny(kind, `{"id": 1}`)
		if r.Usable || r.OtherFormat {
			t.Errorf("%s without schema: %+v", kind, r)
		}
	}
}

// state.json's migration: a required integer literal from 0 to MaxSafe.
func TestStateMigration(t *testing.T) {
	doc := func(m string) string { return `{"schema": 2, "last_id": 1, "migration": ` + m + `}` }
	for _, tc := range []struct {
		m    string
		want int64
		ok   bool
	}{
		{`0`, 0, true}, {`1`, 1, true}, {`9007199254740991`, MaxSafe, true},
		{`-1`, 0, false}, {`9007199254740992`, 0, false}, {`1.0`, 0, false}, {`1e0`, 0, false},
		{`"1"`, 0, false}, {`null`, 0, false},
	} {
		s, r := ReadState([]byte(doc(tc.m)))
		if r.Usable != tc.ok || tc.ok && s.Migration != tc.want || !tc.ok && (r.OtherFormat || len(r.Problems) != 1 || r.Problems[0].Field != "/migration") {
			t.Errorf("migration %s: %+v, %+v", tc.m, s, r)
		}
	}
	if _, r := ReadState([]byte(`{"schema": 2, "last_id": 1}`)); r.Usable || len(r.Problems) != 1 || r.Problems[0] != (Problem{"/migration", reasonRequired}) {
		t.Errorf("missing migration: %+v", r)
	}
}

// set returns the fixture kind with the member at path (keys, outermost
// first) set to value, a JSON text.
func set(t *testing.T, kind string, value string, path ...string) string {
	t.Helper()
	return setDoc(t, fixture(t, kind), value, path...)
}

// setDoc is set, on the document doc.
func setDoc(t *testing.T, doc string, value string, path ...string) string {
	t.Helper()
	obj, _, err := jsonio.ParseObject([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := jsonio.ParseValue([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	o := obj
	for _, k := range path[:len(path)-1] {
		child, _ := o.Get(k)
		o = child.(*jsonio.Object)
	}
	o.Set(path[len(path)-1], v)
	b, err := jsonio.MarshalFile(obj)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// integerFields are every integer-typed field of each file kind.
var integerFields = map[string][][]string{
	"state.json":      {{"last_id"}, {"migration"}},
	"lifecycle.json":  {{"pid"}, {"compactions"}, {"background_tasks"}, {"session_crons"}, {"event_seq"}},
	"statusline.json": {{"received_ns"}, {"pid"}},
	"sesshin.json":    {{"id"}},
}

// Integer fields take only integer literals, though the schema's integer
// accepts any integral number: tested here, outside the agreement.
func TestIntegerLiterals(t *testing.T) {
	for kind, fields := range integerFields {
		for _, path := range fields {
			for _, lit := range []string{`2.0`, `2e0`, `2E0`, `20e-1`, `2.00`} {
				doc := set(t, kind, lit, path...)
				_, r := readAny(kind, doc)
				want := []Problem{{jsonio.Pointer("", path[0]), reasonLiteral}}
				if r.Usable || !slices.Equal(r.Problems, want) {
					t.Errorf("%s %s = %s: %+v, want %v", kind, path, lit, r, want)
				}
				if ok, f := schematest.Check(t, strings.TrimSuffix(kind, ".json")+"-file", []byte(doc)); !ok {
					t.Errorf("%s %s = %s: the schema rejects it at %s; the agreement could cover it", kind, path, lit, f)
				}
			}
			if _, r := readAny(kind, set(t, kind, `2`, path...)); !r.Usable {
				t.Errorf("%s %s = 2: %v", kind, path, r.Problems)
			}
		}
	}
}

// The non-integer numbers take any JSON number at least 0 that a float64
// holds. Negativity is read from the text: -1e-400 parses as -0.
func TestNumbers(t *testing.T) {
	for _, path := range [][]string{{"burn_usd_per_hour"}, {"cost_sample", "usd"}} {
		at := "/" + strings.Join(path, "/")
		for _, tc := range []struct {
			lit        string
			ok         bool
			additional bool
		}{
			{`0`, true, false}, {`-0`, true, false}, {`-0.0e5`, true, false}, {`2.50`, true, false}, {`1.5e2`, true, false},
			{`1E-400`, true, false}, {`1.7976931348623157e308`, true, false},
			{`-1`, false, false}, {`-1e-400`, false, false}, {`-0.001`, false, false},
			{`1e400`, false, true}, {`1.8e308`, false, true},
		} {
			_, r := ReadStatusline([]byte(set(t, "statusline.json", tc.lit, path...)))
			switch {
			case r.Usable != tc.ok:
				t.Errorf("%s = %s: usable %v (%v), want %v", at, tc.lit, r.Usable, r.Problems, tc.ok)
			case !tc.ok && (len(r.Problems) != 1 || r.Problems[0].Field != at):
				t.Errorf("%s = %s: problems %v, want one at %s", at, tc.lit, r.Problems, at)
			case !tc.ok && (len(r.SchemaProblems) == 0) != tc.additional:
				t.Errorf("%s = %s: schema problems %v, want beyond the schema %v", at, tc.lit, r.SchemaProblems, tc.additional)
			}
		}
	}
}

// The rules stated only in prose (design-spec.md, File fields), beyond the
// schemas: each makes the file unusable, and none is a schema problem.
func TestRulesBeyondSchema(t *testing.T) {
	type tc struct {
		name, kind, doc string
		want            Problem
	}
	cases := []tc{
		{"session_id is not the directory", "lifecycle.json", set(t, "lifecycle.json", `"00000000-0000-4000-8000-000000000000"`, "session_id"),
			Problem{"/session_id", "must be its directory's name, " + uuid}},
		{"lifecycle pid without its start", "lifecycle.json", set(t, "lifecycle.json", `null`, "pid_started_at"),
			Problem{"/pid_started_at", "must be null exactly when pid is"}},
		{"lifecycle start without its pid", "lifecycle.json", set(t, "lifecycle.json", `null`, "pid"),
			Problem{"/pid_started_at", "must be null exactly when pid is"}},
		{"statusline pid without its start", "statusline.json", set(t, "statusline.json", `null`, "pid_started_at"),
			Problem{"/pid_started_at", "must be null exactly when pid is"}},
		{"statusline start without its pid", "statusline.json", set(t, "statusline.json", `null`, "pid"),
			Problem{"/pid_started_at", "must be null exactly when pid is"}},
		{"reservation job is not the file", "reservation.json", set(t, "reservation.json", `"other-job"`, "job"),
			Problem{"/job", "its key must be its file's name, api-review"}},
		{"end_reason without ended_at", "lifecycle.json", set(t, "lifecycle.json", `"other"`, "end_reason"),
			Problem{"/end_reason", "must be null while ended_at is"}},
	}
	for kind, field := range map[string][]string{
		"lifecycle.json":   {"started_at"},
		"statusline.json":  {"cost_sample", "at"},
		"install.json":     {"installed_at"},
		"reservation.json": {"created_at"},
	} {
		for _, ts := range []string{"2026-02-30T00:00:00Z", "2026-10-03T24:00:00Z", "2026-10-03T23:59:60Z", "2026-13-01T00:00:00Z", "2026-10-00T00:00:00Z"} {
			cases = append(cases, tc{"unreal " + ts, kind, set(t, kind, `"`+ts+`"`, field...), Problem{"/" + strings.Join(field, "/"), "must be a real date and time"}})
		}
	}
	for _, c := range cases {
		_, r := readAny(c.kind, c.doc)
		if r.Usable || !slices.Equal(r.Problems, []Problem{c.want}) || len(r.SchemaProblems) != 0 {
			t.Errorf("%s: %+v, want only %v, beyond the schema", c.name, r, c.want)
		}
		if ok, f := schematest.Check(t, strings.TrimSuffix(c.kind, ".json")+"-file", []byte(c.doc)); !ok {
			t.Errorf("%s: the schema rejects it at %s", c.name, f)
		}
	}

	// What the rules allow: an ended session whose reason failed its guard,
	// and both pid fields null; and a rule isn't checked against a field that
	// failed its own check.
	for _, doc := range []string{
		set(t, "lifecycle.json", `"2026-10-03T18:40:00Z"`, "ended_at"),
		setDoc(t, set(t, "lifecycle.json", `null`, "pid"), `null`, "pid_started_at"),
	} {
		if _, r := ReadLifecycle([]byte(doc), uuid); !r.Usable {
			t.Errorf("%s: %v", doc, r.Problems)
		}
	}
	_, r := ReadLifecycle([]byte(set(t, "lifecycle.json", `0`, "pid")), uuid)
	if want := []Problem{{"/pid", "must be between 1 and 9007199254740991"}}; !slices.Equal(r.Problems, want) {
		t.Errorf("pid 0: %v, want %v", r.Problems, want)
	}
}
