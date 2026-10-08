package ops

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// Operations.md, Session view: extra follows source, is the stored object
// with its key order and number text, and is null without a usable
// sesshin.json.
func TestViewExtra(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.write(uuidA, "sesshin.json", []byte(`{"schema": 2,"id":7,"job":null,"source":"hook","placement":null,"extra":{"z":1,"ticket":"auth-3","r":1.10,"big":1e30,"l":[-0]}}`))
	f.running(uuidB, time.Minute, 12) // no sesshin.json

	_, l := f.list(`{"liveness":"all"}`)
	byID := map[string]int{}
	for i, u := range l.uuids() {
		byID[u] = i
	}
	a, b := l.Raw[byID[uuidA]], l.Raw[byID[uuidB]]
	if got := keys(t, a); !slices.Contains(got, "extra") || slices.Index(got, "extra") != slices.Index(got, "source")+1 {
		t.Errorf("keys %v", got)
	}
	if string(extraRaw(t, a)) != `{"z":1,"ticket":"auth-3","r":1.10,"big":1e30,"l":[-0]}` {
		t.Errorf("extra %s", extraRaw(t, a))
	}
	if string(extraRaw(t, b)) != `null` {
		t.Errorf("extra without sesshin.json: %s", extraRaw(t, b))
	}

	_, l = f.list(`{"liveness":"all","fields":["extra","job"]}`)
	if got := keys(t, l.Raw[byID[uuidA]]); !slices.Equal(got, []string{"id", "session_id", "job", "extra"}) {
		t.Errorf("projection keys %v", got)
	}
}

func extraRaw(t *testing.T, session json.RawMessage) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(session, &m); err != nil {
		t.Fatal(err)
	}
	return m["extra"]
}
