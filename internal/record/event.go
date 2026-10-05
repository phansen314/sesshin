package record

import (
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/proc"
)

// Kind is a row of the hooks spec's effects table: what happened. The
// verbs choose it from the payload's event name and notification type; the
// effects, the guard, and whether the hook may adopt follow from it.
type Kind int

const (
	// SessionStart is the two SessionStart rows: a source of startup,
	// resume, clear, or fork starts a life; any other, or none, is
	// status-neutral (compact among them).
	SessionStart Kind = iota + 1
	UserPromptSubmit
	PostToolUse
	PostToolUseFailure
	Stop
	StopFailure
	// PermissionPrompt, ElicitationDialog, and ElicitationComplete are the
	// Notification rows. Other notification types record nothing, and have
	// no Kind: their verb returns before it calls Record.
	PermissionPrompt
	ElicitationDialog
	ElicitationComplete
	PreCompact
	PostCompact
	SessionEnd
)

func (k Kind) valid() bool { return k >= SessionStart && k <= SessionEnd }

// guarded reports whether the straggler guard checks the kind (the effects
// table's Guarded column).
func (k Kind) guarded() bool {
	switch k {
	case PostToolUse, PostToolUseFailure, PermissionPrompt, ElicitationDialog, ElicitationComplete:
		return true
	}
	return false
}

// adopts reports whether a hook of this kind may adopt a session it finds
// with no lifecycle.json, creating the session directory if need be (Late
// adoption). Only session-end among the events recorded here may not: a
// session sesshin never saw that is ending has nothing worth recording.
// cwd-changed, terminal-sync, and the statusline don't adopt either, and
// don't record through this package.
func (k Kind) adopts() bool { return k != SessionEnd }

// replacesPlacement reports whether the kind replaces the placement of a
// sesshin.json that already has an ID: only SessionStart (Creating sesshin.json,
// step 3).
func (k Kind) replacesPlacement() bool { return k == SessionStart }

// Event is what happened: a Kind and the payload values that qualify it. A
// field its Kind doesn't read is ignored. Strings are the payload's, already
// scrubbed and guarded; Record guards them again, so a value that fails costs
// its qualifier or field and never the write (H6).
type Event struct {
	Kind Kind

	// PromptID is the payload's prompt_id: the straggler guard's key, and
	// what Stop and StopFailure store as ended_prompt_id.
	PromptID string
	// PermissionMode is set by every event that has one.
	PermissionMode string

	// Cwd, TranscriptPath, and Model are set by SessionStart; any event
	// that creates the file also takes the first two, as a late adoption
	// does. A verb whose hook doesn't read them leaves them empty.
	Cwd            string
	TranscriptPath string
	Model          string

	// The qualifiers of last_event_type, and of end_reason and stall_reason:
	// Source for SessionStart, Trigger for PostCompact, Reason for
	// SessionEnd, Error for StopFailure.
	Source  string
	Trigger string
	Reason  string
	Error   string

	// BackgroundTasks and SessionCrons are Stop's and StopFailure's counts.
	BackgroundTasks int64
	SessionCrons    int64
	// SessionTitle is UserPromptSubmit's: an empty one leaves the stored
	// title alone.
	SessionTitle string

	// Claude is SessionStart's lookup of Claude's process, made before any
	// lock (session-start, step 1). Nil means none was made: an existing
	// file keeps its pid, and a file being created makes the lookup itself,
	// as a late adoption does.
	Claude *proc.Claude
}

// FromPayload is the event of kind k with everything the payload carries.
// The verb sets Claude where it has one.
func FromPayload(k Kind, p payload.Payload) Event {
	return Event{
		Kind:            k,
		PromptID:        p.PromptID,
		PermissionMode:  p.PermissionMode,
		Cwd:             p.Cwd,
		TranscriptPath:  p.TranscriptPath,
		Model:           p.Model,
		Source:          p.Source,
		Trigger:         p.Trigger,
		Reason:          p.Reason,
		Error:           p.Error,
		BackgroundTasks: p.BackgroundTasks,
		SessionCrons:    p.SessionCrons,
		SessionTitle:    p.SessionTitle,
	}
}
