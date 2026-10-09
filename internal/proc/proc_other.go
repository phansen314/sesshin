//go:build !linux && !darwin

package proc

import "github.com/phansen314/sesshin/internal/fsys"

// Find finds nothing on a system other than Linux and macOS.
func Find(fsy fsys.FS, claudePID string) Claude { return Claude{} }

// FindCaller finds nothing on a system other than Linux and macOS.
func FindCaller(fsy fsys.FS, claudePID string) Claude { return Claude{} }

// StartedAt fails on a system other than Linux and macOS.
func StartedAt(fsy fsys.FS, pid int64) (string, error) { return "", errUnsupported }
