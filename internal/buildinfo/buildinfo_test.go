package buildinfo

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func build(version string, settings ...string) *debug.BuildInfo {
	bi := &debug.BuildInfo{GoVersion: "go1.26.8"}
	bi.Main.Version = version
	for i := 0; i < len(settings); i += 2 {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return bi
}

func TestFromBuild(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bi         *debug.BuildInfo
		want       Info
		identified bool
	}{
		{"no build info", nil, Info{Devel, "", false, "go-running"}, false},
		{"no version, no vcs", build(""), Info{Devel, "", false, "go1.26.8"}, false},
		{"devel, no vcs", build(Devel), Info{Devel, "", false, "go1.26.8"}, false},
		{
			"checkout, clean",
			build("v0.0.0-20261003190000-0a2ed27a1b2c", "vcs.revision", "0a2ed27a1b2c", "vcs.modified", "false"),
			Info{"v0.0.0-20261003190000-0a2ed27a1b2c", "0a2ed27a1b2c", false, "go1.26.8"}, true,
		},
		{
			"checkout, modified",
			build("v0.0.0-20261003190000-0a2ed27a1b2c+dirty", "vcs.revision", "0a2ed27a1b2c", "vcs.modified", "true"),
			Info{"v0.0.0-20261003190000-0a2ed27a1b2c+dirty", "0a2ed27a1b2c", true, "go1.26.8"}, true,
		},
		{"devel with vcs", build(Devel, "vcs.revision", "abc", "vcs.modified", "true"), Info{Devel, "abc", true, "go1.26.8"}, true},
		{"modified without revision", build(Devel, "vcs.modified", "true"), Info{Devel, "", false, "go1.26.8"}, false},
		{"go install, tag", build("v0.1.0"), Info{"v0.1.0", "", false, "go1.26.8"}, true},
		{"go install, pseudo-version", build("v0.0.0-20261003190000-0a2ed27a1b2c"), Info{"v0.0.0-20261003190000-0a2ed27a1b2c", "", false, "go1.26.8"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fromBuild(tc.bi, "go-running")
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
			if got.Identified() != tc.identified {
				t.Errorf("Identified() = %v, want %v", got.Identified(), tc.identified)
			}
		})
	}
}

func TestSame(t *testing.T) {
	a := Info{"v0.1.0", "abc", false, "go1.26.8"}
	for _, tc := range []struct {
		name string
		b    Info
		want bool
	}{
		{"identical", a, true},
		{"other Go", Info{"v0.1.0", "abc", false, "go1.26.9"}, true},
		{"other version", Info{"v0.1.1", "abc", false, "go1.26.8"}, false},
		{"other commit", Info{"v0.1.0", "abd", false, "go1.26.8"}, false},
		{"modified", Info{"v0.1.0", "abc", true, "go1.26.8"}, false},
	} {
		if got := Same(a, tc.b); got != tc.want {
			t.Errorf("%s: Same = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ReadFile on the test binary reads what the running binary reports.
func TestReadFileSelf(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if want := Read(); got != want {
		t.Errorf("ReadFile(self) = %+v, Read() = %+v", got, want)
	}
	if got.Go == "" {
		t.Error("no Go version")
	}
}

func TestReadFileNotGo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path); err == nil {
		t.Error("ReadFile of a shell script succeeded")
	}
	if _, err := ReadFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("ReadFile of a missing file succeeded")
	}
}
