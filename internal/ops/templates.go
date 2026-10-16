package ops

import (
	"errors"
	"path/filepath"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/templates"
)

// TemplatesInput is templates' input (templates-input): an empty object.
type TemplatesInput struct{}

// DecodeTemplatesInput is templates' own checks: it asks for no member, so
// any member is unknown.
func DecodeTemplatesInput(*model.Fields, *model.Problems) TemplatesInput { return TemplatesInput{} }

// TemplatesOutput is templates' result (templates-output).
type TemplatesOutput struct {
	Templates []TemplateEntry `json:"templates"`
}

// TemplateEntry is one template of the listing.
type TemplateEntry struct {
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	Description *string `json:"description"`
	// Template is the file as parsed; nil when the template file itself is
	// corrupt or unreadable.
	Template *jsonio.Object   `json:"template"`
	Problem  *TemplateProblem `json:"problem"`
}

// TemplateProblem is why spawn would refuse a template.
type TemplateProblem struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

// Templates lists the templates spawn can start from, with each one's
// description and content, and whether spawn would refuse it (operations.md,
// templates). It reads only the config directory, never the state directory,
// so it has no migration status to warn of.
func Templates(_ TemplatesInput, env ReadEnv) Envelope {
	l, e := resolveLocations(env.GOOS, env.Getenv)
	if e != nil {
		return Failed(e)
	}
	dir := filepath.Join(l.ConfigDir, templates.Dir)
	list, err := templates.List(env.FS, dir, env.Getenv("HOME"))
	var ioe *templates.IOError
	if errors.As(err, &ioe) {
		return Failed(IOError(ioe.Path, ioe.Err))
	}
	out := TemplatesOutput{Templates: []TemplateEntry{}}
	for _, en := range list {
		te := TemplateEntry{Name: en.Name, Path: en.Path}
		if en.Template != nil {
			te.Description, te.Template = en.Template.Description, en.Template.Object
		}
		if en.Problem != nil {
			te.Problem = &TemplateProblem{Kind: en.Problem.Kind, Path: en.Problem.Path, Detail: en.Problem.Detail}
		}
		out.Templates = append(out.Templates, te)
	}
	return Succeeded(out)
}
