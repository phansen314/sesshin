package templates

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
)

// NotFound is a template, or its prompt file, that does not exist or is not a
// regular file once symlinks are followed.
type NotFound struct {
	// Path is the file at fault, absolute for a prompt file.
	Path string
}

func (e *NotFound) Error() string { return e.Path + " does not exist or is not a regular file" }

// IOError is an OS error reading a template or its prompt file.
type IOError struct {
	Path string
	Err  error
}

func (e *IOError) Error() string { return e.Path + ": " + fsys.Describe(e.Err) }
func (e *IOError) Unwrap() error { return e.Err }

// Corrupt is a template, or a prompt file's contents, that was read and is
// wrong.
type Corrupt struct {
	Path   string
	Detail string
}

func (e *Corrupt) Error() string { return e.Path + ": " + e.Detail }

// Path is the file of the template called name in dir.
func Path(dir, name string) string { return filepath.Join(dir, name+Ext) }

// Load reads the template called name in dir, the templates directory. name
// has passed model.IsTemplateName. The file is read, then validated, and then
// its prompt_file, if any, is read in full and becomes the Prompt, so a bad
// prompt file is an error even when the input will replace the prompt. A
// leading ~/ in cwd is expanded against home.
//
// The errors are *NotFound, *IOError, and *Corrupt. When the template file
// itself was read and valid, the template is returned with an error from its
// prompt file too, for the listing, which reports the rest of it.
func Load(fsy fsys.FS, dir, home, name string) (*Template, error) {
	path := Path(dir, name)
	info, err := fsy.Stat(path) // follows symlinks: a template may be a link
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, &NotFound{Path: path}
	case err != nil:
		return nil, &IOError{Path: path, Err: err}
	case !info.Mode().IsRegular():
		return nil, &NotFound{Path: path}
	}
	data, err := fsy.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, &NotFound{Path: path}
	case err != nil:
		return nil, &IOError{Path: path, Err: err}
	}
	t, problems := Read(data)
	if len(problems) > 0 {
		return nil, &Corrupt{Path: path, Detail: describe(problems)}
	}
	if t.Cwd != nil && strings.HasPrefix(*t.Cwd, "~/") {
		cwd := strings.TrimSuffix(home, "/") + (*t.Cwd)[1:]
		t.Cwd = &cwd
	}
	if t.PromptFile != nil {
		prompt, err := readPrompt(fsy, filepath.Join(dir, *t.PromptFile))
		if err != nil {
			return &t, err
		}
		t.Prompt = &prompt
	}
	return &t, nil
}

// readPrompt reads a prompt file exactly: a regular file, in UTF-8, with no
// NUL.
func readPrompt(fsy fsys.FS, path string) (string, error) {
	info, err := fsy.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", &NotFound{Path: path}
	case err != nil:
		return "", &IOError{Path: path, Err: err}
	case !info.Mode().IsRegular():
		return "", &NotFound{Path: path}
	}
	data, err := fsy.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", &NotFound{Path: path}
	case err != nil:
		return "", &IOError{Path: path, Err: err}
	case !utf8.Valid(data):
		return "", &Corrupt{Path: path, Detail: "the prompt file is not valid UTF-8"}
	case strings.ContainsRune(string(data), 0):
		return "", &Corrupt{Path: path, Detail: "the prompt file contains a NUL"}
	}
	return string(data), nil
}

// describe is the problems of a template, as the detail of its Corrupt.
func describe(problems []model.Problem) string {
	parts := make([]string, len(problems))
	for i, p := range problems {
		parts[i] = p.Field + ": " + p.Reason
		if p.Field == "" {
			parts[i] = p.Reason
		}
	}
	return strings.Join(parts, "; ")
}

// Problem is why spawn would refuse a template, as the listing reports it.
type Problem struct {
	// Kind is the error kind spawn would raise: corrupt, not-found, or io.
	Kind   string
	Path   string
	Detail string
}

// Entry is one template of the listing.
type Entry struct {
	Name string
	// Path is the template file, as listed: a symlink is not resolved.
	Path string
	// Template is the template as read; nil when the template file itself is
	// corrupt or unreadable, but not for a bad prompt file.
	Template *Template
	// Problem is nil when spawn would accept the template.
	Problem *Problem
}

// List lists the templates in dir by name, bytewise: the top-level regular
// files and symlinks named <valid name>.json, each loaded as Load does. A
// problem with one is its entry's, not an error. A missing dir is none; one
// that can't be listed is an *IOError.
func List(fsy fsys.FS, dir, home string) ([]Entry, error) {
	root, err := fsy.OpenRoot(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, &IOError{Path: dir, Err: err}
	}
	defer root.Close()
	des, err := root.ReadDir(".")
	if err != nil {
		return nil, &IOError{Path: dir, Err: err}
	}
	var names []string
	for _, d := range des {
		stem, ok := strings.CutSuffix(d.Name(), Ext)
		if ok && model.IsTemplateName(stem) && (d.Type().IsRegular() || d.Type()&fs.ModeSymlink != 0) {
			names = append(names, stem)
		}
	}
	sort.Strings(names)
	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		t, err := Load(fsy, dir, home, name)
		e := Entry{Name: name, Path: Path(dir, name), Template: t}
		if err != nil {
			e.Problem = problemOf(err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// problemOf is err as a listing's problem; an io one's detail is the errno's
// name.
func problemOf(err error) *Problem {
	switch e := err.(type) {
	case *NotFound:
		return &Problem{Kind: "not-found", Path: e.Path, Detail: e.Error()}
	case *Corrupt:
		return &Problem{Kind: "corrupt", Path: e.Path, Detail: e.Detail}
	case *IOError:
		code := "unnamed OS error"
		if errno, ok := fsys.ErrnoOf(e.Err); ok {
			if name, ok := fsys.ErrnoName(errno); ok {
				code = name
			}
		}
		return &Problem{Kind: "io", Path: e.Path, Detail: code}
	}
	return &Problem{Kind: "corrupt", Path: "", Detail: err.Error()}
}
