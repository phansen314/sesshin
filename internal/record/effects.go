package record

import (
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/proc"
	"github.com/phansen314/sesshin/internal/text"
)

// The last_event_type bases, one per row of the effects table.
const (
	typeStart      = "start"
	typePrompt     = "prompt"
	typeTool       = "tool"
	typeToolFail   = "toolfail"
	typeStop       = "stop"
	typeStopFail   = "stopfail"
	typeNotify     = "notify"
	typeElicit     = "elicit"
	typeElicitDone = "elicit_done"
	typePreCompact = "precompact"
	typeCompact    = "compact"
	typeEnd        = "end"
)

// stallUnknown is StopFailure's stall_reason when its error is missing or
// fails its guard: the turn died either way.
const stallUnknown = "unknown"

// qualified is base:q, or base alone when q fails the shape guard: a bad
// qualifier costs the qualifier, never the write (Open sets).
func qualified(base, q string) string {
	if model.IsEnum(q) {
		return base + ":" + q
	}
	return base
}

// startsLife reports whether a SessionStart source begins a life: the first
// effects table row. Any other source, or none, is the second.
func startsLife(source string) bool {
	switch source {
	case "startup", "resume", "clear", "fork":
		return true
	}
	return false
}

// baseType is the last_event_type of a new record, until the event's own
// effects set it: the event's verb, so the field is never empty.
func baseType(k Kind) string {
	switch k {
	case SessionStart:
		return typeStart
	case UserPromptSubmit:
		return typePrompt
	case PostToolUse:
		return typeTool
	case PostToolUseFailure:
		return typeToolFail
	case Stop:
		return typeStop
	case StopFailure:
		return typeStopFail
	case PermissionPrompt:
		return typeNotify
	case ElicitationDialog:
		return typeElicit
	case ElicitationComplete:
		return typeElicitDone
	case PreCompact:
		return typePreCompact
	case PostCompact:
		return typeCompact
	}
	return typeEnd
}

// newRecord is a new lifecycle.json (A new lifecycle.json), before the
// event's clocks and effects: for the first SessionStart, a late adoption,
// and the replacement of an unusable file. It also fills what a late adoption
// does: the payload's cwd and transcript_path, the pid and nested of Claude's
// process, and the entrypoint. status starts as working; every event that
// sets one overrides it.
func (e Env) newRecord(ev Event, now model.Timestamp) model.LifecycleFile {
	l := model.LifecycleFile{
		SessionID:     e.SessionID,
		Status:        model.StatusWorking,
		StartedAt:     now,
		LastStartAt:   now,
		LastEventType: baseType(ev.Kind),
	}
	setText(&l.Cwd, ev.Cwd)
	setText(&l.TranscriptPath, ev.TranscriptPath)
	setText(&l.Model, ev.Model)
	c := ev.Claude
	if c == nil {
		found := proc.FindWith(e.Lookup, e.FS, e.Getenv("CLAUDE_PID"))
		c = &found
	}
	setClaude(&l, *c, true)
	l.Entrypoint = e.entrypoint()
	return l
}

// entrypoint is CLAUDE_CODE_ENTRYPOINT through its guard, nil when unset or
// failing it.
func (e Env) entrypoint() *string {
	v := e.Getenv("CLAUDE_CODE_ENTRYPOINT")
	if !model.IsEntrypoint(v) {
		return nil
	}
	return &v
}

// setClaude records a lookup's result. With replace set (a life begins, or
// the file is new), pid, pid_started_at, and nested become the result, null
// where it found none. Without it, a result that found no Claude changes
// nothing, and nested changes only when the lookup could tell (session-start,
// Effects).
func setClaude(l *model.LifecycleFile, c proc.Claude, replace bool) {
	if replace {
		l.PID, l.PIDStartedAt, l.Nested = nil, nil, nil
	}
	if c.PID != 0 {
		pid, at := c.PID, c.StartedAt
		l.PID, l.PIDStartedAt = &pid, &at
	}
	if c.Nested != nil {
		nested := *c.Nested
		l.Nested = &nested
	}
}

// setText stores s in *p when it is non-empty, scrubbed.
func setText(p **string, s string) {
	if s = text.Scrub(s); s != "" {
		*p = &s
	}
}

// count clamps a payload count to the field's range.
func count(n int64) *int64 {
	n = max(0, min(n, jsonio.MaxSafe))
	return &n
}

// apply applies ev to l: the clocks, then the event's row of the effects
// table unless the event is a straggler (Recording an event, step 3).
func (e Env) apply(l *model.LifecycleFile, ev Event, now model.Timestamp, straggler bool) {
	l.LastEventAt = now
	l.EventSeq++
	if pm := ev.PermissionMode; model.IsPermissionMode(pm) {
		l.PermissionMode = &pm
	}
	if straggler {
		return
	}
	switch ev.Kind {
	case SessionStart:
		e.applyStart(l, ev, now)
	case UserPromptSubmit:
		turn(l, model.StatusWorking, typePrompt)
		l.EndedPromptID = nil
		setText(&l.SessionTitle, ev.SessionTitle)
	case PostToolUse:
		turn(l, model.StatusWorking, typeTool)
	case PostToolUseFailure:
		turn(l, model.StatusWorking, typeToolFail)
	case Stop:
		ended(l, ev, typeStop)
	case StopFailure:
		ended(l, ev, typeStopFail)
		reason := stallUnknown
		if model.IsEnum(ev.Error) {
			reason = ev.Error
		}
		l.StallReason = &reason
	case PermissionPrompt:
		turn(l, model.StatusNeedsApproval, typeNotify)
	case ElicitationDialog:
		turn(l, model.StatusNeedsApproval, typeElicit)
	case ElicitationComplete:
		turn(l, model.StatusWorking, typeElicitDone)
	case PreCompact:
		l.LastEventType = typePreCompact
	case PostCompact:
		l.LastEventType = qualified(typeCompact, ev.Trigger)
		l.Compactions++
	case SessionEnd:
		l.LastEventType = qualified(typeEnd, ev.Reason)
		l.EndedAt = &now
		l.EndReason = nil
		if model.IsEnum(ev.Reason) {
			reason := ev.Reason
			l.EndReason = &reason
		}
	}
}

// turn is a row that starts or continues a turn: it clears stall_reason and
// the pending counts (the effects table's notes).
func turn(l *model.LifecycleFile, status, typ string) {
	l.Status = status
	l.LastEventType = typ
	l.StallReason = nil
	l.BackgroundTasks, l.SessionCrons = nil, nil
}

// ended is a row that ends a turn: Stop's and StopFailure's, bar the
// stall_reason of a failure. An empty prompt_id leaves ended_prompt_id null:
// the guard it feeds is inert without one.
func ended(l *model.LifecycleFile, ev Event, typ string) {
	l.Status = model.StatusWaiting
	l.LastEventType = typ
	l.StallReason = nil
	l.EndedPromptID = nil
	setText(&l.EndedPromptID, ev.PromptID)
	l.BackgroundTasks, l.SessionCrons = count(ev.BackgroundTasks), count(ev.SessionCrons)
}

// applyStart is the two SessionStart rows, and session-start's Effects.
func (e Env) applyStart(l *model.LifecycleFile, ev Event, now model.Timestamp) {
	life := startsLife(ev.Source)
	if life {
		l.Status = model.StatusIdle
		l.LastEventType = typeStart
		if ev.Source != "startup" {
			l.LastEventType = qualified(typeStart, ev.Source)
		}
		l.EndedPromptID, l.StallReason = nil, nil
		l.BackgroundTasks, l.SessionCrons = nil, nil
		l.LastStartAt = now
		l.EndedAt, l.EndReason = nil, nil
	} else {
		l.LastEventType = qualified(typeStart, ev.Source)
	}
	setText(&l.Cwd, ev.Cwd)
	setText(&l.TranscriptPath, ev.TranscriptPath)
	setText(&l.Model, ev.Model)
	if ev.Claude != nil {
		if life {
			setClaude(l, *ev.Claude, true)
		} else if ev.Claude.PID != 0 {
			setClaude(l, *ev.Claude, false)
		}
	}
	if ep := e.entrypoint(); life || ep != nil {
		l.Entrypoint = ep
	}
}
