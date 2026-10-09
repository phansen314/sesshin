package ops

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
)

// MaxCandidates is how many candidates an ambiguous error lists at most
// (operations.md, Error kinds).
const MaxCandidates = 20

// Selector is a session selector (operations.md, Selecting a session): a
// sesshin ID, a UUID prefix lowercased, a job, or self.
type Selector struct {
	// Raw is the selector as given.
	Raw string
	// Self is the selector self: the caller's own session. resolveSelf finds
	// it before the selector matches anything; until then, and when there is
	// none, it matches nothing.
	Self bool
	// uuid is the session Self resolved to.
	uuid string
	// ID is the sesshin ID, when the selector is all digits.
	ID int64
	// Prefix is the lowercased UUID prefix, when it is 8 to 36 characters of
	// hex digits and hyphens.
	Prefix string
	// Job is the job name, when it is any other selector that is one, bare
	// or after job:.
	Job string
}

func (s Selector) isJob() bool { return s.Job != "" }
func (s Selector) isID() bool  { return s.Job == "" && s.Prefix == "" } // self included: it names one session by its ID

// matches reports whether the session is one the selector names: by sesshin ID,
// by UUID or prefix, or by the job it reports. A job names many; choose
// takes one.
func (s Selector) matches(r *sessionRec) bool {
	switch {
	case s.Self:
		return s.uuid != "" && r.ID == s.uuid
	case s.isJob():
		return r.job != nil && *r.job == s.Job
	case s.isID():
		id := r.sesshinID()
		return id != nil && *id == s.ID
	}
	return strings.HasPrefix(r.ID, s.Prefix)
}

// choose is the sessions of recs, which are in session order, that the
// selector selects: every match, but for a job, which selects the first, the
// live one if there is one, else the last seen.
func (s Selector) choose(recs []*sessionRec) []*sessionRec {
	var found []*sessionRec
	for _, r := range recs {
		if s.matches(r) {
			found = append(found, r)
			if s.isJob() {
				break
			}
		}
	}
	return found
}

// resolveSelf resolves the selector self among recs, which are every session
// read: Claude's process is the nearest ancestor that is CLAUDE_PID or a
// claude by name (proc.FindCaller), and self is the session whose pid and pid_started_at are that
// process's and which is its live session, the top-ranked one of the process
// (Liveness rule 3). Any other selector is returned as it is. With no such
// session the selector matches nothing, which the operation reports as
// not-found.
func (s Selector) resolveSelf(env ReadEnv, recs []*sessionRec) Selector {
	if !s.Self {
		return s
	}
	lookup := env.Lookup
	if lookup == nil {
		lookup = proc.FindCaller
	}
	c := lookup(env.FS, env.Getenv("CLAUDE_PID"))
	if c.PID == 0 {
		return s
	}
	for _, r := range recs {
		if r.res.State == live.Ended || r.Lifecycle == nil {
			continue
		}
		pid, started := r.Lifecycle.PID, r.Lifecycle.PIDStartedAt
		if pid == nil && r.Statusline != nil {
			pid, started = r.Statusline.PID, r.Statusline.PIDStartedAt
		}
		if pid != nil && started != nil && *pid == c.PID && *started == c.StartedAt {
			s.uuid = r.ID
			return s
		}
	}
	return s
}

// parseSelector applies Selecting a session's rules, beyond what the
// schema's patterns say. The reason is for the invalid-input problem.
func parseSelector(s string) (Selector, string) {
	sel := Selector{Raw: s}
	if s == "self" { // before the job form, which it also matches
		sel.Self = true
		return sel, ""
	}
	if s != "" && strings.Trim(s, "0123456789") == "" {
		if s[0] == '0' {
			return sel, "a sesshin ID has no leading zero"
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n > model.MaxSafe {
			return sel, "a sesshin ID is at most 9007199254740991"
		}
		sel.ID = n
		return sel, ""
	}
	if len(s) >= 8 && len(s) <= 36 && strings.Trim(s, "0123456789abcdefABCDEF-") == "" {
		sel.Prefix = strings.ToLower(s) // ASCII only, as checked above
		return sel, ""
	}
	if job := strings.TrimPrefix(s, "job:"); model.IsJob(job) {
		sel.Job = job
		return sel, ""
	}
	return sel, "must be a sesshin ID, a UUID or a prefix of one of 8 to 36 characters of hex digits and hyphens, or a job name, bare or after job:"
}

// decodeSelector is the required session of show's, resume's, send's, focus's,
// and update's input: a selector by the rules of Selecting a session. It is the zero
// Selector when absent or invalid.
func decodeSelector(f *model.Fields, p *model.Problems) Selector {
	var sel Selector
	if v, ok := f.Required("session"); ok {
		ptr := f.Ptr("session")
		if s, ok := p.String(v, ptr); ok {
			var reason string
			sel, reason = parseSelector(s)
			if reason != "" {
				p.AddAdditional(ptr, reason)
			}
		}
	}
	return sel
}

// ShowInput is show's input (show-input).
type ShowInput struct {
	Selector       Selector
	IncludePayload bool
}

// DecodeShowInput is show's own checks: session required, and a selector by
// the rules of Selecting a session; include_payload a boolean.
func DecodeShowInput(f *model.Fields, p *model.Problems) ShowInput {
	in := ShowInput{Selector: decodeSelector(f, p)}
	if v, ok := f.Optional("include_payload"); ok {
		in.IncludePayload, _ = p.Bool(v, f.Ptr("include_payload"))
	}
	return in
}

// ShowOutput is show's result (show-output).
type ShowOutput struct {
	Session SessionView `json:"session"`
	// StatuslinePayload is present only with include_payload: the stored
	// payload, or null without a usable statusline.json.
	StatuslinePayload any `json:"statusline_payload,omitempty"`
}

// noSession is not-found for a selector that selected no session.
func noSession(sel Selector) *Error {
	return &Error{
		Kind:    KindNotFound,
		Message: "no session matches " + strconv.Quote(sel.Raw),
		Details: map[string]any{"selectors": []string{sel.Raw}, "paths": []string{}},
	}
}

// pool is the sessions a selector chooses among: a job's among those keep
// accepts, so a job names the live (or the ended) one; the other forms among
// all, so a refusal can name a session keep would have left out.
func (s Selector) pool(recs []*sessionRec, keep func(*sessionRec) bool) []*sessionRec {
	if !s.isJob() {
		return recs
	}
	var out []*sessionRec
	for _, r := range recs {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// selectOne is the one session sel selects among recs, or the not-found or
// ambiguous error.
func selectOne(sel Selector, recs []*sessionRec, vw viewer) (*sessionRec, *Error) {
	found := sel.choose(recs)
	switch len(found) {
	case 0:
		return nil, noSession(sel)
	case 1:
		return found[0], nil
	}
	return nil, ambiguous(sel, found, vw)
}

// ambiguous is the error for a selector that selected several sessions.
func ambiguous(sel Selector, found []*sessionRec, vw viewer) *Error {
	cands := []SessionRef{}
	for _, r := range found[:min(len(found), MaxCandidates)] {
		cands = append(cands, vw.view(r).ref())
	}
	details := map[string]any{"selector": sel.Raw, "candidates": cands}
	if len(found) > MaxCandidates {
		details["candidates_truncated"] = true
	}
	return &Error{
		Kind:    KindAmbiguous,
		Message: sel.Raw + " matches " + strconv.Itoa(len(found)) + " sessions",
		Details: details,
	}
}

// Show returns one session in full (operations.md, show). It takes no lock.
func showOp(in ShowInput, env ReadEnv) Envelope {
	set, _, e := readSessions(env)
	if e != nil {
		return Failed(e)
	}
	vw := viewer{fs: env.FS, now: set.now}
	in.Selector = in.Selector.resolveSelf(env, set.recs)
	r, selErr := selectOne(in.Selector, set.recs, vw)
	selected := ""
	if r != nil {
		selected = r.ID
	}
	// The warnings go with a failure too: an unusable lifecycle.json or
	// sesshin.json is why a session isn't found.
	var warnings []Warning
	for _, is := range set.issues {
		if showWarns(in.Selector, selected, is) {
			warnings = append(warnings, issueWarning(is))
		}
	}
	if selErr != nil {
		return FailedWith(selErr, warnings)
	}

	out := ShowOutput{Session: vw.view(r)}
	if in.IncludePayload {
		out.StatuslinePayload = json.RawMessage("null")
		if r.Statusline != nil {
			out.StatuslinePayload = r.Statusline.Payload
		}
	}
	res := Succeeded(out)
	res.Warnings = append(res.Warnings, warnings...)
	return res
}

// showWarns is whether show reports an issue: any file of the selected
// session ("" when it found none or several), and the lifecycle.json or
// sesshin.json of a session that might have matched the selector, as those are
// what it was matched through. A sesshin ID or a job is matched through every session's
// sesshin.json and lifecycle.json; a prefix, through the directory names.
func showWarns(sel Selector, selected string, is sessionIssue) bool {
	switch {
	case is.session == selected:
		return true
	case is.File == model.StatuslineName:
		return false
	case sel.isID(), sel.isJob():
		return true
	}
	return strings.HasPrefix(is.session, sel.Prefix)
}
