//go:build !linux

package proc

import "github.com/phansen314/sesshin/internal/fsys"

// Find finds nothing here until macOS is built; see proc_linux.go.
func Find(fsy fsys.FS, claudePID string) Claude { return Claude{} }

// FindCaller finds nothing here until macOS is built; see proc_linux.go.
func FindCaller(fsy fsys.FS, claudePID string) Claude { return Claude{} }

// StartedAt fails here until macOS is built; see proc_linux.go.
func StartedAt(fsy fsys.FS, pid int64) (string, error) { return "", errUnsupported }
