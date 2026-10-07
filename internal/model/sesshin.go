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
	// Extra is the user-owned object (design-spec.md, User-owned extra), kept
	// whole in the ordered tree, numbers as written. sesshin never reads its
	// contents. Never nil in a file that reads usable.
	Extra *jsonio.Object `json:"extra"`
}

// extra's limits (design-spec.md, User-owned extra).
const (
	// ExtraMaxBytes is the most an extra may hold as compact JSON.
	ExtraMaxBytes = 65536
	// ExtraMaxDepth is the deepest an extra may nest, counting its own
	// object as 1.
	ExtraMaxDepth = 32
)

const (
	reasonExtraBytes = "must be at most 65536 bytes as compact JSON"
	reasonExtraDepth = "must be nested at most 32 levels, counting its own object"
)

// ExtraProblem returns why o breaks extra's limits, or "" when it is within
// them. The size is measured as MarshalLine writes it, less the newline.
func ExtraProblem(o *jsonio.Object) string {
	if extraDepth(o) > ExtraMaxDepth {
		return reasonExtraDepth
	}
	b, err := jsonio.MarshalLine(o)
	if err != nil || len(b)-1 > ExtraMaxBytes {
		return reasonExtraBytes
	}
	return ""
}

// extraDepth is the nesting of v: an object or array is one level plus its
// deepest child.
func extraDepth(v any) int {
	var kids []any
	switch v := v.(type) {
	case *jsonio.Object:
		for _, m := range v.Members {
			kids = append(kids, m.Value)
		}
	case []any:
		kids = v
	default:
		return 0
	}
	most := 0
	for _, k := range kids {
		most = max(most, extraDepth(k))
	}
	return most + 1
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
		h.Extra = extraField(f, p)
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

// extraField checks the required member extra of f: an object within the
// limits. Its contents are otherwise unchecked, numbers included. It returns
// it whole, nil when failing.
func extraField(f *Fields, p *Problems) *jsonio.Object {
	v, ok := f.Required("extra")
	if !ok {
		return nil
	}
	if _, ok := p.Object(v, f.Ptr("extra")); !ok {
		return nil
	}
	o := v.(*jsonio.Object)
	if why := ExtraProblem(o); why != "" {
		p.AddAdditional(f.Ptr("extra"), why)
		return nil
	}
	return o
}
