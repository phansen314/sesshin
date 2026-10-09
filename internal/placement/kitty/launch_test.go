package kitty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/placement"
)

// recordingKitten puts a kitten on PATH that writes its arguments, one per
// line, to the file it returns, then runs script.
func recordingKitten(t *testing.T, script string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "args")
	fakeKitten(t, `for a in "$@"; do printf '%s\n' "$a" >> `+log+`; done
`+script)
	return log
}

func recorded(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

const testSocket = "unix:/run/kitty-$KITTY_PID"

func baseSpec() placement.LaunchSpec {
	return placement.LaunchSpec{
		Caller: PlacementOf(testSocket, 3),
		Type:   "tab",
		Cwd:    "/work/api",
		Title:  "api",
		Vars:   []placement.Var{{Name: "project", Value: "api"}, {Name: "note", Value: "a=b,c"}},
		Env:    []placement.Var{{Name: "SESSHIN_JOB", Value: "api"}, {Name: "SESSHIN_TOKEN", Value: "00ff"}},
		Argv:   []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--", "-x"},
	}
}

func TestLaunchArgs(t *testing.T) {
	program := []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--", "-x"}

	tab := []string{"@", "--to", "unix:/run/kitty-$KITTY_PID", "launch", "--type=tab", "--self", "--keep-focus",
		"--cwd=/work/api", "--tab-title=api", "--var=project=api", "--var=note=a=b,c"}
	tab = append(tab, "--env=SESSHIN_JOB=api", "--env=SESSHIN_TOKEN=00ff")
	tab = append(tab, program...)
	if got := launchArgs(testSocket, baseSpec()); !slices.Equal(got, tab) {
		t.Errorf("tab:\n got %q\nwant %q", got, tab)
	}

	// An OS window takes the title too; a split is kitty's window, and keeps
	// its tab's title.
	osw := baseSpec()
	osw.Type = "os-window"
	if got := launchArgs(testSocket, osw); !slices.Contains(got, "--type=os-window") || !slices.Contains(got, "--tab-title=api") {
		t.Errorf("os-window: %q", got)
	}
	split := baseSpec()
	split.Type = "split"
	got := launchArgs(testSocket, split)
	if !slices.Contains(got, "--type=window") || slices.Contains(got, "--type=split") {
		t.Errorf("split's type: %q", got)
	}
	if slices.ContainsFunc(got, func(a string) bool { return strings.HasPrefix(a, "--tab-title") }) {
		t.Errorf("split has a tab title: %q", got)
	}

	// No title, no job: no --tab-title, and no --env at all.
	bare := baseSpec()
	bare.Title, bare.Vars, bare.Env = "", nil, nil
	got = launchArgs(testSocket, bare)
	want := []string{"@", "--to", "unix:/run/kitty-$KITTY_PID", "launch", "--type=tab", "--self", "--keep-focus", "--cwd=/work/api"}
	want = append(want, program...)
	if !slices.Equal(got, want) {
		t.Errorf("bare:\n got %q\nwant %q", got, want)
	}

	// A title or a variable that begins with "-" is a value, never an option.
	dash := baseSpec()
	dash.Title = "-x"
	dash.Vars = []placement.Var{{Name: "k", Value: "--copy-env"}}
	got = launchArgs(testSocket, dash)
	if !slices.Contains(got, "--tab-title=-x") || !slices.Contains(got, "--var=k=--copy-env") {
		t.Errorf("dash values: %q", got)
	}
	if slices.Contains(got, "--copy-env") {
		t.Errorf("--copy-env as an option: %q", got)
	}
}

func TestLaunch(t *testing.T) {
	log := recordingKitten(t, `echo 42`)
	id, err := Launch(baseSpec())
	if err != nil || id != 42 {
		t.Fatalf("got %d, %v", id, err)
	}
	if got := recorded(t, log); !slices.Equal(got, launchArgs(testSocket, baseSpec())) {
		t.Errorf("kitten got %q", got)
	}
}

func TestLaunchID(t *testing.T) {
	for name, tc := range map[string]struct {
		script  string
		id      int64
		unknown bool
	}{
		"no newline":        {`printf 7`, 7, false},
		"one newline":       {`echo 7`, 7, false},
		"two newlines":      {`printf '7\n\n'`, 0, true},
		"empty":             {`:`, 0, true},
		"text":              {`echo hello`, 0, true},
		"zero":              {`echo 0`, 0, true},
		"negative":          {`echo -3`, 0, true},
		"fraction":          {`echo 1.5`, 0, true},
		"leading zero":      {`echo 07`, 0, true},
		"plus":              {`echo +7`, 0, true},
		"spaces":            {`echo ' 7'`, 0, true},
		"a JSON object":     {`echo '{"id":7}'`, 0, true},
		"nonzero exit":      {`echo 7; exit 1`, 0, false},
		"error on stderr":   {`echo "unknown option" >&2; exit 1`, 0, false},
		"a window and exit": {`echo 7; exit 2`, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			fakeKitten(t, tc.script)
			id, err := Launch(baseSpec())
			if tc.id != 0 {
				if err != nil || id != tc.id {
					t.Fatalf("got %d, %v; want %d", id, err, tc.id)
				}
				return
			}
			var e *placement.LaunchError
			if !errors.As(err, &e) || id != 0 {
				t.Fatalf("got %d, %v; want a *placement.LaunchError", id, err)
			}
			if e.Unknown != tc.unknown || placement.IsUnknown(err) != tc.unknown {
				t.Errorf("Unknown %v for %v, want %v", e.Unknown, err, tc.unknown)
			}
		})
	}
}

func TestLaunchErrorDetail(t *testing.T) {
	fakeKitten(t, `echo "Error: no such socket" >&2
echo "more" >&2
exit 1`)
	_, err := Launch(baseSpec())
	if err == nil || !strings.Contains(err.Error(), "no such socket") || strings.Contains(err.Error(), "more") {
		t.Errorf("error %v", err)
	}
}

func TestLaunchNoKitten(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // never a real kitten
	_, err := Launch(baseSpec())
	var e *placement.LaunchError
	if !errors.As(err, &e) || e.Unknown || placement.IsUnknown(err) {
		t.Errorf("error %v: a missing kitten opened nothing", err)
	}
}

// A kitten that hangs is given up on at the limit, and a window may have
// opened.
func TestLaunchHang(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip(err)
	}
	fakeKitten(t, "exec "+sleep+" 5")
	defer func(d time.Duration) { launchLimit = d }(launchLimit)
	launchLimit = 200 * time.Millisecond
	start := time.Now()
	id, err := Launch(baseSpec())
	if !placement.IsUnknown(err) || id != 0 {
		t.Errorf("got %d, %v", id, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestLaunchLimit(t *testing.T) {
	if launchLimit != 10*time.Second {
		t.Errorf("launch limit %v, want 10s", launchLimit)
	}
}

func TestPlacementOf(t *testing.T) {
	p := PlacementOf("unix:/x", 7)
	if got, ok := Parse(p); !ok || got.Socket != "unix:/x" || got.WindowID != 7 {
		t.Errorf("%v, %v", got, ok)
	}
	want := Recognize(func(k string) string {
		return map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "7"}[k]
	})
	if enc(t, p) != enc(t, want) {
		t.Errorf("%s differs from Recognize's %s", enc(t, p), enc(t, want))
	}
}
