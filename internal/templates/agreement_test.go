package templates

import (
	"testing"

	"github.com/phansen314/sesshin/internal/schematest"
)

// The validator and the template schema agree: each valid template, mutated
// one field at a time, is accepted by both or rejected by both at the same
// fields. The rules beyond the schema (a NUL, a vars key, a .. component, a
// repeated key) are left out.
func TestReadAgreesWithSchema(t *testing.T) {
	bases := []string{
		`{}`,
		`{"job":"api","cwd":"/srv/api","type":"split","name":"api","prompt":"hello","args":["--model","opus"],"vars":{"PROJECT":"api"},"start_timeout_secs":30,"description":"d"}`,
		`{"job":"Api-2","cwd":"~/code","type":"os-window","name":"n","prompt_file":"prompts/a.md","args":[],"vars":{},"start_timeout_secs":0}`,
		`{"type":"tab","prompt":""}`,
	}
	var docs []string
	for _, b := range bases {
		docs = append(docs, schematest.Mutations(t, b)...)
	}
	docs = append(docs, `{"prompt":"a","prompt_file":"b"}`, `[]`, `"x"`, `null`, `{"prompt_file":"/abs"}`, `{"prompt_file":""}`)
	for _, doc := range docs {
		libOK, lib := schematest.Check(t, "template", []byte(doc))
		_, p := read([]byte(doc))
		var at []string
		for _, pr := range p.SchemaList() {
			at = append(at, pr.Field)
		}
		switch ok := len(at) == 0; {
		case ok != libOK:
			t.Errorf("schema accepts %v (%s), validator accepts %v (%q)\n  %s", libOK, lib, ok, at, doc)
		case !libOK && !lib.Matches(at):
			t.Errorf("schema rejects at %s, validator at %q\n  %s", lib, at, doc)
		}
	}
	t.Logf("%d documents", len(docs))
}
