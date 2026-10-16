package record

import (
	"testing"
	"time"
)

// templateText is the session's template as JSON text.
func templateText(t *testing.T, f *fix, id string) string {
	t.Helper()
	h := f.sesshin(id)
	if h.Template == nil {
		return "null"
	}
	return `"` + *h.Template + `"`
}

// Hooks-spec, Creating sesshin.json: the reservation a session adopts hands it
// its template, whether the hook creates the file or completes its pending id.
func TestTemplateAdoptedFromReservation(t *testing.T) {
	t.Run("a fresh write", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.reserveTemplate("api", tokA, time.Minute, true, `{}`, `"review"`)
		f.rec(Event{Kind: PostToolUse})
		if got := templateText(t, f, sid); got != `"review"` {
			t.Errorf("template %s", got)
		}
	})
	t.Run("completing a pending id", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.reserveTemplate("api", tokA, time.Minute, true, `{}`, `"review"`)
		f.write(f.sessionPath(sid, "sesshin.json"), pendingSesshin)
		f.rec(Event{Kind: PostToolUse})
		if got := templateText(t, f, sid); got != `"review"` {
			t.Errorf("template %s", got)
		}
	})
	t.Run("a reservation with none", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.reserveTemplate("api", tokA, time.Minute, true, `{}`, `null`)
		f.rec(start())
		if got := templateText(t, f, sid); got != "null" {
			t.Errorf("template %s", got)
		}
	})
}

// Without a fresh reservation of its own a file written afresh has null: none,
// or a stale one.
func TestTemplateNullWithoutAdoption(t *testing.T) {
	t.Run("no reservation", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.rec(start())
		if got := templateText(t, f, sid); got != "null" {
			t.Errorf("template %s", got)
		}
	})
	t.Run("a stale reservation", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.reserveTemplate("api", tokA, 3*time.Minute, false, `{}`, `"review"`)
		f.rec(start())
		if got := templateText(t, f, sid); got != "null" {
			t.Errorf("template %s", got)
		}
	})
	t.Run("a nested session", func(t *testing.T) {
		f := jobFix(t, "api", tokA)
		f.reserveTemplate("api", tokA, time.Minute, true, `{}`, `"review"`)
		f.env.Lookup = nestedClaude
		f.rec(start())
		if got := templateText(t, f, sid); got != "null" {
			t.Errorf("template %s", got)
		}
	})
}

// A /clear after adoption inherits the variables, but the reservation was
// removed by the first session: the next starts with no template.
func TestTemplateNotInherited(t *testing.T) {
	f := jobFix(t, "api", tokA)
	f.reserveTemplate("api", tokA, time.Minute, true, `{}`, `"review"`)
	f.rec(start())
	e := f.as(idB)
	if err := recordWith(e, Event{Kind: SessionStart, Source: "clear"}); err != nil {
		t.Fatal(err)
	}
	if got := templateText(t, f, idB); got != "null" {
		t.Errorf("the cleared session's template %s", got)
	}
}

// A completed file without a reservation keeps its template.
func TestTemplateKeptOnCompletion(t *testing.T) {
	f := newFix(t)
	f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 3, "id": null, "job": null, "source": "spawn", "placement": null, "extra": {}, "template": "review"}`)
	f.rec(Event{Kind: PostToolUse})
	if f.sesshinID(sid) != 1 {
		t.Fatal("id not issued")
	}
	if got := templateText(t, f, sid); got != `"review"` {
		t.Errorf("template %s", got)
	}
}

// Adopting a resumed session's reservation never touches its template,
// whatever the reservation holds.
func TestTemplateUntouchedByResumeAdoption(t *testing.T) {
	f := resumeFix(t)
	f.write(f.sessionPath(sid, "sesshin.json"), `{"schema": 3, "id": 1, "job": null, "source": "hook", "placement": null, "extra": {}, "template": "mine"}`)
	f.reserveTemplate("api", tokA, time.Minute, true, `{}`, `"theirs"`)
	f.rec(Event{Kind: SessionStart, Source: "resume"})
	f.wantJob(sid, "api", "hook")
	if got := templateText(t, f, sid); got != `"mine"` {
		t.Errorf("template %s", got)
	}
}
