package ops

import (
	"encoding/json"
	"testing"

	"github.com/phansen314/sesshin/internal/schematest"
)

// checkEnvelope validates an envelope a test produced against envelope, and
// its result against output (an operation's output schema) or its error
// against error, and each warning against warning (implementation-spec.md,
// Schemas in tests).
func checkEnvelope(t *testing.T, env Envelope, output string) {
	t.Helper()
	check := func(id string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if ok, f := schematest.Check(t, id, b); !ok {
			t.Errorf("%s rejects %s at %s", id, b, f)
		}
	}
	check("envelope", env)
	if env.OK {
		check(output, env.Result)
	} else {
		check("error", env.Error)
	}
	for _, w := range env.Warnings {
		check("warning", w)
	}
}
