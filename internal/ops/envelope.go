package ops

import (
	"github.com/phansen314/sesshin/internal/placement"
)

// Envelope is the output envelope (operations.md, Output envelope): Result on
// success, Error on failure, Warnings always present.
type Envelope struct {
	OK       bool      `json:"ok"`
	Result   any       `json:"result,omitempty"`
	Error    *Error    `json:"error,omitempty"`
	Warnings []Warning `json:"warnings"`
}

// Error is an envelope's error (operations.md, Error schema).
type Error struct {
	Kind    string         `json:"kind"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// Warning is one of an envelope's warnings (operations.md, Warning schema).
type Warning struct {
	Kind    string         `json:"kind"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// Error kinds (operations.md, Error kinds; cli-spec.md, Usage errors).
const (
	KindUsage        = "usage"
	KindInvalidInput = "invalid-input"
	KindEnvironment  = "environment"
	KindCorrupt      = "corrupt"
	KindConflict     = "conflict"
	KindBusy         = "busy"
	KindTerminal     = "terminal"
	KindIO           = "io"
	KindSelfTest     = "self-test-failed"
	KindInternal     = "internal"
	KindNotFound     = "not-found"
	KindAmbiguous    = "ambiguous"
	// KindUnsupportedFormat: state.json is newer than this binary (migrate).
	KindUnsupportedFormat = "unsupported-format"
)

// Warning kinds (operations.md, Warning kinds).
const (
	KindDuplicateID          = "duplicate-id"
	KindUnusableFile         = "unusable-file" // a session or reservation file that could not be used
	KindTranscriptMissing    = "transcript-missing"
	KindNotStarted           = "not-started"
	KindPlacementNotRecorded = "placement-not-recorded"
	KindMigrationPending     = "migration-pending"
	KindMigrationAhead       = "migration-ahead"
)

// The reasons of an unusable-file warning (operations.md, Warning kinds).
const (
	ReasonUnreadable        = "unreadable"         // an OS error, a file past the size limit, a directory in its place
	ReasonMissing           = "missing"            // a session directory without its lifecycle.json
	ReasonCorrupt           = "corrupt"            // read, but not a valid file of this format
	ReasonUnsupportedFormat = "unsupported-format" // in another format
)

// Succeeded is the envelope of a result with no warnings.
func Succeeded(result any) Envelope {
	return Envelope{OK: true, Result: result, Warnings: []Warning{}}
}

// Failed is the envelope of an error with no warnings. Nil details become {}.
func Failed(e *Error) Envelope {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	return Envelope{Error: e, Warnings: []Warning{}}
}

// FailedWith is the envelope of an error with the warnings found before it.
func FailedWith(e *Error, ws []Warning) Envelope {
	res := Failed(e)
	res.Warnings = append(res.Warnings, ws...)
	return res
}

// terminalError is terminal with the reason, backend tag (nil for none), and
// detail every one carries.
func terminalError(message, reason string, terminal any, detail string) *Error {
	return &Error{
		Kind:    KindTerminal,
		Message: message,
		Details: map[string]any{"reason": reason, "terminal": terminal, "detail": detail},
	}
}

// backendError is terminal from the backend b.
func backendError(b placement.Backend, reason, detail string) *Error {
	return terminalError(b.Tag()+": "+detail, reason, b.Tag(), detail)
}

// unsupported is terminal unsupported: b lacks the ability a command needs,
// named in the detail. Nothing was done.
func unsupported(b placement.Backend, ability string) *Error {
	return backendError(b, reasonUnsupported, "cannot "+ability)
}
