package config

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
)

func TestParseGood(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     Config
	}{
		{"empty", "", Default()},
		{"comment only", "# nothing\n", Default()},
		{"spec example", "retain_days           = 30\nretain_headless_hours = 24\n", Default()},
		{"one key", "retain_days = 7", Config{RetainDays: 7, RetainHeadlessHours: 24}},
		{"zeros", "retain_days = 0\nretain_headless_hours = 0", Config{}},
		{"max", "retain_days = 9223372036854775807", Config{RetainDays: math.MaxInt64, RetainHeadlessHours: 24}},
		{"TOML integer forms", "retain_days = 1_000\nretain_headless_hours = 0x10", Config{RetainDays: 1000, RetainHeadlessHours: 16}},
		{"spawn_shell", `spawn_shell = ["/bin/zsh", "-l", "-i"]`, Config{RetainDays: 30, RetainHeadlessHours: 24, Shell: []string{"/bin/zsh", "-l", "-i"}}},
		{"spawn_shell alone", `spawn_shell = ["/usr/bin/fish"]`, Config{RetainDays: 30, RetainHeadlessHours: 24, Shell: []string{"/usr/bin/fish"}}},
		{"spawn_shell with a flag", "spawn_shell = [\"/bin/sh\", \"-c\"]", Config{RetainDays: 30, RetainHeadlessHours: 24, Shell: []string{"/bin/sh", "-c"}}},
	} {
		got, detail := Parse([]byte(c.in))
		if detail != "" || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Parse(%q) = %v, %q; want %v", c.name, c.in, got, detail, c.want)
		}
	}
}

func TestParseCorrupt(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     []string
	}{
		{"syntax", "retain_days = ", []string{"line 1"}},
		{"float", "retain_days = 30.0", []string{"incompatible types"}},
		{"string", `retain_days = "30"`, []string{"incompatible types"}},
		{"bool", "retain_days = true", []string{"incompatible types"}},
		{"repeated", "retain_days = 1\nretain_days = 2", []string{"already been defined"}},
		{"negative", "retain_days = -1", []string{"retain_days is -1, below 0"}},
		{"removed key", "unknown_pid_ttl_secs = 86400", []string{`unknown key "unknown_pid_ttl_secs"`}},
		{"unknown", "retain = 1", []string{`unknown key "retain"`}},
		{"in a table", "[sesshin]\nretain_days = 1", []string{`"sesshin"`, `"sesshin.retain_days"`}},
		{"spawn_shell empty", "spawn_shell = []", []string{"spawn_shell is empty"}},
		{"spawn_shell empty string", `spawn_shell = ["/bin/sh", ""]`, []string{"spawn_shell has an empty string"}},
		{"spawn_shell empty first", `spawn_shell = [""]`, []string{"spawn_shell has an empty string"}},
		{"spawn_shell relative", `spawn_shell = ["zsh", "-l"]`, []string{"not an absolute path"}},
		{"spawn_shell a string", `spawn_shell = "/bin/zsh"`, []string{"incompatible types"}},
		{"spawn_shell not strings", `spawn_shell = ["/bin/zsh", 1]`, []string{"incompatible types"}},
		{"too big", "retain_days = 9223372036854775808", []string{"line 1"}},
		{"every problem", "x = 1\nretain_days = -1\nretain_headless_hours = -2",
			[]string{`unknown key "x"`, "retain_days is -1", "retain_headless_hours is -2"}},
	} {
		got, detail := Parse([]byte(c.in))
		if !reflect.DeepEqual(got, Config{}) {
			t.Errorf("%s: config %v alongside a problem", c.name, got)
		}
		for _, w := range c.want {
			if !strings.Contains(detail, w) {
				t.Errorf("%s: detail %q, want it to contain %q", c.name, detail, w)
			}
		}
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	if c, err := Read(fsys.OS{}, path); err != nil || !reflect.DeepEqual(c, Default()) {
		t.Errorf("missing: %v, %v", c, err)
	}
	if err := os.WriteFile(path, []byte("retain_days = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := Read(fsys.OS{}, path); err != nil || c.RetainDays != 3 {
		t.Errorf("good: %v, %v", c, err)
	}
	if err := os.WriteFile(path, []byte("retain_days = -3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(fsys.OS{}, path)
	var ce *CorruptError
	if !errors.As(err, &ce) || ce.Path != path || ce.Detail == "" {
		t.Errorf("corrupt: %v", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if _, err := Read(fsys.OS{}, path); errors.As(err, &ce) || err == nil {
			t.Errorf("unreadable: %v, want the read's error", err)
		}
	}
}

// Every setting saturates: math.MaxInt64 never wraps into a negative
// duration or a cutoff in the future (implementation-spec.md, Settings).
func TestSaturate(t *testing.T) {
	c := Config{RetainDays: math.MaxInt64, RetainHeadlessHours: math.MaxInt64}
	if d := c.RetainHeadless(); d != math.MaxInt64 {
		t.Errorf("RetainHeadless = %v", d)
	}
	if d := Default().RetainHeadless(); d != 24*time.Hour {
		t.Errorf("default RetainHeadless = %v", d)
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, days := range []int64{math.MaxInt64, math.MaxInt32, MaxDays + 1, MaxDays} {
		if got := DaysBefore(now, days); !got.Before(now.AddDate(-2700, 0, 0)) {
			t.Errorf("DaysBefore(%d) = %v, not far in the past", days, got)
		}
	}
	if got, want := DaysBefore(now, 30), time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("DaysBefore(30) = %v, want %v", got, want)
	}
	if got := DaysBefore(now, 0); !got.Equal(now) {
		t.Errorf("DaysBefore(0) = %v", got)
	}
}

// spawn_shell is the configured array, else $SHELL with -l -i, else /bin/sh
// with them (design-spec.md, config.toml).
func TestSpawnShell(t *testing.T) {
	env := func(shell string) func(string) string {
		return func(k string) string {
			if k == "SHELL" {
				return shell
			}
			return ""
		}
	}
	configured := Config{Shell: []string{"/opt/fish", "-l"}}
	for name, tc := range map[string]struct {
		c     Config
		shell string
		want  []string
	}{
		"configured wins":      {configured, "/bin/zsh", []string{"/opt/fish", "-l"}},
		"default is SHELL":     {Default(), "/bin/zsh", []string{"/bin/zsh", "-l", "-i"}},
		"SHELL unset":          {Default(), "", []string{"/bin/sh", "-l", "-i"}},
		"SHELL relative":       {Default(), "zsh", []string{"/bin/sh", "-l", "-i"}},
		"configured, no SHELL": {configured, "", []string{"/opt/fish", "-l"}},
	} {
		if got := tc.c.SpawnShell(env(tc.shell)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	got := configured.SpawnShell(env(""))
	got[0] = "changed"
	if configured.Shell[0] != "/opt/fish" {
		t.Error("SpawnShell returned the config's own slice")
	}
}
