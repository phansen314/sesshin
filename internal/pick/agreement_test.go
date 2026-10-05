package pick

import (
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/schematest"
)

// The decoder and restart-input agree on every mutation of a valid input,
// but for an argument holding a NUL, a rule beyond the schema, as resume's.
func TestInputAgreesWithSchema(t *testing.T) {
	base := `{"query": "killed", "args": ["--model", "opus"]}`
	docs := append(schematest.Mutations(t, base), base, `{}`, `[]`, `null`, `1`)
	for _, doc := range docs {
		_, e := ops.DecodeInput([]byte(doc), DecodeInput)
		ok, _ := schematest.Check(t, "restart-input", []byte(doc))
		switch {
		case e == nil && !ok:
			t.Errorf("the decoder accepts %s, the schema rejects it", doc)
		case e != nil && ok && !strings.Contains(doc, `\u0000`):
			t.Errorf("the decoder rejects %s (%v), the schema accepts it", doc, e.Details["problems"])
		}
	}
	t.Logf("%d documents", len(docs))
}
