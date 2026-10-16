package templates

import (
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
)

// Dir is the templates' directory in the config directory.
const Dir = "templates"

// Ext ends a template's file name: <name>.json.
const Ext = ".json"

// The spawn types a template may name (the template schema's type).
var types = []string{"tab", "split", "os-window"}

const maxStartTimeout = 120

// Template is one template as loaded: each key a pointer or a nil slice, so
// that absent differs from empty (design-spec.md, Templates).
type Template struct {
	Job, Cwd, Type, Name *string
	// Prompt is the first prompt: the file's own, or, once Load has read the
	// prompt file, its contents.
	Prompt *string
	// PromptFile is as written, relative to the templates directory.
	PromptFile       *string
	Args             []string
	Vars             []placement.Var
	StartTimeoutSecs *int64
	Description      *string
	// Object is the file as read, for the listing, which reports it as
	// parsed.
	Object *jsonio.Object
}

// Read is the validator of the template schema (design-spec.md, Templates),
// in the order of the other validators, less the schema step: the read, the
// repeated keys, then every field in schema order, every problem collected.
// The template is meaningful only when there are none.
func Read(data []byte) (Template, []model.Problem) {
	t, p := read(data)
	return t, p.List()
}

// read is Read with the problems as collected, which the agreement test asks
// which the schema also finds.
func read(data []byte) (Template, *model.Problems) {
	var t Template
	var p model.Problems
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		p.Add("", err.Error())
		return t, &p
	}
	t.Object = obj
	for _, ptr := range repeated {
		p.AddAdditional(ptr, "repeated key") // beyond the schema, which can't see repeats
	}
	f, _ := p.Object(obj, "")
	if v, ok := f.Optional("job"); ok {
		if s, ok := p.String(v, f.Ptr("job")); ok {
			if model.IsJob(s) {
				t.Job = &s
			} else {
				p.Add(f.Ptr("job"), "must match ^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$ and not be all digits")
			}
		}
	}
	if v, ok := f.Optional("cwd"); ok {
		if s, ok := text(&p, v, f.Ptr("cwd")); ok {
			if !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "~/") {
				p.Add(f.Ptr("cwd"), "must be absolute, or start with ~/")
			} else {
				t.Cwd = &s
			}
		}
	}
	if v, ok := f.Optional("type"); ok {
		if s, ok := p.String(v, f.Ptr("type")); ok {
			if !slices.Contains(types, s) {
				p.Add(f.Ptr("type"), "must be one of "+strings.Join(types, ", "))
			} else {
				t.Type = &s
			}
		}
	}
	if v, ok := f.Optional("name"); ok {
		if s, ok := text(&p, v, f.Ptr("name")); ok {
			if s == "" {
				p.Add(f.Ptr("name"), "must not be empty")
			} else {
				t.Name = &s
			}
		}
	}
	prompt, hasPrompt := f.Optional("prompt")
	if hasPrompt {
		if s, ok := text(&p, prompt, f.Ptr("prompt")); ok {
			t.Prompt = &s
		}
	}
	if v, ok := f.Optional("prompt_file"); ok {
		if s, ok := text(&p, v, f.Ptr("prompt_file")); ok {
			switch {
			case s == "" || s[0] == '/':
				p.Add(f.Ptr("prompt_file"), "must be a path relative to the templates directory")
			case slices.Contains(strings.Split(s, "/"), ".."):
				p.AddAdditional(f.Ptr("prompt_file"), "must not have a .. component")
			default:
				t.PromptFile = &s
			}
		}
		if hasPrompt {
			p.Add("", "give prompt or prompt_file, not both")
		}
	}
	if v, ok := f.Optional("args"); ok {
		ptr := f.Ptr("args")
		if items, ok := v.([]any); !ok {
			p.Add(ptr, "expected an array")
		} else {
			t.Args = []string{}
			for i, item := range items {
				s, _ := text(&p, item, jsonio.Pointer(ptr, strconv.Itoa(i)))
				t.Args = append(t.Args, s)
			}
		}
	}
	if v, ok := f.Optional("vars"); ok {
		ptr := f.Ptr("vars")
		if o, ok := v.(*jsonio.Object); !ok {
			p.Add(ptr, "expected a JSON object")
		} else {
			t.Vars = []placement.Var{}
			for _, m := range o.Members {
				mptr := jsonio.Pointer(ptr, m.Key)
				if !model.IsVarKey(m.Key) {
					p.AddAdditional(mptr, "must match ^[A-Za-z_][A-Za-z0-9_]{0,63}$")
				}
				if s, ok := text(&p, m.Value, mptr); ok {
					t.Vars = append(t.Vars, placement.Var{Name: m.Key, Value: s})
				}
			}
		}
	}
	if v, ok := f.Optional("start_timeout_secs"); ok {
		if n, ok := p.Int(v, f.Ptr("start_timeout_secs"), 0, maxStartTimeout); ok {
			t.StartTimeoutSecs = &n
		}
	}
	if v, ok := f.Optional("description"); ok {
		if s, ok := text(&p, v, f.Ptr("description")); ok {
			t.Description = &s
		}
	}
	f.Done()
	return t, &p
}

// text checks that v, at ptr, is a string with no NUL. (UTF-8 is the
// reader's.) A string with a NUL is reported and still returned, for the
// checks of the schema's that follow.
func text(p *model.Problems, v any, ptr string) (string, bool) {
	s, ok := p.String(v, ptr)
	if ok && strings.ContainsRune(s, 0) {
		p.AddAdditional(ptr, "must not contain a NUL")
	}
	return s, ok
}
