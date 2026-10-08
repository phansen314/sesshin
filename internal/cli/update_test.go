package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/ops"
)

// updateEcho is the real update command with its operation replaced by one
// that echoes the input the command line made of it.
func updateEcho(t *testing.T) []Command {
	t.Helper()
	for _, c := range commands {
		if c.Name != "update" {
			continue
		}
		c.Run = operation(ops.DecodeUpdateInput, func(in ops.UpdateInput, _ Env) ops.Envelope {
			text := func(o *jsonio.Object) any {
				if o == nil {
					return nil
				}
				b, err := jsonio.MarshalLine(o)
				if err != nil {
					t.Fatal(err)
				}
				return strings.TrimSuffix(string(b), "\n")
			}
			return ops.Succeeded(map[string]any{
				"raw": in.Selector.Raw, "self": in.Selector.Self,
				"merge": text(in.Extra.Merge), "remove": in.Extra.Remove, "replace_all": text(in.Extra.ReplaceAll),
			})
		})
		return []Command{c}
	}
	t.Fatal("no update command")
	return nil
}

func runUpdate(t *testing.T, stdin string, args ...string) argResult {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{
		Stdin:     strings.NewReader(stdin),
		Stdout:    &out,
		Stderr:    &errOut,
		BuildInfo: func() buildinfo.Info { return buildinfo.Info{Version: buildinfo.Devel, Go: "go1.26.8"} },
	}
	o, code, note := execute(updateEcho(t), args, env)
	deliver(env, o, code, note)
	var r argResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// cli-spec, update: --extra-merge and --extra-replace-all are JSON values,
// --extra-remove is repeatable with one key per occurrence, commas included.
func TestUpdateOptions(t *testing.T) {
	r := runUpdate(t, "", "update", "self", "--extra-merge", `{"ticket": "auth-3", "n": 1.10}`, "--extra-remove", "status", "--extra-remove", "a,b")
	if !r.OK || r.Result["raw"] != "self" || r.Result["self"] != true ||
		r.Result["merge"] != `{"ticket":"auth-3","n":1.10}` || !reflect.DeepEqual(r.Result["remove"], []any{"status", "a,b"}) || r.Result["replace_all"] != nil {
		t.Errorf("%+v", r)
	}
	r = runUpdate(t, "", "update", "12", "--extra-replace-all", `{}`)
	if !r.OK || r.Result["replace_all"] != `{}` || r.Result["merge"] != nil {
		t.Errorf("%+v", r)
	}
	r = runUpdate(t, `{"session":"api","extra":{"remove":["x"]}}`, "update", "-i", "-")
	if !r.OK || !reflect.DeepEqual(r.Result["remove"], []any{"x"}) {
		t.Errorf("%+v", r)
	}
}

// With none of the three options the extra is empty, which the operation
// refuses; replace-all with another is the operation's invalid-input too.
func TestUpdateInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		field string
	}{
		{"no option", []string{"update", "12"}, "/extra"},
		{"replace-all with merge", []string{"update", "12", "--extra-replace-all", "{}", "--extra-merge", `{"a":1}`}, "/extra/merge"},
		{"replace-all with remove", []string{"update", "12", "--extra-replace-all", "{}", "--extra-remove", "a"}, "/extra/remove"},
		{"merge and remove share a key", []string{"update", "12", "--extra-merge", `{"a":1}`, "--extra-remove", "a"}, "/extra/remove/0"},
		{"empty merge", []string{"update", "12", "--extra-merge", `{}`}, "/extra/merge"},
		{"merge not an object", []string{"update", "12", "--extra-merge", `[1]`}, "/extra/merge"},
		{"merge not JSON", []string{"update", "12", "--extra-merge", `{"a":`}, "/extra/merge"},
		{"repeated key", []string{"update", "12", "--extra-merge", `{"a":1,"a":2}`}, "/extra/merge/a"},
		{"replace-all not an object", []string{"update", "12", "--extra-replace-all", `null`}, "/extra/replace_all"},
		{"repeated remove", []string{"update", "12", "--extra-remove", "a", "--extra-remove", "a"}, "/extra/remove/1"},
		{"a bad session", []string{"update", "#12", "--extra-merge", `{"a":1}`}, "/session"},
	} {
		r := runUpdate(t, "", tc.args...)
		if r.OK || r.Error.Kind != "invalid-input" || !slices.Contains(problemFields(r), tc.field) {
			t.Errorf("%s: %+v", tc.name, r)
		}
	}
	// The session is required unless --input is given.
	if r := runUpdate(t, "", "update", "--extra-merge", `{"a":1}`); r.OK || r.Error.Kind != "usage" {
		t.Errorf("without a session: %+v", r)
	}
}
