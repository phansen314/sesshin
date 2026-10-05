package ops

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
)

// fixture is a Setup over a temporary HOME, with a stubbed self-test.
type fixture struct {
	t    *testing.T
	home string
	env  map[string]string
	s    Setup

	selfTests []string // the sesshin-hooks the self-test was asked about
	selfTest  *Error   // what the stubbed self-test returns
}

const (
	fixtureHook  = "/opt/sesshin/bin/sesshin-hook"
	fixtureBuild = "v0.0.0-20261003190000-0a2ed27a1b2c"
)

var fixtureNow = time.Date(2026, 10, 3, 19, 30, 5, 600, time.UTC)

// newFixture's HOME holds a space and a quote, so every path the operations
// print needs quoting.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), "my dir's home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, home: home, env: map[string]string{"HOME": home}}
	f.s = Setup{
		FS:         fsys.OS{},
		GOOS:       "linux",
		Getenv:     func(k string) string { return f.env[k] },
		Executable: func() (string, error) { return "/opt/sesshin/bin/sesshin", nil },
		Now:        func() time.Time { return fixtureNow },
		Build:      buildinfo.Info{Version: fixtureBuild, Commit: "0a2ed27a1b2c", Go: "go1.26.8"},
		SelfTest: func(hook string, _ buildinfo.Info) *Error {
			f.selfTests = append(f.selfTests, hook)
			return f.selfTest
		},
	}
	return f
}

func (f *fixture) config() string   { return filepath.Join(f.home, ".config", "sesshin") }
func (f *fixture) state() string    { return filepath.Join(f.home, ".local", "state", "sesshin") }
func (f *fixture) settings() string { return filepath.Join(f.home, ".claude", "settings.json") }

// write writes a file under the temporary HOME, making its directory.
func (f *fixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(path string) string {
	f.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// snapshot is every file under dir and its content ("" for a directory), to
// compare before and after.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[p] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// result decodes an envelope's result, after checking the envelope against
// the schemas.
func result[T any](t *testing.T, env Envelope, output string) T {
	t.Helper()
	checkEnvelope(t, env, output)
	if !env.OK {
		t.Fatalf("failed: %+v", env.Error)
	}
	b, err := json.Marshal(env.Result)
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// failure checks a failed envelope against the schemas and returns its error,
// after checking its kind.
func failure(t *testing.T, env Envelope, kind string) *Error {
	t.Helper()
	checkEnvelope(t, env, "install-output")
	if env.OK || env.Error.Kind != kind {
		t.Fatalf("want %s, got %+v (ok %v)", kind, env.Error, env.OK)
	}
	return env.Error
}

func installFile(t *testing.T, path string) model.InstallFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	in, r := model.ReadInstall(b)
	if !r.Usable {
		t.Fatalf("install.json unusable: %s\n%s", r.Reason(), b)
	}
	return in
}

// sh runs a shell command, as the user would with apply's output.
func sh(t *testing.T, command string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", command, err, out)
	}
	return string(out)
}
