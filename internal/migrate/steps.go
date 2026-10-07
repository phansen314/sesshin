package migrate

import "github.com/phansen314/sesshin/internal/jsonio"

// Kind names a kind of file that a step can cover.
type Kind string

const (
	// State is state.json.
	State Kind = "state"
	// Sesshin is a session's sesshin.json.
	Sesshin Kind = "sesshin"
)

// FileStep is a step's conversion of one kind of file.
type FileStep struct {
	// From is the schema the step takes the file from; the step leaves it at
	// From + 1.
	From int64
	// Apply converts the file's tree. It knows the old format from its own
	// code, never from model's current types, and is pure: it may return its
	// argument changed or a new tree, and does no I/O. An error means the file
	// isn't in the old format; its text is the detail reported to the user.
	Apply func(*jsonio.Object) (*jsonio.Object, error)
}

// Step is one numbered migration.
type Step struct {
	// N is the step's number, 1, 2, 3, ... in the order written.
	N    int
	Name string
	// Files holds the kinds of file the step covers.
	Files map[Kind]FileStep
}

// Steps is every step this binary knows, in order. The last one's N is
// model.LatestMigration.
var Steps = []Step{
	step001,
}
