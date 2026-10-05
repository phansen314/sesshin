package ops

import (
	"slices"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/schematest"
)

// agreeInput checks a hand-written input decoder against its published
// schema over every mutation of base and the extra documents: a document
// either accepts both or rejects both. beyond marks a document the decoder
// may reject though the schema accepts it, a rule the operation states
// beyond its schema (operations.md, Additional validation); nil for none.
func agreeInput[T any](t *testing.T, id, base string, decode func(*model.Fields, *model.Problems) T, beyond func(doc string) bool, extra ...string) {
	t.Helper()
	docs := append(schematest.Mutations(t, base), base, `{}`, `[]`, `null`, `1`)
	docs = append(docs, extra...)
	for _, doc := range docs {
		_, e := DecodeInput([]byte(doc), decode)
		ok, _ := schematest.Check(t, id, []byte(doc))
		switch {
		case e == nil && !ok:
			t.Errorf("%s: the decoder accepts %s, the schema rejects it", id, doc)
		case e != nil && ok && (beyond == nil || !beyond(doc)):
			t.Errorf("%s: the decoder rejects %s (%v), the schema accepts it", id, doc, e.Details["problems"])
		}
	}
	t.Logf("%s: %d documents", id, len(docs))
}

func TestDryRunInputsAgreeWithSchema(t *testing.T) {
	agreeInput(t, "install-input", `{"dry_run": true}`, DecodeInstallInput, nil)
	agreeInput(t, "uninstall-input", `{"dry_run": true}`, DecodeUninstallInput, nil)
}

func TestVersionInputAgreesWithSchema(t *testing.T) {
	agreeInput(t, "version-input", `{}`, DecodeVersionInput, nil, `{"x": 1}`)
}

func TestPruneInputAgreesWithSchema(t *testing.T) {
	agreeInput(t, "prune-input", `{"dry_run": true, "retain_days": 7}`, DecodePruneInput, nil)
}

func TestShowInputAgreesWithSchema(t *testing.T) {
	agreeInput(t, "show-input", `{"session": "12", "include_payload": true}`, DecodeShowInput, nil,
		`{"session": "a1b2c3d4"}`, `{"session": "api:deadbeef"}`, `{"session": "api"}`)
}

func TestListInputAgreesWithSchema(t *testing.T) {
	// The schema takes any string in fields; the decoder takes only a
	// session view field.
	beyond := func(doc string) bool {
		obj, _, err := jsonio.ParseObject([]byte(doc))
		if err != nil {
			return false
		}
		v, _ := obj.Get("fields")
		items, _ := v.([]any)
		for _, it := range items {
			if s, ok := it.(string); ok && !slices.Contains(viewFields, s) {
				return true
			}
		}
		return false
	}
	agreeInput(t, "list-input", `{"liveness": "all", "include_headless": true, "fields": ["name", "status"], "limit": 5}`, DecodeListInput, beyond,
		`{"fields": ["name", "name"]}`, `{"liveness": "live"}`, `{"liveness": "ended"}`, `{"limit": 0}`)
}
