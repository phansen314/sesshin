package ops

import (
	"fmt"

	"github.com/phansen314/sesshin/internal/fsys"
)

// IOError reports an OS error at path as io, with the errno's symbolic name
// (operations.md, Error kinds; implementation-spec.md, OS errors). An error
// with no errno inside it, or one with no name, is a bug in sesshin's table or a
// failure the OS did not report: internal, never an invented code. A call site
// that gives an errno another meaning classifies it before calling this.
func IOError(path string, err error) *Error {
	e, ok := fsys.ErrnoOf(err)
	if !ok {
		return &Error{Kind: KindInternal, Message: fmt.Sprintf("%s: error with no OS error code: %v", path, err)}
	}
	code, ok := fsys.ErrnoName(e)
	if !ok {
		return &Error{Kind: KindInternal, Message: fmt.Sprintf("%s: OS error %d has no symbolic name: %v", path, int(e), err)}
	}
	return &Error{
		Kind:    KindIO,
		Message: fmt.Sprintf("%s: %s", path, code),
		Details: map[string]any{"path": path, "code": code},
	}
}
