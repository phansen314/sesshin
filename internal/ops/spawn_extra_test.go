package ops

import (
	"reflect"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/schematest"
)

// Operations.md, spawn: extra is an object within extra's limits, measured as
// compact JSON; its numbers are kept as given. A NUL is not special: the
// extra travels in a file, not the environment.
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
		{`{"ticket":"auth-3","r":1.10,"big":1e400,"n":-0,"l":[1.0,{"a":null}]}`, "", false},
		{deep(32), "", false},
		{deep(33), "/extra", true},
		{val(exact), "", false},
		{val(exact + 1), "/extra", true},
		{`[]`, "/extra", false},
		{`null`, "/extra", false},
		{`"x"`, "/extra", false},
		{`{"a":"x\u0000"}`, "", false},
		{`{"a\u0000":1}`, "", false},
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

// The extra goes into the reservation, compact with the key order and number
// text as given, and never into the environment: SESSHIN_TOKEN is always
// set, SESSHIN_JOB with a job, and there is no SESSHIN_EXTRA.
func TestSpawnExtraInReservation(t *testing.T) {
	const extra = `{"z":1,"ticket":"auth-3","r":1.10,"big":1e400,"s":"é<&>","n":{"a":[-0]}}`
	member := `"extra":{ "z" : 1, "ticket":"auth-3", "r":1.10, "big":1e400, "s":"é<&>", "n":{"a":[-0]} }`

	f := newSpawnFixture(t)
	f.spawned(member, `"start_timeout_secs":0`)
	if got := f.launches[0].Env; !reflect.DeepEqual(got, []placement.Var{{Name: "SESSHIN_TOKEN", Value: token(1)}}) {
		t.Errorf("without a job: env %+v", got)
	}
	r, ok := f.reservationOf("", token(1))
	if !ok || enc(t, r.Extra) != extra {
		t.Errorf("without a job: reservation %+v", r)
	}

	f = newSpawnFixture(t)
	f.spawned(`"job":"api"`, member, `"start_timeout_secs":0`)
	want := []placement.Var{{Name: "SESSHIN_JOB", Value: "api"}, {Name: "SESSHIN_TOKEN", Value: token(1)}}
	if got := f.launches[0].Env; !reflect.DeepEqual(got, want) {
		t.Errorf("with a job: env %+v", got)
	}
	r, ok = f.reservationOf("api", token(1))
	if !ok || enc(t, r.Extra) != extra {
		t.Errorf("with a job: reservation %+v", r)
	}

	// Without an extra it is {}.
	f = newSpawnFixture(t)
	f.spawned(`"job":"api"`, `"start_timeout_secs":0`)
	r, _ = f.reservationOf("api", token(1))
	if enc(t, r.Extra) != "{}" {
		t.Errorf("extra %s", enc(t, r.Extra))
	}
	// A size within the limit survives the file, which is read back by the
	// strict reader with the same limits.
	f = newSpawnFixture(t)
	big := `"extra":{"k":"` + strings.Repeat("x", 65536-len(`{"k":""}`)) + `"}`
	f.spawned(big, `"start_timeout_secs":0`)
	if _, ok := f.reservationOf("", token(1)); !ok {
		t.Error("a reservation holding an extra at the limit is unusable")
	}
}
