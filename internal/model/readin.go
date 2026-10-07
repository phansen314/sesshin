package model

import (
	"errors"
	"io/fs"

	"github.com/phansen314/sesshin/internal/fsys"
)

// FileStatus is how reading one of sesshin's files ended.
type FileStatus int

const (
	// FileMissing: there is no such file.
	FileMissing FileStatus = iota
	// FileUnreadable: it exists, and reading it failed (EACCES, EIO, EFBIG, a
	// directory in its place).
	FileUnreadable
	// FileUnusable: it was read, and failed its validation.
	FileUnusable
	// FileOtherFormat: it was read, and its schema is another version of the
	// format, a number this binary doesn't support. It is left alone, never
	// replaced (design-spec.md, Format versions).
	FileOtherFormat
	FileUsable
)

// ReadIn reads name from root and validates it with read: a missing file is
// not an error. It returns the validated file (meaningful only when usable),
// the bytes read, how the read ended, and, for unreadable and unusable, the
// error to log, in the log's grammar (hooks-spec.md, Log): `read <name>:
// <error>`, `<name> unusable: <reason>`, or `<name> in format <n>, not <m>;
// left alone`. Whether to log it is the caller's.
func ReadIn[T any](root fsys.Root, name string, read func([]byte) (T, FileResult)) (v T, data []byte, st FileStatus, err error) {
	data, err = root.ReadFile(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return v, nil, FileMissing, nil
	case err != nil:
		return v, nil, FileUnreadable, &readError{name: name, err: err}
	}
	v, r := read(data)
	if r.OtherFormat {
		return v, data, FileOtherFormat, errors.New(name + " " + r.Problems[0].Reason + "; left alone")
	}
	if !r.Usable {
		return v, data, FileUnusable, errors.New(name + " unusable: " + r.Reason())
	}
	return v, data, FileUsable, nil
}

// readError is an OS error reading a file. It keeps its chain, so errors.Is
// and fsys.ErrnoOf still see through it.
type readError struct {
	name string
	err  error
}

func (e *readError) Error() string { return "read " + e.name + ": " + fsys.Describe(e.err) }
func (e *readError) Unwrap() error { return e.err }
