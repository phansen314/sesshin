package ops

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
	"github.com/phansen314/sesshin/internal/schematest"
)

// Operations.md, spawn: extra is an object within extra's limits, measured as
// the compact JSON SESSHIN_EXTRA will hold, with no NUL; its numbers are kept
// as given.
func TestSpawnExtraChecks(t *testing.T) {
	deep := func(n int) string { return strings.Repeat(`{"a":`, n-1) + `{}` + strings.Repeat("}", n-1) }
	val := func(n int) string { return `{"k":"` + strings.Repeat("x", n) + `"}` }
	exact := 65536 - len(`{"k":""}`)
	for _, tc := range []struct {
		extra  string
		field  string // "": accepted
		beyond bool   // a rule the schema doesn't state
	}{
		{`{}`, "", false},
		{`{"koan-task":57,"r":1.10,"big":1e400,"n":-0,"l":[1.0,{"a":null}]}`, "", false},
		{deep(32), "", false},
		{deep(33), "/extra", true},
		{val(exact), "", false},
		{val(exact + 1), "/extra", true},
		{`[]`, "/extra", false},
		{`null`, "/extra", false},
		{`"x"`, "/extra", false},
		{`{"a":"x\u0000"}`, "/extra/a", true},
		{`{"a\u0000":1}`, "/extra/a\u0000", true},
		{`{"a":["x\u0000"]}`, "/extra/a/0", true},
	} {
		in := `{"cwd":"/w","extra":` + tc.extra + `}`
		_, e := DecodeInput([]byte(in), DecodeSpawnInput)
		ok := e == nil
		if ok != (tc.field == "") {
			t.Errorf("%.80s: error %+v, want field %q", tc.extra, e, tc.field)
		} else if !ok {
			if ps := e.Details["problems"].([]model.Problem); ps[0].Field != tc.field {
				t.Errorf("%.80s: problems %+v, want field %q", tc.extra, ps, tc.field)
			}
		}
		if schemaOK, _ := schematest.Check(t, "spawn-input", []byte(in)); schemaOK != (tc.field == "" || tc.beyond) {
			t.Errorf("%.80s: the schema says %v", tc.extra, schemaOK)
		}
	}
}

// SESSHIN_EXTRA is the third variable of the launch, compact with the key
// order and number text as given, set with or without a job. Without an extra
// it is not set.
func TestSpawnExtraEnv(t *testing.T) {
	const extra = `{"z":1,"koan-task":57,"r":1.10,"big":1e400,"s":"é<&>","n":{"a":[-0]}}`
	f := newSpawnFixture(t)
	f.spawned(`"extra":{ "z" : 1, "koan-task":57, "r":1.10, "big":1e400, "s":"é<&>", "n":{"a":[-0]} }`, `"start_timeout_secs":0`)
	if got := f.launches[0].Env; !reflect.DeepEqual(got, []kitty.Var{{Name: "SESSHIN_EXTRA", Value: extra}}) {
		t.Errorf("without a job: env %+v", got)
	}
	f = newSpawnFixture(t)
	f.spawned(`"job":"api"`, `"extra":{}`, `"start_timeout_secs":0`)
	want := []kitty.Var{{Name: "SESSHIN_JOB", Value: "api"}, {Name: "SESSHIN_TOKEN", Value: token(1)}, {Name: "SESSHIN_EXTRA", Value: "{}"}}
	if got := f.launches[0].Env; !reflect.DeepEqual(got, want) {
		t.Errorf("with a job: env %+v", got)
	}
	f = newSpawnFixture(t)
	f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
	for _, v := range f.launches[0].Env {
		if v.Name == "SESSHIN_EXTRA" {
			t.Errorf("SESSHIN_EXTRA set without an extra: %+v", v)
		}
	}
}
