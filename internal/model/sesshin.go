package model

import "github.com/phansen314/sesshin/internal/jsonio"

// SesshinName is sesshin.json's name in a session directory.
const SesshinName = "sesshin.json"

// sesshin.json's source values (design-spec.md, sesshin.json).
const (
	SourceSpawn = "spawn"
	SourceHook  = "hook"
)

// SesshinFile is a session's sesshin.json (design-spec.md, sesshin.json), its fields
// in the schema's order.
type SesshinFile struct {
	Schema SesshinVersion `json:"schema"`
	// ID is the sesshin ID; nil while an issue is pending.
	ID *int64 `json:"id"`
	// Job is the session's job; nil for none.
	Job *string `json:"job"`
	// Source is SourceSpawn or SourceHook.
	Source string `json:"source"`
	// Placement is the terminal backend's, kept whole; nil when sesshin can't
	// place the session. Only its terminal tag is checked here: the backend
	// checks the rest itself, and treats a placement it can't use as null.
	Placement *jsonio.Object `json:"placement"`
}

// ReadSesshin reads a session's sesshin.json. Its content is meaningful only when
// the result is usable.
func ReadSesshin(data []byte) (SesshinFile, FileResult) {
	return readFile(data, SesshinSchema, func(h *SesshinFile, f *Fields, p *Problems) {
		h.ID, _ = nullablePositive(f, p, "id")
		h.Job = nullableGuarded(f, p, "job", IsJob, reasonJob)
		if v, ok := f.Required("source"); ok {
			if s, ok := p.String(v, f.Ptr("source")); ok {
				if s == SourceSpawn || s == SourceHook {
					h.Source = s
				} else {
					p.Add(f.Ptr("source"), reasonSource)
				}
			}
		}
		h.Placement = placementField(f, p)
	})
}

const reasonSource = "must be spawn or hook"

// placementField checks the required member placement of f: null, or an
// object with a terminal tag. It returns it whole, nil when null or failing.
func placementField(f *Fields, p *Problems) *jsonio.Object {
	v, ok := f.Required("placement")
	if !ok || v == nil {
		return nil
	}
	pf, ok := p.Object(v, f.Ptr("placement"))
	if !ok {
		return nil
	}
	before := len(p.List())
	if t, ok := pf.Required("terminal"); ok {
		p.guarded(t, pf.Ptr("terminal"), IsEnum, reasonEnum)
	}
	if len(p.List()) != before {
		return nil
	}
	return v.(*jsonio.Object)
}
