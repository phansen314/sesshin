package model

import (
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/schematest"
)

// verdict is a validator's outcome for one document. Early marks a file
// rejected before its fields were checked (at its schema field), so only that
// field is compared.
type verdict struct {
	ok    bool
	at    []string
	early bool
}

// agree runs doc through the schema library and the validator: both must
// accept, or both reject at exactly the same fields (Failure.Matches).
func agree(t *testing.T, schemaID, doc string, validator func(string) verdict) {
	t.Helper()
	libOK, lib := schematest.Check(t, schemaID, []byte(doc))
	v := validator(doc)
	switch {
	case libOK != v.ok:
		t.Errorf("%s: schema accepts %v (%s), validator accepts %v (%q)\n  %s", schemaID, libOK, lib, v.ok, v.at, doc)
	case v.early && !slices.Contains(lib.Fields, "/schema"):
		t.Errorf("%s: validator stops at /schema, schema rejects at %s\n  %s", schemaID, lib, doc)
	case !libOK && !v.early && !lib.Matches(v.at):
		t.Errorf("%s: schema rejects at %s, validator at %q\n  %s", schemaID, lib, v.at, doc)
	}
}

// fileVerdict is where a validator rejected, by the file's schema alone: a
// file failing early fails at its schema field; one failing only rules beyond
// the schema (SchemaProblems empty) is, by the schema, valid.
func fileVerdict(r FileResult) verdict {
	switch {
	case r.Early:
		return verdict{at: []string{"/schema"}, early: true}
	case len(r.SchemaProblems) == 0:
		return verdict{ok: true}
	}
	var at []string
	for _, p := range r.SchemaProblems {
		at = append(at, p.Field)
	}
	return verdict{at: at}
}

// variants returns base with each pair's first text replaced by its second,
// in turn: the same file in another valid state.
func variants(t *testing.T, base string, edits ...[][2]string) []string {
	t.Helper()
	var out []string
	for _, e := range edits {
		doc := base
		for _, r := range e {
			if !strings.Contains(doc, r[0]) {
				t.Fatalf("no %s to replace", r[0])
			}
			doc = strings.Replace(doc, r[0], r[1], 1)
		}
		out = append(out, doc)
	}
	return out
}

// corpus is every mutation of each base, plus extra documents.
func corpus(t *testing.T, bases []string, extra ...string) []string {
	t.Helper()
	var docs []string
	for _, b := range bases {
		docs = append(docs, schematest.Mutations(t, b)...)
	}
	return append(docs, extra...)
}

// Documents every file's validator is given whole, beside the mutations.
var commonExtra = []string{`{}`, `{"schema": 1}`, `{"schema": 2}`, `{"schema": "1"}`, `{"schema": null}`, `{"schema": 0}`}

func TestStateAgreesWithSchema(t *testing.T) {
	validator := func(doc string) verdict {
		_, r := ReadState([]byte(doc))
		return fileVerdict(r)
	}
	docs := corpus(t, []string{fixture(t, "state.json")}, commonExtra...)
	for _, doc := range docs {
		agree(t, "state-file", doc, validator)
	}
	t.Logf("%d documents", len(docs))
}

func TestLifecycleAgreesWithSchema(t *testing.T) {
	validator := func(doc string) verdict {
		// The directory is the file's own session_id, when it has one: a
		// mismatch is outside what the schema expresses.
		dir := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		if obj, _, err := jsonio.ParseObject([]byte(doc)); err == nil {
			if s, ok := obj.Get("session_id"); ok {
				if s, ok := s.(string); ok {
					dir = s
				}
			}
		}
		_, r := ReadLifecycle([]byte(doc), dir)
		return fileVerdict(r)
	}
	base := fixture(t, "lifecycle.json")
	bases := append([]string{base}, variants(t, base,
		// A new record: every nullable field null.
		[][2]string{
			{`"cwd": "/home/phansen/code/sesshin"`, `"cwd": null`},
			{`"transcript_path": "/home/phansen/.claude/projects/-home-phansen-code-sesshin/3fa85f64-5717-4562-b3fc-2c963f66afa6.jsonl"`, `"transcript_path": null`},
			{`"session_title": "api review"`, `"session_title": null`},
			{`"model": "claude-opus-5-5"`, `"model": null`},
			{`"permission_mode": "acceptEdits"`, `"permission_mode": null`},
			{`"pid": 554338`, `"pid": null`},
			{`"pid_started_at": "linux:0f6c2a8e-9d1b-4c3a-8e7f-5b2d1a0c9e84:18446744"`, `"pid_started_at": null`},
			{`"entrypoint": "cli"`, `"entrypoint": null`},
			{`"nested": false`, `"nested": null`},
			{`"background_tasks": 0`, `"background_tasks": null`},
			{`"session_crons": 0`, `"session_crons": null`},
			{`"ended_prompt_id": "9b2e4c1a-7f3d-4e8b-a6c5-0d1f2e3a4b5c"`, `"ended_prompt_id": null`},
		},
		// Ended, after a failed turn.
		[][2]string{
			{`"stall_reason": null`, `"stall_reason": "rate_limit"`},
			{`"last_event_type": "stop"`, `"last_event_type": "end:prompt_input_exit"`},
			{`"ended_at": null`, `"ended_at": "2026-10-03T18:40:00Z"`},
			{`"end_reason": null`, `"end_reason": "prompt_input_exit"`},
		},
	)...)
	docs := corpus(t, bases, commonExtra...)
	for _, doc := range docs {
		agree(t, "lifecycle-file", doc, validator)
	}
	t.Logf("%d documents", len(docs))
}

func TestStatuslineAgreesWithSchema(t *testing.T) {
	validator := func(doc string) verdict {
		_, r := ReadStatusline([]byte(doc))
		return fileVerdict(r)
	}
	base := fixture(t, "statusline.json")
	bases := append([]string{base}, variants(t, base,
		[][2]string{
			{`"git_branch": "main"`, `"git_branch": null`},
			{`"cost_sample": {
    "at": "2026-10-03T18:28:40Z",
    "usd": 4.66012
  }`, `"cost_sample": null`},
			{`"burn_usd_per_hour": 3.958125`, `"burn_usd_per_hour": null`},
			{`"pid": 554338`, `"pid": null`},
			{`"pid_started_at": "linux:0f6c2a8e-9d1b-4c3a-8e7f-5b2d1a0c9e84:18446744"`, `"pid_started_at": null`},
		},
	)...)
	docs := corpus(t, bases, commonExtra...)
	for _, doc := range docs {
		agree(t, "statusline-file", doc, validator)
	}
	t.Logf("%d documents", len(docs))
}

func TestSesshinAgreesWithSchema(t *testing.T) {
	validator := func(doc string) verdict {
		_, r := ReadSesshin([]byte(doc))
		return fileVerdict(r)
	}
	base := fixture(t, "sesshin.json")
	bases := []string{
		base,
		`{"schema": 1, "id": null, "job": null, "source": "hook", "placement": null}`,
		`{"schema": 1, "id": 3, "job": "a", "source": "hook", "placement": null}`,
	}
	docs := corpus(t, bases, commonExtra...)
	for _, doc := range docs {
		agree(t, "sesshin-file", doc, validator)
	}
	t.Logf("%d documents", len(docs))
}

func TestReservationAgreesWithSchema(t *testing.T) {
	validator := func(doc string) verdict {
		// The file is named for its own job, when it has one: a mismatch is
		// outside what the schema expresses.
		job := "api-review"
		if obj, _, err := jsonio.ParseObject([]byte(doc)); err == nil {
			if s, ok := obj.Get("job"); ok {
				if s, ok := s.(string); ok {
					job = s
				}
			}
		}
		_, r := ReadReservation([]byte(doc), job)
		return fileVerdict(r)
	}
	base := fixture(t, "reservation.json")
	bases := append([]string{base}, variants(t, base,
		[][2]string{{`"job": "api-review"`, `"job": "a"`}},
		[][2]string{{`"job": "api-review"`, `"job": "a1-2b"`}},
		[][2]string{{`"job": "api-review"`, `"job": "1-2"`}},
		[][2]string{{`"job": "api-review"`, `"job": "12"`}},
		// A reservation not yet launched.
		[][2]string{{`"placement": {
    "terminal": "kitty",
    "socket": "unix:/tmp/kitty-{kitty.pid}-4099",
    "window_id": 7
  }`, `"placement": null`}},
	)...)
	docs := corpus(t, bases, commonExtra...)
	for _, doc := range docs {
		agree(t, "reservation-file", doc, validator)
	}
	t.Logf("%d documents", len(docs))
}

func TestInstallAgreesWithSchema(t *testing.T) {
	validator := func(doc string) verdict {
		_, r := ReadInstall([]byte(doc))
		return fileVerdict(r)
	}
	docs := corpus(t, []string{fixture(t, "install.json")}, commonExtra...)
	for _, doc := range docs {
		agree(t, "install-file", doc, validator)
	}
	t.Logf("%d documents", len(docs))
}
