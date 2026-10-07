package migrate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/model"
)

func TestStepChain(t *testing.T) {
	for i, s := range Steps {
		if s.N != i+1 {
			t.Fatalf("Steps[%d].N = %d, want %d", i, s.N, i+1)
		}
	}
	if got := Steps[len(Steps)-1].N; got != model.LatestMigration {
		t.Errorf("last step %d, model.LatestMigration %d", got, model.LatestMigration)
	}
	for _, kind := range []Kind{State, Sesshin} {
		current, _ := Current(kind)
		var froms []int64
		for _, s := range Steps {
			if fs, ok := s.Files[kind]; ok {
				froms = append(froms, fs.From)
			}
		}
		if len(froms) == 0 {
			t.Fatalf("%s: no steps", kind)
		}
		for i := 1; i < len(froms); i++ {
			if froms[i] != froms[i-1]+1 {
				t.Errorf("%s: From %v is not a chain", kind, froms)
			}
		}
		if last := froms[len(froms)-1]; last != current-1 {
			t.Errorf("%s: chain ends at %d, want %d", kind, last+1, current)
		}
	}
}

func TestFixture001(t *testing.T) {
	dir := "testdata/001-extra"
	work := filepath.Join(t.TempDir(), "state")
	copyTree(t, filepath.Join(dir, "before"), work)

	run := func() (changed []string, errs map[string]*Error) {
		errs = map[string]*Error{}
		filepath.WalkDir(work, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			var kind Kind
			switch d.Name() {
			case "state.json":
				kind = State
			case "sesshin.json":
				kind = Sesshin
			default:
				return nil
			}
			rel, _ := filepath.Rel(work, p)
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			out, ch, err := Convert(kind, data)
			if err != nil {
				var e *Error
				if !errors.As(err, &e) {
					t.Fatalf("%s: %v is not *Error", rel, err)
				}
				errs[rel] = e
				return nil
			}
			if ch {
				changed = append(changed, rel)
				if err := os.WriteFile(p, out, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		})
		return
	}

	changed, errs := run()
	if len(changed) != 4 {
		t.Errorf("changed %v, want state.json and 3 sessions", changed)
	}
	want := map[string]ErrorKind{
		"sessions/55555555-5555-4555-8555-555555555555/sesshin.json": Corrupt,
		"sessions/66666666-6666-4666-8666-666666666666/sesshin.json": Unconverted,
		"sessions/77777777-7777-4777-8777-777777777777/sesshin.json": Newer,
	}
	if len(errs) != len(want) {
		t.Errorf("errors %v", errs)
	}
	for p, k := range want {
		if e := errs[p]; e == nil || e.Kind != k {
			t.Errorf("%s: %v, want kind %d", p, e, k)
		}
	}
	if e := errs["sessions/66666666-6666-4666-8666-666666666666/sesshin.json"]; e != nil && !strings.Contains(e.Detail, "cwd") {
		t.Errorf("unconverted detail %q does not name cwd", e.Detail)
	}

	compareTree(t, work, filepath.Join(dir, "after"))

	changed, errs = run()
	if len(changed) != 0 {
		t.Errorf("second pass changed %v", changed)
	}
	if len(errs) != len(want) {
		t.Errorf("second pass errors %v", errs)
	}
	compareTree(t, work, filepath.Join(dir, "after"))
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func compareTree(t *testing.T, got, want string) {
	t.Helper()
	files := func(root string) map[string]string {
		m := map[string]string{}
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(p)
				rel, _ := filepath.Rel(root, p)
				m[rel] = string(b)
			}
			return nil
		})
		return m
	}
	g, w := files(got), files(want)
	for p, wb := range w {
		if gb, ok := g[p]; !ok {
			t.Errorf("%s missing", p)
		} else if gb != wb {
			t.Errorf("%s differs:\n got %q\nwant %q", p, gb, wb)
		}
	}
	for p := range g {
		if _, ok := w[p]; !ok {
			t.Errorf("%s unexpected", p)
		}
	}
}

func convertErr(t *testing.T, kind Kind, in string, k ErrorKind) *Error {
	t.Helper()
	_, ch, err := Convert(kind, []byte(in))
	var e *Error
	if ch || !errors.As(err, &e) || e.Kind != k {
		t.Fatalf("Convert(%q) = changed %v, err %v; want kind %d", in, ch, err, k)
	}
	return e
}

func TestConvertSesshinOrderAndContent(t *testing.T) {
	in := `{"job":null,"schema":1,"source":"hook","id":5,"placement":{"terminal":"kitty","user_vars":{"n":1.50,"big":12345678901234567890,"s":"A"}}}`
	out, ch, err := Convert(Sesshin, []byte(in))
	if err != nil || !ch {
		t.Fatalf("%v %v", ch, err)
	}
	// Order is the input's, with extra after placement; schema is set in place.
	want := `{
  "job": null,
  "schema": 2,
  "source": "hook",
  "id": 5,
  "placement": {
    "terminal": "kitty",
    "user_vars": {
      "n": 1.50,
      "big": 12345678901234567890,
      "s": "A"
    }
  },
  "extra": {}
}
`
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestConvertState(t *testing.T) {
	out, ch, err := Convert(State, []byte(`{"schema":1,"last_id":7}`))
	if err != nil || !ch {
		t.Fatal(ch, err)
	}
	if want := "{\n  \"schema\": 2,\n  \"last_id\": 7,\n  \"migration\": 0\n}\n"; string(out) != want {
		t.Errorf("got %q", out)
	}
}

func TestConvertRefusals(t *testing.T) {
	good := `"id":1,"job":null,"source":"hook","placement":null`
	e := convertErr(t, Sesshin, `{"schema":1,`+good+`,"extra":{}}`, Unconverted)
	if !strings.Contains(e.Detail, "extra") {
		t.Errorf("detail %q", e.Detail)
	}
	e = convertErr(t, Sesshin, `{"schema":1,"id":1,"job":null,"source":"hook"}`, Unconverted)
	if !strings.Contains(e.Detail, "placement") {
		t.Errorf("detail %q", e.Detail)
	}
	convertErr(t, Sesshin, `{"schema":1,`+good+`,"x":1}`, Unconverted)
	convertErr(t, Sesshin, `{"schema":1,"id":"x","job":null,"source":"hook","placement":null}`, Unconverted)
	convertErr(t, Sesshin, `{"schema":0,`+good+`}`, Unconverted)
	convertErr(t, State, `{"schema":1,"last_id":1,"migration":1}`, Unconverted)
	convertErr(t, State, `{"schema":1}`, Unconverted)
	e = convertErr(t, Sesshin, `{"schema":3,`+good+`}`, Newer)
	if e.Found != 3 {
		t.Errorf("found %d", e.Found)
	}
}

func TestConvertCorrupt(t *testing.T) {
	for _, in := range []string{``, `garbage`, `[]`, `{}`, `{"schema":"1"}`, `{"schema":1.0}`, `{"schema":1e0}`,
		`{"schema":1,"schema":2}`, `{"schema":99999999999999999999}`} {
		convertErr(t, State, in, Corrupt)
	}
}

func TestConvertCurrentUnchanged(t *testing.T) {
	// Even an invalid file at the current schema is not this package's to judge.
	out, ch, err := Convert(Sesshin, []byte(`{"schema":2,"bogus":true}`))
	if out != nil || ch || err != nil {
		t.Errorf("%q %v %v", out, ch, err)
	}
	if out, ch, err := Convert("lifecycle", []byte(`{"schema":1}`)); out != nil || ch || err != nil {
		t.Errorf("%q %v %v", out, ch, err)
	}
}
