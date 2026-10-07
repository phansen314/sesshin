package migrate

import (
	"errors"
	"slices"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// Migration 1, extra: sesshin.json 1 -> 2 adds `extra` as {} after
// `placement`; state.json 1 -> 2 adds `migration` after `last_id`.
var step001 = Step{
	N:    1,
	Name: "extra",
	Files: map[Kind]FileStep{
		Sesshin: {From: 1, Apply: sesshinExtra},
		State:   {From: 1, Apply: stateMigration},
	},
}

func sesshinExtra(o *jsonio.Object) (*jsonio.Object, error) {
	if _, ok := o.Get("extra"); ok {
		return nil, errors.New("/extra: already present in a schema 1 file")
	}
	if err := requireKeys(o, "id", "job", "source", "placement"); err != nil {
		return nil, err
	}
	o.Set("schema", jsonNumber("2"))
	insertAfter(o, "placement", "extra", &jsonio.Object{})
	return o, nil
}

// stateMigration writes `migration` as 0: the step has no way to know the run
// it belongs to. ops.Migrate sets it to the latest step under the state lock
// once every session is converted, so the 0 is never what a finished run
// leaves.
func stateMigration(o *jsonio.Object) (*jsonio.Object, error) {
	if _, ok := o.Get("migration"); ok {
		return nil, errors.New("/migration: already present in a schema 1 file")
	}
	if err := requireKeys(o, "last_id"); err != nil {
		return nil, err
	}
	o.Set("schema", jsonNumber("2"))
	insertAfter(o, "last_id", "migration", jsonNumber("0"))
	return o, nil
}

func requireKeys(o *jsonio.Object, keys ...string) error {
	for _, k := range keys {
		if _, ok := o.Get(k); !ok {
			return errors.New("/" + k + ": required in a schema 1 file")
		}
	}
	return nil
}

// insertAfter inserts the member key right after the first member named
// after, which must exist.
func insertAfter(o *jsonio.Object, after, key string, value any) {
	i := slices.IndexFunc(o.Members, func(m jsonio.Member) bool { return m.Key == after })
	m := slices.Clone(o.Members[:i+1])
	m = append(m, jsonio.Member{Key: key, Value: value})
	o.Members = append(m, o.Members[i+1:]...)
}
