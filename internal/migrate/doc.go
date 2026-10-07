// Package migrate holds sesshin's migration steps and the conversion of one
// file through them (design-spec.md, Migrations; implementation-spec.md,
// Migrate). Only the sesshin binary links it: sesshin-hook never does.
//
// A Step is a pure function on a file's ordered tree. Convert applies the
// steps that cover a file's kind from the file's own schema up to the version
// this binary supports, marshals the result in sesshin's File format, and
// accepts it only if the kind's model reader finds it usable. It does no I/O:
// the run that walks the state directory, takes the locks, and publishes the
// files is ops.Migrate's.
package migrate
