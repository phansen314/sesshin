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
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
	"github.com/phansen314/sesshin/internal/schematest"
)

// sesshinExtra writes a usable sesshin.json for a session, id 0 for a null
// ID, with the extra's JSON as given.
func (f *pruneFixture) sesshinExtra(session string, id int64, extra string) {
	f.t.Helper()
	idJSON := "null"
	if id != 0 {
		idJSON = fmt.Sprint(id)
	}
	b := fmt.Sprintf("{\n  \"schema\": 2,\n  \"id\": %s,\n  \"job\": \"api\",\n  \"source\": \"spawn\",\n  \"placement\": null,\n  \"extra\": %s\n}\n", idJSON, extra)
	if _, r := model.ReadSesshin([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture sesshin.json unusable: %s", r.Reason())
	}
	f.write(session, "sesshin.json", []byte(b))
}

func (f *pruneFixture) sesshinPath(session string) string {
	return filepath.Join(f.loc.SessionDir(session), "sesshin.json")
}

// stored is the extra in a session's sesshin.json, as compact JSON.
func (f *pruneFixture) stored(session string) string {
	f.t.Helper()
	b, err := os.ReadFile(f.sesshinPath(session))
	if err != nil {
		f.t.Fatal(err)
	}
	h, r := model.ReadSesshin(b)
	if !r.Usable {
		f.t.Fatalf("sesshin.json unusable: %s", r.Reason())
	}
	out, err := jsonio.MarshalLine(h.Extra)
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimSuffix(string(out), "\n")
}

// updateRaw runs update on the JSON input as given, through the decoder.
func (f *pruneFixture) updateRaw(in string) Envelope {
	f.t.Helper()
	ui, e := DecodeInput([]byte(in), DecodeUpdateInput)
	var env Envelope
	if e != nil {
		env = Failed(e)
	} else {
		env = Update(ui, f.env())
	}
	checkEnvelope(f.t, env, "update-output")
	return env
}

// update runs update on a selector and the JSON of its extra.
func (f *pruneFixture) update(selector, extra string) Envelope {
	f.t.Helper()
	s, _ := json.Marshal(selector)
	return f.updateRaw(`{"session":` + string(s) + `,"extra":` + extra + `}`)
}

// updated is an update that must have succeeded.
func (f *pruneFixture) updated(selector, extra string) UpdateOutput {
	f.t.Helper()
	env := f.update(selector, extra)
	if !env.OK {
		f.t.Fatalf("update failed: %+v", env.Error)
	}
	return env.Result.(UpdateOutput)
}

// A live session with a sesshin.json that has an ID and the extra given.
func (f *pruneFixture) liveWithExtra(extra string) {
	f.t.Helper()
	f.running(uuidA, time.Minute, 11)
	f.sesshinExtra(uuidA, 1, extra)
}

func viewExtra(t *testing.T, out UpdateOutput) string {
	t.Helper()
	b, err := jsonio.MarshalLine(out.Session.Extra)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

// Operations.md, update: every input check, at its field; and whether the
// schema rejects it too.
func TestUpdateInputChecks(t *testing.T) {
	long := strings.Repeat("x", 65536)
	deep := func(n int) string { return strings.Repeat(`{"a":`, n-1) + `{}` + strings.Repeat("}", n-1) }
	for _, tc := range []struct {
		in     string
		field  string // "": accepted
		schema bool   // the published schema rejects it too
	}{
		{`{"session":"12","extra":{"replace_all":{}}}`, "", false},
		{`{"session":"12","extra":{"replace_all":{"a":1.10}}}`, "", false},
		{`{"session":"self","extra":{"merge":{"a":null}}}`, "", false},
		{`{"session":"api","extra":{"remove":["a","b"]}}`, "", false},
		{`{"session":"api","extra":{"merge":{"a":1},"remove":["b"]}}`, "", false},
		{`{"session":"0b6c5a3e","extra":{"merge":{"a":` + deep(31) + `}}}`, "", false},
		{`{"session":"12","extra":{"merge":{"a":` + deep(32) + `}}}`, "/extra/merge", false},
		{`{"session":"12","extra":{"replace_all":` + deep(33) + `}}`, "/extra/replace_all", false},
		{`{"session":"12","extra":{"replace_all":{"k":"` + long + `"}}}`, "/extra/replace_all", false},
		{`{"session":"12","extra":{"merge":{"k":"` + long + `"}}}`, "/extra/merge", false},
		{`{"extra":{"merge":{"a":1}}}`, "/session", true},
		{`{"session":"12"}`, "/extra", true},
		{`{"session":"12","extra":{}}`, "/extra", true},
		{`{"session":"12","extra":[]}`, "/extra", true},
		{`{"session":"12","extra":null}`, "/extra", true},
		{`{"session":"12","extra":{"replace_all":[]}}`, "/extra/replace_all", true},
		{`{"session":"12","extra":{"replace_all":null}}`, "/extra/replace_all", true},
		{`{"session":"12","extra":{"replace_all":{},"merge":{"a":1}}}`, "/extra/merge", true},
		{`{"session":"12","extra":{"replace_all":{},"remove":["a"]}}`, "/extra/remove", true},
		{`{"session":"12","extra":{"merge":{}}}`, "/extra/merge", true},
		{`{"session":"12","extra":{"merge":[1]}}`, "/extra/merge", true},
		{`{"session":"12","extra":{"remove":[]}}`, "/extra/remove", true},
		{`{"session":"12","extra":{"remove":"a"}}`, "/extra/remove", true},
		{`{"session":"12","extra":{"remove":["a","a"]}}`, "/extra/remove/1", true},
		{`{"session":"12","extra":{"remove":["a",1]}}`, "/extra/remove/1", true},
		{`{"session":"12","extra":{"merge":{"a":1},"remove":["a"]}}`, "/extra/remove/0", false},
		{`{"session":"12","extra":{"merge":{"a":1},"remove":["b","a"]}}`, "/extra/remove/1", false},
		{`{"session":"12","extra":{"merge":{"a":1},"other":1}}`, "/extra/other", true},
		{`{"session":"12","extra":{"merge":{"a":1}},"other":1}`, "/other", true},
		{`{"session":"#12","extra":{"merge":{"a":1}}}`, "/session", true},
		{`{"session":"012","extra":{"merge":{"a":1}}}`, "/session", true},
		{`{"session":"12","extra":{"merge":{"a":1,"a":2}}}`, "/extra/merge/a", false},
	} {
		_, e := DecodeInput([]byte(tc.in), DecodeUpdateInput)
		switch {
		case tc.field == "" && e != nil:
			t.Errorf("%.100s: %+v", tc.in, e)
		case tc.field != "":
			if e == nil {
				t.Errorf("%.100s: accepted", tc.in)
				continue
			}
			ps := e.Details["problems"].([]model.Problem)
			if e.Kind != KindInvalidInput || !slices.ContainsFunc(ps, func(p model.Problem) bool { return p.Field == tc.field }) {
				t.Errorf("%.100s: %+v, want a problem at %s", tc.in, e, tc.field)
			}
		}
		if schemaOK, _ := schematest.Check(t, "update-input", []byte(tc.in)); schemaOK == tc.schema {
			t.Errorf("%.100s: the schema says %v", tc.in, schemaOK)
		}
	}
}

func TestUpdateInputAgreesWithSchema(t *testing.T) {
	beyond := func(doc string) bool {
		// The rules no schema states: merge and remove sharing a key, and
		// extra's limits (none of the documents here break them).
		return strings.Contains(doc, `"remove"`) && strings.Contains(doc, `"merge"`)
	}
	agreeInput(t, "update-input", `{"session": "12", "extra": {"merge": {"a": 1}, "remove": ["b"]}}`, DecodeUpdateInput, beyond,
		`{"session": "self", "extra": {"replace_all": {}}}`,
		`{"session": "api", "extra": {"merge": {"a": 1}, "remove": ["a"]}}`,
		`{"session": "job:self", "extra": {"remove": ["a"]}}`,
		`{"session": "a1b2c3d4", "extra": {"replace_all": {"x": [1, {"y": null}]}}}`)
}

// Each form's effect, and key order: existing keys keep their position, new
// ones append in the order given.
func TestUpdateEffects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before string
		change string
		after  string
		stays  bool // the extra is unchanged
	}{
		{"replace_all", `{"a":1,"b":2}`, `{"replace_all":{"c":3,"a":0}}`, `{"c":3,"a":0}`, false},
		{"replace_all clears", `{"a":1}`, `{"replace_all":{}}`, `{}`, false},
		{"merge sets and appends", `{"a":1,"b":2}`, `{"merge":{"d":4,"a":9,"c":3}}`, `{"a":9,"b":2,"d":4,"c":3}`, false},
		{"merge replaces whole values", `{"a":{"x":1,"y":2}}`, `{"merge":{"a":{"z":3}}}`, `{"a":{"z":3}}`, false},
		{"merge null is a value", `{"a":1}`, `{"merge":{"a":null}}`, `{"a":null}`, false},
		{"remove", `{"a":1,"b":2,"c":3}`, `{"remove":["b","zz"]}`, `{"a":1,"c":3}`, false},
		{"merge and remove", `{"a":1,"b":2}`, `{"merge":{"c":3},"remove":["a"]}`, `{"b":2,"c":3}`, false},
		{"numbers kept", `{"a":1}`, `{"merge":{"n":1.10,"big":1e400,"z":-0,"i":12345678901234567890}}`, `{"a":1,"n":1.10,"big":1e400,"z":-0,"i":12345678901234567890}`, false},
		{"replace_all numbers kept", `{}`, `{"replace_all":{"n":1.10,"l":[-0,1E2]}}`, `{"n":1.10,"l":[-0,1E2]}`, false},
		{"unchanged merge", `{"a":1,"b":[1,2]}`, `{"merge":{"a":1}}`, `{"a":1,"b":[1,2]}`, true},
		{"unchanged remove", `{"a":1}`, `{"remove":["nope"]}`, `{"a":1}`, true},
		{"unchanged replace_all", `{}`, `{"replace_all":{}}`, `{}`, true},
		{"1.0 is 1", `{"a":1}`, `{"merge":{"a":1.0}}`, `{"a":1}`, true},
		{"1e2 is 100", `{"a":100}`, `{"merge":{"a":1e2}}`, `{"a":100}`, true},
		{"keys reordered", `{"a":1,"b":{"x":1,"y":2}}`, `{"replace_all":{"b":{"y":2.0,"x":1},"a":1}}`, `{"a":1,"b":{"x":1,"y":2}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPruneFixture(t)
			f.liveWithExtra(tc.before)
			beforeBytes, _ := os.ReadFile(f.sesshinPath(uuidA))
			old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			if err := os.Chtimes(f.sesshinPath(uuidA), old, old); err != nil {
				t.Fatal(err)
			}
			beforeLife, _ := os.ReadFile(filepath.Join(f.loc.SessionDir(uuidA), "lifecycle.json"))

			out := f.updated("1", tc.change)
			wantChanged := []string{"extra"}
			if tc.stays {
				wantChanged = []string{}
			}
			if !slices.Equal(out.Changed, wantChanged) {
				t.Errorf("changed %v, want %v", out.Changed, wantChanged)
			}
			if got := f.stored(uuidA); got != tc.after {
				t.Errorf("stored extra %s, want %s", got, tc.after)
			}
			if got := viewExtra(t, out); got != tc.after {
				t.Errorf("view extra %s, want %s", got, tc.after)
			}
			afterBytes, _ := os.ReadFile(f.sesshinPath(uuidA))
			st, _ := os.Stat(f.sesshinPath(uuidA))
			if tc.stays && (!st.ModTime().Equal(old) || string(afterBytes) != string(beforeBytes)) {
				t.Errorf("the file was rewritten:\n%s", afterBytes)
			}
			// Every other key as read, and lifecycle.json untouched.
			if !tc.stays {
				strip := func(b []byte) string {
					head, _, _ := strings.Cut(string(b), `"extra":`)
					return head
				}
				if strip(afterBytes) != strip(beforeBytes) {
					t.Errorf("other keys changed:\n%s\n%s", beforeBytes, afterBytes)
				}
			}
			if afterLife, _ := os.ReadFile(filepath.Join(f.loc.SessionDir(uuidA), "lifecycle.json")); string(afterLife) != string(beforeLife) {
				t.Error("lifecycle.json changed")
			}
			if f.hook == nil {
				if left, _ := os.ReadDir(f.loc.SessionDir(uuidA)); len(left) != 2 {
					t.Errorf("session directory holds %d entries", len(left))
				}
			}
		})
	}
}

// update selects among every session, live, ended, and headless alike, by
// each selector form.
func TestUpdateSelectors(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshinExtra(uuidA, 1, `{}`)
	f.session(uuidC, time.Hour) // ended
	f.sesshinExtra(uuidC, 3, `{}`)
	f.running(uuidD, time.Minute, 12, func(l *model.LifecycleFile) { n := true; l.Nested = &n })
	f.sesshinExtra(uuidD, 4, `{}`)

	for i, sel := range []string{"1", uuidA, "0b6c5a3e-1f7e", "3", uuidC, "7d1e0000-0000-4000-8000-000000000003", "4"} {
		out := f.updated(sel, fmt.Sprintf(`{"merge":{"s":%d}}`, i))
		if len(out.Changed) != 1 {
			t.Errorf("%s: %+v", sel, out)
		}
	}
	// A job selects the live session holding it, else the one last seen.
	if out := f.updated("api", `{"merge":{"by":"job"}}`); out.Session.SessionID != uuidA && out.Session.SessionID != uuidC && out.Session.SessionID != uuidD {
		t.Errorf("%+v", out.Session)
	}
	// Nothing, and several.
	env := f.update("99", `{"merge":{"a":1}}`)
	wantKind(t, env, KindNotFound)
	env = f.update("7d1e0000", `{"merge":{"a":1}}`)
	wantKind(t, env, KindAmbiguous)
	if stored := f.stored(uuidC); strings.Contains(stored, `"a"`) {
		t.Error("an ambiguous selector wrote")
	}
}

// Operations.md, update, Errors: invalid-input, environment, not-found,
// ambiguous, busy, io, conflict, in that order.
func TestUpdateErrorOrder(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidC, time.Minute, 11)
	f.sesshinExtra(uuidC, 3, `{}`)
	f.running(uuidD, time.Minute, 12)
	f.sesshinExtra(uuidD, 4, `{}`)
	f.running(uuidA, time.Minute, 13)
	f.write(uuidA, "sesshin.json", []byte("{")) // conflict: unusable
	good := `{"merge":{"a":1}}`

	wantKind(t, f.update("#1", `{}`), KindInvalidInput)
	home := f.home
	f.home = ""
	wantKind(t, f.update("3", good), KindEnvironment)
	f.home = home
	wantKind(t, f.update("99", good), KindNotFound)
	wantKind(t, f.update("7d1e0000", good), KindAmbiguous)

	// busy: the session's lock is held.
	root, err := fsys.OS{}.OpenRoot(f.loc.SessionDir(uuidC))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	env := f.update("3", good)
	wantKind(t, env, KindBusy)
	if env.Error.Details["lock"] != "session" || env.Error.Details["session_id"] != uuidC {
		t.Errorf("details %+v", env.Error.Details)
	}
	lock.Unlock()
	root.Close()

	// io: sesshin.json can't be read.
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpReadFile && op.Path == "sesshin.json" && filepath.Base(op.Root) == uuidD {
			return syscall.EACCES
		}
		return nil
	}
	wantKind(t, f.update("4", good), KindIO)
	f.hook = nil

	// conflict, last: the file is unusable.
	wantKind(t, f.update("0b6c5a3e", good), KindConflict)
}

// A sesshin.json that can't be read is io, whether found while selecting or
// under the lock.
func TestUpdateIO(t *testing.T) {
	f := newPruneFixture(t)
	f.liveWithExtra(`{}`)
	reads := 0
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpReadFile && op.Path == "sesshin.json" && filepath.Base(op.Root) == uuidA {
			if reads++; reads == 2 { // selecting read it once
				return syscall.EACCES
			}
		}
		return nil
	}
	env := f.update("1", `{"merge":{"a":1}}`)
	wantKind(t, env, KindIO)
	if env.Error.Details["path"] != f.sesshinPath(uuidA) || env.Error.Details["code"] != "EACCES" {
		t.Errorf("details %+v", env.Error.Details)
	}
	if f.stored(uuidA) != `{}` {
		t.Error("written")
	}
	// A directory in its place is not readable either.
	g := newPruneFixture(t)
	g.running(uuidA, time.Minute, 11)
	if err := os.MkdirAll(g.sesshinPath(uuidA), 0o700); err != nil {
		t.Fatal(err)
	}
	if env := g.update(uuidA, `{"merge":{"a":1}}`); env.OK || (env.Error.Kind != KindIO && env.Error.Kind != KindConflict) {
		t.Errorf("%+v", env)
	}
}

// Operations.md, update, No sesshin.json to change: each row of the table
// has its file, its path, and a message that says what to do.
func TestUpdateNoSesshinFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(f *pruneFixture)
		file    string
		ended   bool     // the session has ended: the message says to resume it
		message []string // each is in the message
	}{
		{"missing, live", func(f *pruneFixture) { f.running(uuidA, time.Minute, 11) }, "missing", false,
			[]string{"session 0b6c5a3e has no sesshin.json yet", "it is live", "retry after its next prompt"}},
		{"missing, ended", func(f *pruneFixture) { f.session(uuidA, time.Hour) }, "missing", true,
			[]string{"session 0b6c5a3e has no sesshin.json", "sesshin resume " + uuidA}},
		{"unusable, live", func(f *pruneFixture) {
			f.running(uuidA, time.Minute, 11)
			f.write(uuidA, "sesshin.json", []byte("{"))
		}, "unusable", false, []string{"corrupt", "retry after its next prompt"}},
		{"unusable, ended", func(f *pruneFixture) {
			f.session(uuidA, time.Hour)
			f.write(uuidA, "sesshin.json", []byte("{"))
		}, "unusable", true, []string{"corrupt", "sesshin resume " + uuidA}},
		{"a newer format", func(f *pruneFixture) {
			f.running(uuidA, time.Minute, 11)
			f.write(uuidA, "sesshin.json", []byte(`{"schema":99}`))
		}, "other-format", false, []string{"format 99, newer", "upgrade sesshin"}},
		{"an older format, ended", func(f *pruneFixture) {
			f.session(uuidA, time.Hour)
			f.write(uuidA, "sesshin.json", []byte(`{"schema":1}`))
		}, "other-format", true, []string{"format 1, older", "sesshin migrate"}},
		{"pending, live", func(f *pruneFixture) {
			f.running(uuidA, time.Minute, 11)
			f.sesshinExtra(uuidA, 0, `{}`)
		}, "pending", false, []string{"no sesshin ID", "retry after its next prompt"}},
		{"pending, ended", func(f *pruneFixture) {
			f.session(uuidA, time.Hour)
			f.sesshinExtra(uuidA, 0, `{}`)
		}, "pending", true, []string{"no sesshin ID", "sesshin resume " + uuidA}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPruneFixture(t)
			tc.setup(f)
			env := f.update(uuidA, `{"merge":{"a":1}}`)
			wantKind(t, env, KindConflict)
			d := env.Error.Details
			if d["rule"] != "no-sesshin-file" || d["file"] != tc.file || d["path"] != f.sesshinPath(uuidA) {
				t.Errorf("details %+v", d)
			}
			refs := d["sessions"].([]SessionRef)
			if len(refs) != 1 || refs[0].SessionID != uuidA {
				t.Errorf("sessions %+v", refs)
			}
			for _, part := range tc.message {
				if !strings.Contains(env.Error.Message, part) {
					t.Errorf("message %q lacks %q", env.Error.Message, part)
				}
			}
			// Nothing was created.
			if tc.file == "missing" {
				if _, err := os.Stat(f.sesshinPath(uuidA)); !os.IsNotExist(err) {
					t.Errorf("sesshin.json: %v", err)
				}
			}
		})
	}
}

// The re-read under the lock decides: a file that turns unusable, or pending,
// between selecting and the lock is refused.
func TestUpdateRereadUnderLock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(f *pruneFixture)
		file  string
	}{
		{"turns unusable", func(f *pruneFixture) { f.write(uuidA, "sesshin.json", []byte("{")) }, "unusable"},
		{"removed", func(f *pruneFixture) { os.Remove(f.sesshinPath(uuidA)) }, "missing"},
		{"back to pending", func(f *pruneFixture) { f.sesshinExtra(uuidA, 0, `{}`) }, "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPruneFixture(t)
			f.liveWithExtra(`{}`)
			f.hook = func(op fsys.Op) error {
				if op.Name == fsys.OpLock && filepath.Base(op.Root) == uuidA {
					tc.write(f)
				}
				return nil
			}
			env := f.update("1", `{"merge":{"a":1}}`)
			wantKind(t, env, KindConflict)
			if env.Error.Details["file"] != tc.file {
				t.Errorf("details %+v", env.Error.Details)
			}
		})
	}
}

// A directory pruned while update waited for its lock is not-found; so is
// one already gone at the lock.
func TestUpdatePrunedWhileWaiting(t *testing.T) {
	f := newPruneFixture(t)
	f.liveWithExtra(`{}`)
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpLock && filepath.Base(op.Root) == uuidA {
			aside := filepath.Join(f.loc.SessionsDir(), ".removing-"+uuidA)
			if err := os.Rename(f.loc.SessionDir(uuidA), aside); err != nil {
				f.t.Fatal(err)
			}
		}
		return nil
	}
	env := f.update("1", `{"merge":{"a":1}}`)
	wantKind(t, env, KindNotFound)
	if left, _ := filepath.Glob(filepath.Join(f.loc.SessionsDir(), ".removing-*", "sesshin.json")); len(left) != 1 {
		t.Fatalf("aside: %v", left)
	} else if b, _ := os.ReadFile(left[0]); strings.Contains(string(b), `"a"`) {
		t.Error("wrote into the directory aside")
	}
}

// A result past extra's limits is conflict extra-too-large, and nothing is
// written.
func TestUpdateExtraTooLarge(t *testing.T) {
	f := newPruneFixture(t)
	pad := 65536 - len(`{"a":"","b":""}`)
	f.liveWithExtra(`{"a":"` + strings.Repeat("x", pad) + `"}`)
	before, _ := os.ReadFile(f.sesshinPath(uuidA))
	// Two keys that fit alone, past the limit together.
	env := f.update("1", `{"merge":{"b":"yy"}}`)
	wantKind(t, env, KindConflict)
	if env.Error.Details["rule"] != "extra-too-large" {
		t.Errorf("details %+v", env.Error.Details)
	}
	if refs := env.Error.Details["sessions"].([]SessionRef); len(refs) != 1 || refs[0].SessionID != uuidA {
		t.Errorf("sessions %+v", refs)
	}
	if after, _ := os.ReadFile(f.sesshinPath(uuidA)); string(after) != string(before) {
		t.Error("written")
	}
	// Exactly at the limit is fine.
	out := f.updated("1", `{"merge":{"b":""}}`)
	if len(out.Changed) != 1 {
		t.Errorf("%+v", out)
	}
	// Removing brings it back within.
	out = f.updated("1", `{"remove":["a"]}`)
	if viewExtra(t, out) != `{"b":""}` {
		t.Errorf("extra %s", viewExtra(t, out))
	}
}

// Warnings: a session file read while selecting that is unusable is reported,
// as list reports it.
func TestUpdateWarnings(t *testing.T) {
	f := newPruneFixture(t)
	f.liveWithExtra(`{}`)
	f.running(uuidC, time.Minute, 12)
	f.write(uuidC, "statusline.json", []byte("{"))
	env := f.update("1", `{"merge":{"a":1}}`)
	if !env.OK || len(env.Warnings) != 1 || env.Warnings[0].Kind != KindUnusableFile {
		t.Errorf("%+v", env)
	}
}

// update never takes the state lock, and takes the session lock once.
func TestUpdateLocks(t *testing.T) {
	f := newPruneFixture(t)
	f.liveWithExtra(`{}`)
	var locks []string
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpLock {
			locks = append(locks, filepath.Base(op.Root))
		}
		return nil
	}
	f.updated("1", `{"merge":{"a":1}}`)
	if !slices.Equal(locks, []string{uuidA}) {
		t.Errorf("locks %v", locks)
	}
}

// The selector self: the session whose pid and pid_started_at are those of
// the caller's Claude, its live session; it behaves as an exact ID in each
// operation's scope (operations.md, Selecting a session).
func TestSelectorSelf(t *testing.T) {
	setup := func() *pruneFixture {
		f := newPruneFixture(t)
		f.running(uuidA, time.Hour, 11)
		f.sesshinExtra(uuidA, 1, `{}`)
		f.running(uuidC, time.Minute, 12)
		f.sesshinExtra(uuidC, 3, `{}`)
		f.lookup = func(_ fsys.FS, claudePID string) proc.Claude {
			return proc.Claude{PID: 12, StartedAt: "s12"}
		}
		return f
	}

	f := setup()
	if got := f.shown("self"); got["session_id"] != uuidC {
		t.Errorf("show self: %v", got["session_id"])
	}
	if out := f.updated("self", `{"merge":{"a":1}}`); out.Session.SessionID != uuidC || f.stored(uuidC) != `{"a":1}` || f.stored(uuidA) != `{}` {
		t.Errorf("update self: %+v", out.Session)
	}

	// The CLAUDE_PID the caller has is handed to the lookup.
	var seen string
	f.lookup = func(_ fsys.FS, claudePID string) proc.Claude {
		seen = claudePID
		return proc.Claude{PID: 11, StartedAt: "s11"}
	}
	// (getenv gives HOME only: no CLAUDE_PID)
	if got := f.shown("self"); got["session_id"] != uuidA || seen != "" {
		t.Errorf("show self: %v, CLAUDE_PID %q", got["session_id"], seen)
	}

	// Outside any session: not-found. So is a process no session has, a
	// start time that differs, and an ended session.
	for name, c := range map[string]proc.Claude{
		"no claude":       {},
		"unknown process": {PID: 99, StartedAt: "s99"},
		"another start":   {PID: 12, StartedAt: "other"},
	} {
		f.lookup = func(fsys.FS, string) proc.Claude { return c }
		env := f.show("self", false)
		wantKind(t, env, KindNotFound)
		if env.Error.Details["sessions"].([]string)[0] != "self" {
			t.Errorf("%s: %+v", name, env.Error.Details)
		}
		wantKind(t, f.update("self", `{"merge":{"a":1}}`), KindNotFound)
	}
	f.lookup = nil
	wantKind(t, f.show("self", false), KindNotFound)

	// A superseded session of the same process is not its live session.
	g := newPruneFixture(t)
	g.running(uuidA, time.Hour, 11)
	g.running(uuidC, time.Minute, 11)
	g.lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{PID: 11, StartedAt: "s11"} }
	if got := g.shown("self"); got["session_id"] != uuidC {
		t.Errorf("superseded: show self is %v", got["session_id"])
	}

	// A job named self is job:self.
	h := newPruneFixture(t)
	h.running(uuidA, time.Minute, 11)
	h.sesshinWith(uuidA, 1, "self", "spawn")
	h.lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{} }
	wantKind(t, h.show("self", false), KindNotFound)
	if got := h.shown("job:self"); got["session_id"] != uuidA {
		t.Errorf("job:self: %v", got["session_id"])
	}
}

// With a null pid in lifecycle.json, self matches the pid and pid_started_at
// of statusline.json.
func TestSelectorSelfStatuslinePID(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Hour, 11)
	f.running(uuidC, time.Minute, 12, func(l *model.LifecycleFile) { l.PID, l.PIDStartedAt = nil, nil })
	f.writeStatusline(uuidC, f.now.Add(-time.Minute), 12, "s12")
	f.lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{PID: 12, StartedAt: "s12"} }
	if got := f.shown("self"); got["session_id"] != uuidC {
		t.Errorf("show self: %v", got["session_id"])
	}
	f.lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{PID: 12, StartedAt: "other"} }
	wantKind(t, f.show("self", false), KindNotFound)
}

// parseSelector: self first, before the job form it also matches; it is not
// a UUID prefix.
func TestParseSelectorSelf(t *testing.T) {
	sel, why := parseSelector("self")
	if why != "" || !sel.Self || sel.Job != "" || sel.Prefix != "" || sel.Raw != "self" {
		t.Errorf("%+v %q", sel, why)
	}
	sel, why = parseSelector("job:self")
	if why != "" || sel.Self || sel.Job != "self" {
		t.Errorf("%+v %q", sel, why)
	}
	for _, s := range []string{"Self", "self ", "selfie"} {
		if sel, _ := parseSelector(s); sel.Self {
			t.Errorf("%q is self", s)
		}
	}
}
