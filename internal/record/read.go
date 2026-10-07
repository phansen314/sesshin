package record

import (
	"bytes"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

// The ways reading one of sesshin's files ended (model.ReadIn).
const (
	missing    = model.FileMissing
	unreadable = model.FileUnreadable
	unusable   = model.FileUnusable
	otherFmt   = model.FileOtherFormat
	usable     = model.FileUsable
)

// readFile reads name from root and validates it with read (model.ReadIn),
// logging an unreadable or unusable file and returning what was logged. A
// file in another format is logged by session-start alone (logFormat); the
// error comes back either way.
func readFile[T any](e Env, root fsys.Root, name string, read func([]byte) (T, model.FileResult)) (v T, data []byte, st model.FileStatus, err error) {
	v, data, st, err = model.ReadIn(root, name, read)
	switch {
	case st == otherFmt:
		e.logFormat(err)
	case err != nil:
		e.Log(err.Error())
	}
	return v, data, st, err
}

// logFormat logs err, a file in another format, when the hook is
// session-start; every other hook leaves the file alone silently.
func (e Env) logFormat(err error) {
	if e.sessionStart {
		e.Log(err.Error())
	}
}

// logErr logs err and returns it.
func (e Env) logErr(err error) error {
	e.Log(err.Error())
	return err
}

// write marshals v and publishes it as name in root, logging a failure as
// `write <name>: <error>`, marshaling's included.
func (e Env) write(root fsys.Root, name string, v any) error {
	out, err := jsonio.MarshalFile(v)
	if err != nil {
		return e.logErr(wrap("write "+name, err))
	}
	return e.publish(root, name, out)
}

// writeIfChanged marshals v and publishes it as name in root unless it is
// the bytes already read, data, logging a failure as write does.
func (e Env) writeIfChanged(root fsys.Root, name string, v any, data []byte) error {
	out, err := jsonio.MarshalFile(v)
	if err != nil {
		return e.logErr(wrap("write "+name, err))
	}
	if bytes.Equal(out, data) {
		return nil
	}
	return e.publish(root, name, out)
}

// publish writes the bytes marshaled for name, logging a failure.
func (e Env) publish(root fsys.Root, name string, data []byte) error {
	if err := fsys.Publish(root, name, data); err != nil {
		return e.logErr(wrap("write "+name, err))
	}
	return nil
}
