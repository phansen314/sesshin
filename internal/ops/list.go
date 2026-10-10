package ops

import (
	"slices"
	"strconv"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
)

// ListInput is list's input (list-input).
type ListInput struct {
	// Liveness is live, ended, or all; "" is the default, live.
	Liveness        string
	IncludeHeadless bool
	// Fields is the projection; nil keeps every field, and an empty one only
	// the two that are always there.
	Fields []string
	// Limit is an explicit limit; nil when none was given.
	Limit *int64
}

// DecodeListInput is list's own checks: liveness one of its three values,
// include_headless a boolean, fields a list of distinct session view
// properties, and limit an integer from 0.
func DecodeListInput(f *model.Fields, p *model.Problems) ListInput {
	var in ListInput
	if v, ok := f.Optional("liveness"); ok {
		ptr := f.Ptr("liveness")
		if s, ok := p.String(v, ptr); ok {
			if s != "live" && s != "ended" && s != "all" {
				p.Add(ptr, "must be one of live, ended, all")
			} else {
				in.Liveness = s
			}
		}
	}
	if v, ok := f.Optional("include_headless"); ok {
		in.IncludeHeadless, _ = p.Bool(v, f.Ptr("include_headless"))
	}
	if v, ok := f.Optional("fields"); ok {
		ptr := f.Ptr("fields")
		arr, isArr := v.([]any)
		if !isArr {
			p.Add(ptr, "expected an array")
		}
		in.Fields = []string{}
		for i, item := range arr {
			iptr := ptr + "/" + strconv.Itoa(i)
			s, ok := p.String(item, iptr)
			switch {
			case !ok:
			case !slices.Contains(viewFields, s):
				p.AddAdditional(iptr, "not a session view field")
			case slices.Contains(in.Fields, s):
				p.Add(iptr, "duplicate item")
			default:
				in.Fields = append(in.Fields, s)
			}
		}
	}
	if v, ok := f.Optional("limit"); ok {
		if n, ok := p.Int(v, f.Ptr("limit"), 0, jsonio.MaxSafe); ok {
			in.Limit = &n
		}
	}
	return in
}

// ListOutput is list's result (list-output). Each session is a SessionView,
// or its projection to the fields asked for.
type ListOutput struct {
	Sessions  []any `json:"sessions"`
	Total     int   `json:"total"`
	Truncated bool  `json:"truncated"`
}

// List returns the sessions sesshin has recorded, live by default, in session
// order (operations.md, list). It takes no lock.
func listOp(in ListInput, env ReadEnv) Envelope {
	set, _, e := readSessions(env)
	if e != nil {
		return Failed(e)
	}
	var matched []*sessionRec
	for _, r := range set.recs {
		if wanted(r, in) {
			matched = append(matched, r)
		}
	}
	out := ListOutput{Sessions: []any{}, Total: len(matched)}
	vw := viewer{fs: env.FS, now: set.now}
	// The views are built once, as the duplicate-id warnings use them too.
	views := map[*sessionRec]SessionView{}
	view := func(r *sessionRec) SessionView {
		v, ok := views[r]
		if !ok {
			v = vw.view(r)
			views[r] = v
		}
		return v
	}
	var keep map[string]bool
	if in.Fields != nil {
		keep = map[string]bool{"id": true, "session_id": true}
		for _, f := range in.Fields {
			keep[f] = true
		}
	}
	shown := matched
	if in.Limit != nil && int64(len(shown)) > *in.Limit {
		shown = shown[:*in.Limit]
	}
	out.Truncated = len(shown) < len(matched)
	for _, r := range shown {
		v := view(r)
		if keep != nil {
			out.Sessions = append(out.Sessions, v.project(keep))
		} else {
			out.Sessions = append(out.Sessions, v)
		}
	}

	res := Succeeded(out)
	res.Warnings = append(res.Warnings, issueWarnings(set)...)
	res.Warnings = append(res.Warnings, duplicateIDs(matched, view)...)
	return res
}

// wanted is the narrowing by liveness and include_headless.
func wanted(r *sessionRec, in ListInput) bool {
	if r.isHeadless() && !in.IncludeHeadless {
		return false
	}
	switch in.Liveness {
	case "ended":
		return r.res.State == live.Ended
	case "all":
		return true
	}
	return r.res.State != live.Ended
}

// duplicateIDs is a duplicate-id warning for each sesshin ID that several of
// the sessions share, the lowest ID first, with the sessions in the order
// given.
func duplicateIDs(recs []*sessionRec, view func(*sessionRec) SessionView) []Warning {
	byID := map[int64][]*sessionRec{}
	var ids []int64
	for _, r := range recs {
		if id := r.sesshinID(); id != nil {
			if len(byID[*id]) == 1 {
				ids = append(ids, *id)
			}
			byID[*id] = append(byID[*id], r)
		}
	}
	slices.Sort(ids)
	var out []Warning
	for _, id := range ids {
		refs := []SessionRef{}
		for _, r := range byID[id] {
			refs = append(refs, view(r).ref())
		}
		out = append(out, Warning{
			Kind:    KindDuplicateID,
			Message: "sesshin ID " + strconv.FormatInt(id, 10) + " is shared by " + strconv.Itoa(len(refs)) + " sessions",
			Details: map[string]any{"id": id, "sessions": refs},
		})
	}
	return out
}
