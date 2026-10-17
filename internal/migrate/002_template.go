package migrate

import (
	"errors"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Migration 2, template: sesshin.json 2 -> 3 adds `template` as null after
// `extra`. state.json is not covered: its schema 2 is current. A reservation
// has no step, since it lives minutes (design-spec.md, Migrations).
var step002 = Step{
	N:    2,
	Name: "template",
	Files: map[Kind]FileStep{
		Sesshin: {From: 2, Apply: sesshinTemplate},
	},
}

func sesshinTemplate(o *jsonio.Object) (*jsonio.Object, error) {
	if _, ok := o.Get("template"); ok {
		return nil, errors.New("/template: already present in a schema 2 file")
	}
	if err := requireKeys(o, "2", "extra"); err != nil {
		return nil, err
	}
	o.Set("schema", jsonNumber("3"))
	insertAfter(o, "extra", "template", nil)
	return o, nil
}
