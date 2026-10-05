package buildinfo

import (
	"debug/buildinfo"
	"runtime"
	"runtime/debug"
)

// Devel is Go's main-module version for a build with no version to report:
// one from a checkout with no VCS information, or with no build info at all.
const Devel = "(devel)"

// Info describes a binary's build.
type Info struct {
	Version  string // the main module's version as Go stamped it: a tag, a pseudo-version, maybe "+dirty", or Devel
	Commit   string // vcs.revision; "" when the build has no VCS information
	Modified bool   // vcs.modified: built with uncommitted changes
	Go       string // the toolchain that built it
}

// Identified reports whether the build names the source it came from: a
// commit, or else a module version, which the module proxy never changes. A
// Devel build without a commit could be any source, so install refuses it
// (operations.md, install, step 2).
func (i Info) Identified() bool {
	return i.Commit != "" || i.Version != Devel
}

// Same reports whether a and b are the same build: the same version, commit,
// and uncommitted-changes flag. The Go version is not compared.
func Same(a, b Info) bool {
	return a.Version == b.Version && a.Commit == b.Commit && a.Modified == b.Modified
}

// Read reports the running binary's build information.
func Read() Info {
	bi, _ := debug.ReadBuildInfo() // nil when there is none
	return fromBuild(bi, runtime.Version())
}

// ReadFile reports the build information embedded in the Go binary at path,
// without running it.
func ReadFile(path string) (Info, error) {
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	return fromBuild(bi, ""), nil
}

// fromBuild builds Info from Go's embedded build information, nil when there
// is none; goVersion is used when bi names no Go version.
func fromBuild(bi *debug.BuildInfo, goVersion string) Info {
	info := Info{Version: Devel, Go: goVersion}
	if bi == nil {
		return info
	}
	if bi.Main.Version != "" {
		info.Version = bi.Main.Version
	}
	if bi.GoVersion != "" {
		info.Go = bi.GoVersion
	}
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	// A modified flag without its commit describes a build it can't identify.
	info.Modified = info.Commit != "" && modified
	return info
}
