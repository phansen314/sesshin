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

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/ls.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseLS(t *testing.T) {
	ls := fixture(t)
	tests := []struct {
		window int64
		title  string
		vars   string
	}{
		{1, "shell", `{}`},
		{4, "editor", `{"role":"edit"}`},
		{5, "editor", `{}`},                                // no user_vars at all
		{7, "api review", `{"project":"api","env":"dev"}`}, // second OS window, later tab
	}
	for _, tc := range tests {
		got, err := ParseLS(ls, tc.window)
		if err != nil {
			t.Errorf("window %d: %v", tc.window, err)
			continue
		}
		if got.TabTitle != tc.title || enc(t, got.UserVars) != tc.vars {
			t.Errorf("window %d: %q %s, want %q %s", tc.window, got.TabTitle, enc(t, got.UserVars), tc.title, tc.vars)
		}
	}
}

// The tab title and the user variables' values are scrubbed before they are
// stored (design-spec.md, Placement).
func TestParseLSScrubs(t *testing.T) {
	ls := `[{"tabs":[{"title":"a\u001b[31mb\u2028c","windows":[{"id":1,"user_vars":{"k":"x\u0007y"}}]}]}]`
	got, err := ParseLS([]byte(ls), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.TabTitle != model.Scrub("a\x1b[31mb\u2028c") || strings.ContainsAny(got.TabTitle, "\x1b\u2028") {
		t.Errorf("tab title %q", got.TabTitle)
	}
	if v, _ := got.UserVars.Get("k"); v != model.Scrub("x\x07y") || v == "x\x07y" {
		t.Errorf("user var %q", v)
	}
}

func TestParseLSFailures(t *testing.T) {
	ls := string(fixture(t))
	tests := map[string]struct {
		in     string
		window int64
	}{
		"window missing":       {ls, 99},
		"no OS windows":        {`[]`, 1},
		"empty":                {``, 1},
		"malformed":            {`[{"id": 1, "tabs": [`, 1},
		"not JSON":             {`kitty is not running`, 1},
		"trailing data":        {`[] []`, 1},
		"an object":            {`{"tabs": []}`, 1},
		"title not a string":   {`[{"tabs":[{"title":3,"windows":[{"id":1}]}]}]`, 1},
		"title missing":        {`[{"tabs":[{"windows":[{"id":1}]}]}]`, 1},
		"user_vars not object": {`[{"tabs":[{"title":"t","windows":[{"id":1,"user_vars":[]}]}]}]`, 1},
		"user_vars not string": {`[{"tabs":[{"title":"t","windows":[{"id":1,"user_vars":{"a":1}}]}]}]`, 1},
		"id as string":         {`[{"tabs":[{"title":"t","windows":[{"id":"1"}]}]}]`, 1},
		"odd shapes skipped":   {`[1, {"tabs": 2}, {"tabs": [3, {"windows": 4}]}]`, 1},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseLS([]byte(tc.in), tc.window)
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error %v, want an *Error", err)
			}
			if placement.IsTimeout(err) || e.Timeout {
				t.Errorf("error %q is a timeout", err)
			}
			if strings.Contains(err.Error(), "kitty sync") {
				t.Errorf("error %q has a package prefix", err)
			}
		})
	}
}

func TestIsTimeout(t *testing.T) {
	if !placement.IsTimeout(&Error{Timeout: true, Err: errors.New("x")}) {
		t.Error("a timeout is not")
	}
	if placement.IsTimeout(&Error{Err: errors.New("x")}) {
		t.Error("another failure is")
	}
	if placement.IsTimeout(nil) || placement.IsTimeout(errors.New("x")) {
		t.Error("a foreign error is")
	}
}

func TestApply(t *testing.T) {
	p := obj(t, bare)
	s := Synced{TabTitle: "api", UserVars: obj(t, `{"p":"a"}`)}
	got, changed := Apply(p, s)
	if !changed || enc(t, got) != synced {
		t.Errorf("got %s, %v", enc(t, got), changed)
	}
	if enc(t, p) != bare {
		t.Error("Apply changed its argument")
	}
	if again, changed := Apply(got, s); changed || again != got {
		t.Error("same keys reported as a change")
	}
	got2, changed := Apply(got, Synced{TabTitle: "other", UserVars: obj(t, `{}`)})
	if !changed || enc(t, got2) != `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"other","user_vars":{}}` {
		t.Errorf("update: %s", enc(t, got2))
	}
	bad := obj(t, `{"terminal":"kitty","window_id":7}`)
	if out, changed := Apply(bad, s); changed || out != bad {
		t.Error("an invalid placement was written to")
	}
	if out, changed := Apply(nil, s); changed || out != nil {
		t.Error("nil was written to")
	}
}

// With no kitten on PATH, Sync fails silently.
func TestSyncNoKitten(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // never a real kitten
	_, err := Sync("unix:/x", 7)
	var e *Error
	if !errors.As(err, &e) || e.Timeout || placement.IsTimeout(err) {
		t.Errorf("error %v", err)
	}
}

func TestParseWindows(t *testing.T) {
	got, err := ParseWindows(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 4, 5, 7}; !slices.Equal(got, want) {
		t.Errorf("windows %v, want %v", got, want)
	}
	// A kitty with no windows answers: every window is gone.
	got, err = ParseWindows([]byte(`[]`))
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty list: %v, %v", got, err)
	}
}

func TestParseWindowsFailures(t *testing.T) {
	for name, in := range map[string]string{
		"empty":                   ``,
		"not JSON":                `kitty is not running`,
		"malformed":               `[{"id": 1, "tabs": [`,
		"an object":               `{"tabs": []}`,
		"id missing":              `[{"tabs":[{"windows":[{}]}]}]`,
		"id as string":            `[{"tabs":[{"windows":[{"id":"1"}]}]}]`,
		"id zero":                 `[{"tabs":[{"windows":[{"id":0}]}]}]`,
		"id fractional":           `[{"tabs":[{"windows":[{"id":1.5}]}]}]`,
		"OS window not an object": `[1]`,
		"tabs missing":            `[{"id": 1}]`,
		"tabs not a list":         `[{"tabs": 2}]`,
		"tab not an object":       `[{"tabs": [3]}]`,
		"windows not a list":      `[{"tabs": [{"windows": 4}]}]`,
		"trailing data":           `[] junk`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWindows([]byte(in))
			var e *Error
			if !errors.As(err, &e) || e.Timeout || got != nil {
				t.Errorf("got %v, %v; want an *Error that is no timeout", got, err)
			}
		})
	}
}

// fakeKitten puts a kitten that runs script on PATH.
func fakeKitten(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kitten"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestWindows(t *testing.T) {
	fakeKitten(t, `[ "$1 $2" = "@ --to" ] && [ "$3" = "unix:/x" ] && [ "$4" = "ls" ] || exit 2
echo '[{"tabs":[{"title":"t","windows":[{"id":3},{"id":9}]}]}]'`)
	got, err := Windows("unix:/x")
	if err != nil || !slices.Equal(got, []int64{3, 9}) {
		t.Errorf("windows %v, %v", got, err)
	}
}

func TestWindowsFailures(t *testing.T) {
	for name, script := range map[string]string{
		"nonzero exit":    `exit 1`,
		"not kitten @ ls": `echo hello`,
	} {
		t.Run(name, func(t *testing.T) {
			fakeKitten(t, script)
			got, err := Windows("unix:/x")
			var e *Error
			if !errors.As(err, &e) || e.Timeout || got != nil {
				t.Errorf("got %v, %v", got, err)
			}
		})
	}
	t.Run("no kitten", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if got, err := Windows("unix:/x"); err == nil || got != nil || placement.IsTimeout(err) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		sleep, err := exec.LookPath("sleep")
		if err != nil {
			t.Skip(err)
		}
		fakeKitten(t, "exec "+sleep+" 5")
		start := time.Now()
		got, err := Windows("unix:/x")
		if !placement.IsTimeout(err) || got != nil {
			t.Errorf("got %v, %v", got, err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("took %v", d)
		}
	})
}
