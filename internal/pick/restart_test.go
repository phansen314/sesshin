package pick

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/schematest"
)

// The lines: columns, End words, relative times, home as ~, padding.
func TestLines(t *testing.T) {
	f := newFixture(t)
	f.add(12, 3*time.Minute, killed, title("api review"), cwdOf(f.home+"/code/api"))
	f.sesshin(12, "api", "")
	f.add(9, 2*time.Hour, cwdOf(f.home+"/code/sesshin"), title("spec resume"))
	f.add(7, 25*time.Hour, reason("clear"), cwdOf("/tmp/x"))
	f.sesshin(7, "docs", "")
	f.add(4, 6*24*time.Hour, killed, noTranscript, cwdOf(f.home))
	f.write(uuid(4), "sesshin.json", `{"schema": 2,"id":null,"job":null,"source":"hook","placement":null,"extra":{}}`)
	f.selects(1)

	env := f.restart(Input{Query: "killed"})
	if len(actions(t, env)) != 0 {
		t.Fatalf("%+v", env)
	}
	// In session order: most recently seen first.
	want := []string{
		uuid(12) + "\t#12  api   killed                  3m ago  ~/code/api      api review",
		uuid(9) + "\t#9   —     exited                  2h ago  ~/code/sesshin  spec resume",
		uuid(7) + "\t#7   docs  cleared                 1d ago  /tmp/x          #7",
		uuid(4) + "\t—    —     killed transcript-gone  6d ago  ~               " + uuid(4)[:8],
	}
	got := f.stdin()
	if !slices.Equal(got, want) {
		t.Errorf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !slices.Contains(f.argv(), "killed") || f.catches != 1 {
		t.Errorf("argv %q, interrupts caught %d times", f.argv(), f.catches)
	}
}

// Headless sessions and live ones are not candidates.
func TestCandidates(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Hour)
	f.add(2, time.Hour, headless)
	f.add(3, time.Hour, func(l *model.LifecycleFile) { l.EndedAt, l.EndReason = nil, nil })
	f.selects(1)
	f.restart(Input{})
	if got := f.stdin(); len(got) != 1 || !strings.HasPrefix(got[0], uuid(1)+"\t") {
		t.Errorf("lines %q", got)
	}
}

func TestEndWord(t *testing.T) {
	s := func(v string) *string { return &v }
	ts := model.Timestamp("2026-10-04T10:00:00.000Z")
	for _, tc := range []struct {
		ended  *model.Timestamp
		reason *string
		want   string
	}{
		{nil, nil, "killed"},
		{nil, s("clear"), "killed"},
		{nil, s("superseded"), "superseded"},
		{&ts, s("other"), "killed"},
		{&ts, s("prompt_input_exit"), "exited"},
		{&ts, s("clear"), "cleared"},
		{&ts, s("resume"), "resumed"},
		{&ts, s("superseded"), "superseded"},
		{&ts, s("logout"), "logout"},
		{&ts, s("bypass_permissions_disabled"), "bypass_permissions_disabled"},
		{&ts, s("a\tb"), "a b"},
		{&ts, nil, "ended"},
	} {
		got := endWord(ops.SessionView{EndedAt: tc.ended, EndReason: tc.reason})
		if got != tc.want {
			t.Errorf("ended %v, reason %v: %q, want %q", tc.ended != nil, tc.reason, got, tc.want)
		}
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Hour:                    "0s ago",
		0:                             "0s ago",
		59 * time.Second:              "59s ago",
		time.Minute:                   "1m ago",
		59*time.Minute + time.Second:  "59m ago",
		time.Hour:                     "1h ago",
		23*time.Hour + 59*time.Minute: "23h ago",
		24 * time.Hour:                "1d ago",
		400 * 24 * time.Hour:          "400d ago",
	} {
		if got := ago(d); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTilde(t *testing.T) {
	for path, want := range map[string]string{
		"/home/p":         "~",
		"/home/p/code":    "~/code",
		"/home/pp/code":   "/home/pp/code",
		"/tmp/home/p":     "/tmp/home/p",
		"/home/p/":        "~/",
		"relative/home/p": "relative/home/p",
	} {
		if got := tilde(path, "/home/p"); got != want {
			t.Errorf("tilde(%q) = %q, want %q", path, got, want)
		}
	}
	if got := tilde("/x", ""); got != "/x" {
		t.Errorf("no home: %q", got)
	}
}

// A future last seen counts as 0s ago.
func TestFutureSeen(t *testing.T) {
	f := newFixture(t)
	f.add(1, -time.Hour)
	f.selects(1)
	f.restart(Input{})
	if got := f.stdin(); len(got) != 1 || !strings.Contains(got[0], "0s ago") {
		t.Errorf("lines %q", got)
	}
}

// Columns are padded by display width: wide characters take two cells.
func TestPaddingWide(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Minute, cwdOf("/tmp/日本語"), title("a"))
	f.add(2, 2*time.Minute, cwdOf("/tmp/ab"), title("b"))
	f.add(3, 3*time.Minute, cwdOf("/tmp/é"), title("c"))
	f.selects(1)
	f.restart(Input{})
	var widths []int
	for _, line := range f.stdin() {
		_, shown, _ := strings.Cut(line, "\t")
		shown = shown[:strings.LastIndex(shown, "  ")] // without the name
		widths = append(widths, runewidth.StringWidth(shown))
	}
	if len(widths) != 3 || widths[0] != widths[1] || widths[1] != widths[2] {
		t.Errorf("widths %v in %q", widths, f.stdin())
	}
}

// A title with a tab, a newline, line separators, an escape, or a forged
// key stays on one line, under its own key. (A file with such a title is
// unusable, so this is the renderer's own defence.)
func TestHostileNames(t *testing.T) {
	forged := uuid(99)
	hostile := "x\t" + forged + "\tpwn\nnext" + uuid(98) + " y z\x1b[31m\x00\x7f\u0085"
	cwd, job, reason := "/tmp/a\tb\nc", "j\tk", "e\tvil"
	ended := model.Timestamp("2026-10-04T10:00:00.000Z")
	views := []ops.SessionView{
		{SessionID: uuid(1), Name: hostile, Cwd: &cwd, Job: &job, EndReason: &reason, EndedAt: &ended, LastSeen: ended},
		{SessionID: uuid(2), Name: "plain", LastSeen: ended},
	}
	lines := renderLines(views, now, "/home/p")
	if len(lines) != 2 {
		t.Fatalf("%d lines", len(lines))
	}
	for i, line := range lines {
		key, rest, _ := strings.Cut(line, "\t")
		if key != uuid(i+1) || strings.ContainsAny(rest, "\t\n\r\x1b\x00\x7f\u0085  ") {
			t.Errorf("line %q", line)
		}
	}
	if !strings.HasSuffix(lines[0], "x "+forged+" pwn next"+uuid(98)+" y z [31m   ") {
		t.Errorf("name not scrubbed to spaces: %q", lines[0])
	}
	p := preview(views[0], now)
	if strings.Count(p, "\n") != 15 || strings.ContainsAny(strings.ReplaceAll(p, "\n", ""), "\t\x1b\x00\x7f\u0085  ") {
		t.Errorf("preview:\n%q", p)
	}
}

// Keys fzf prints that were not offered are ignored, the forged one among
// them.
func TestForgedKey(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Minute)
	f.add(2, 2*time.Minute)
	f.selects(0, uuid(99), uuid(1)+"\t"+uuid(2), uuid(2))
	a := actions(t, f.restart(Input{}))
	if len(a) != 2 || a[0].Input.Session != uuid(1) || a[1].Input.Session != uuid(2) {
		t.Errorf("actions %+v", a)
	}
}

// The preview: one file per candidate in a private directory, removed
// afterwards, and the preview command reads it.
func TestPreview(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Minute, title("api review"), func(l *model.LifecycleFile) {
		b, m := "main-ish", "opus"
		l.Model, l.Compactions = &m, 2
		_ = b
	})
	f.sesshin(1, "api", `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":"the tab","user_vars":{"a":"b"}}`)
	f.add(2, time.Hour, killed, noTranscript)
	f.selects(1)
	f.restart(Input{})

	dir := filepath.Join(f.fzf, "previews")
	for _, n := range []int{1, 2} {
		if _, err := os.Stat(filepath.Join(dir, uuid(n))); err != nil {
			t.Error(err)
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Errorf("%d files in the preview directory", len(ents))
	}
	p := f.recorded("previews/" + uuid(1))
	for _, want := range []string{"sesshin ID:", "#1\n", "name:", "api review\n", "job:", "api\n", "cwd:", f.cwd, "model:", "opus", "compactions:", "2\n",
		"started_at:", "last seen:", "(1m ago)", "ended_at:", "end_reason:", "prompt_input_exit", "cost:", "transcript:", "(exists)", "tab title:", "the tab"} {
		if !strings.Contains(p, want) {
			t.Errorf("preview lacks %q:\n%s", want, p)
		}
	}
	if p2 := f.recorded("previews/" + uuid(2)); !strings.Contains(p2, "(transcript-gone)") || !strings.Contains(p2, "tab title:       —") {
		t.Errorf("preview of the second:\n%s", p2)
	}
	// The command fzf runs prints the first line's file.
	if out := f.recorded("preview-out"); out != p {
		t.Errorf("preview command printed:\n%s\nwant:\n%s", out, p)
	}
	if m := strings.TrimSpace(f.recorded("mode")); m != "drwx------" {
		t.Errorf("directory mode %s", m)
	}
	if left, _ := filepath.Glob(filepath.Join(f.run, "sesshin-restart-*")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	// The command quotes the directory (its name has a space and a quote) and
	// leaves {1} to fzf.
	argv := f.argv()
	i := slices.Index(argv, "--preview")
	if i < 0 || !strings.HasPrefix(argv[i+1], "cat -- '") || !strings.HasSuffix(argv[i+1], "'/{1}") || !strings.Contains(argv[i+1], `dir'\''s/sesshin-restart-`) {
		t.Errorf("preview command %q", argv[i+1])
	}
}

// The preview directory is removed when fzf fails or is cancelled, too.
func TestPreviewRemovedOnEveryOutcome(t *testing.T) {
	for _, status := range []int{0, 1, 2, 130} {
		f := newFixture(t)
		f.add(1, time.Minute)
		f.selects(status)
		f.restart(Input{})
		if left, _ := filepath.Glob(filepath.Join(f.run, "sesshin-restart-*")); len(left) != 0 {
			t.Errorf("status %d left behind: %v", status, left)
		}
	}
}

// A relative temp directory is made absolute for the preview command.
func TestTempBase(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, tc := range []struct {
		vars map[string]string
		want string
	}{
		{map[string]string{"XDG_RUNTIME_DIR": "/run/user/1", "TMPDIR": "/t"}, "/run/user/1"},
		{map[string]string{"TMPDIR": "/t"}, "/t"},
		{nil, "/tmp"},
	} {
		if got, err := tempBase(get(tc.vars)); err != nil || got != tc.want {
			t.Errorf("%v: %q, %v", tc.vars, got, err)
		}
	}
	got, err := tempBase(get(map[string]string{"XDG_RUNTIME_DIR": "rel"}))
	if err != nil || !filepath.IsAbs(got) || filepath.Base(got) != "rel" {
		t.Errorf("relative: %q, %v", got, err)
	}
}

// The options: the undone ones after FZF_DEFAULT_OPTS, then the picker's
// own, then SESSHIN_PICK_OPTS; and fzf --version without FZF_DEFAULT_OPTS.
func TestOptions(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Minute)
	f.environ = []string{
		"FZF_DEFAULT_OPTS=--select-1 --exit-0 --expect=esc --print-query",
		"FZF_DEFAULT_OPTS_FILE=/nonexistent",
		"SESSHIN_PICK_OPTS=--height 60% --layout reverse # a comment",
	}
	f.selects(1)
	f.restart(Input{Query: "with space"})

	want := []string{
		"--no-select-1", "--no-exit-0", "--no-expect", "--no-tmux", "--no-read0", "--no-header-lines", "--no-print0", "--no-print-query", "--accept-nth", "..",
		"--with-shell", "sh -c", "--multi", "--delimiter", "\t", "--with-nth", "2..", "--tiebreak", "index",
	}
	argv := f.argv()
	if !slices.Equal(argv[:len(want)], want) {
		t.Errorf("argv %q", argv)
	}
	rest := argv[len(want):]
	if len(rest) < 2 || rest[0] != "--preview" {
		t.Errorf("rest %q", rest)
	}
	tail := []string{"--bind", "ctrl-a:select-all", "--query", "with space", "--height", "60%", "--layout", "reverse"}
	if !slices.Equal(rest[2:], tail) {
		t.Errorf("rest %q, want ... %q", rest, tail)
	}
	// The environment is passed on, for fzf to read the person's options.
	env := f.recorded("env")
	for _, kv := range f.environ[:2] {
		if !strings.Contains(env, kv+"\n") {
			t.Errorf("fzf's environment lacks %s", kv)
		}
	}
	// But the version is asked for without them.
	ve := f.recorded("version-env")
	if ve == "" || strings.Contains(ve, "FZF_DEFAULT_OPTS") || !strings.Contains(ve, "FAKE_FZF_DIR=") {
		t.Errorf("version run's environment:\n%s", ve)
	}
}

// Each outcome of the table.
func TestOutcomes(t *testing.T) {
	setup := func() *fixture {
		f := newFixture(t)
		f.add(1, time.Minute)
		f.add(2, 2*time.Minute)
		return f
	}

	t.Run("enter", func(t *testing.T) {
		f := setup()
		f.selects(0, uuid(2)+"\tmore fields", uuid(2)) // a repeated key counts once
		a := actions(t, f.restart(Input{Args: []string{"--model", "opus"}}))
		if len(a) != 1 || a[0].Operation != "resume" || a[0].Input.Session != uuid(2) || !a[0].Output.OK {
			t.Fatalf("%+v", a)
		}
		if got := a[0].Input; got.StartTimeoutSecs != 0 || !slices.Equal(got.Args, []string{"--model", "opus"}) {
			t.Errorf("input %+v", got)
		}
		if len(f.launches) != 1 || !slices.Equal(f.launches[0].Argv[len(f.launches[0].Argv)-4:], []string{"--resume", uuid(2), "--model", "opus"}) {
			t.Errorf("launches %+v", f.launches)
		}
	})
	t.Run("nothing matched", func(t *testing.T) {
		f := setup()
		f.selects(1)
		env := f.restart(Input{})
		if a := actions(t, env); len(a) != 0 || len(f.launches) != 0 {
			t.Errorf("%+v, %d launches", a, len(f.launches))
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		f := setup()
		f.selects(130, uuid(1)) // whatever it printed
		env := f.restart(Input{})
		wantError(t, env, KindCancelled, "")
		if len(env.Error.Details) != 0 || len(f.launches) != 0 {
			t.Errorf("%+v, %d launches", env.Error, len(f.launches))
		}
	})
	t.Run("any other exit", func(t *testing.T) {
		for _, status := range []int{2, 3, 127, 255} {
			f := setup()
			f.selects(status, uuid(1))
			env := f.restart(Input{})
			wantError(t, env, KindUnavailable, "fzf-failed")
			if env.Error.Details["status"] != status || len(f.launches) != 0 {
				t.Errorf("status %d: %+v, %d launches", status, env.Error, len(f.launches))
			}
		}
	})
	t.Run("exit 0 with nothing offered", func(t *testing.T) {
		f := setup()
		f.selects(0, "", "nonsense", uuid(3))
		if a := actions(t, f.restart(Input{})); len(a) != 0 {
			t.Errorf("%+v", a)
		}
	})
	t.Run("fzf cannot start", func(t *testing.T) {
		f := setup()
		if err := os.WriteFile(filepath.Join(f.fzf, "fzf"), []byte("#!/nonexistent/interpreter\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		f.setFile("version-status", "0") // --version runs the same file
		env := f.env()
		env.Sys.Output = func(string, []string, []string) ([]byte, []byte, int, error) { return []byte("0.74.4\n"), nil, 0, nil }
		got := Restart(Input{Args: []string{}}, env)
		wantError(t, got, KindUnavailable, "fzf-failed")
		if _, has := got.Error.Details["status"]; has {
			t.Errorf("a start failure has no status: %+v", got.Error)
		}
	})
}

// Picks resume in line order, not the order fzf printed them; a failing
// resume doesn't stop the rest, and each is reported as resume's own
// envelope.
func TestResumesInLineOrder(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Minute)
	f.sesshin(1, "api", "")
	f.add(2, 2*time.Minute)
	f.sesshin(2, "api", "")
	f.add(3, 3*time.Minute)
	f.add(4, 4*time.Minute)
	f.selects(0, uuid(4), uuid(2), uuid(1), uuid(3))
	a := actions(t, f.restart(Input{}))
	var order []string
	for _, x := range a {
		order = append(order, x.Input.Session)
	}
	if want := []string{uuid(1), uuid(2), uuid(3), uuid(4)}; !slices.Equal(order, want) {
		t.Fatalf("order %v", order)
	}
	// 1 resumed under api; 2 wanted it too; 3 and 4 have none.
	if !a[0].Output.OK {
		t.Errorf("first: %+v", a[0].Output.Error)
	}
	if e := a[1].Output.Error; a[1].Output.OK || e.Kind != ops.KindConflict || e.Details["rule"] != "job-taken" {
		t.Errorf("second: %+v", a[1].Output)
	}
	if !a[2].Output.OK || !a[3].Output.OK || len(f.launches) != 3 {
		t.Errorf("rest: %+v %+v, %d launches", a[2].Output, a[3].Output, len(f.launches))
	}
	for _, l := range f.launches {
		if l.Type != "tab" {
			t.Errorf("launch %+v", l)
		}
	}
}

// A resume that fails for another reason is reported, and the exit is still
// a success.
func TestResumeFailureIsAnAction(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Minute, cwdOf("/nonexistent/dir"))
	f.add(2, 2*time.Minute)
	f.selects(0, uuid(1), uuid(2))
	a := actions(t, f.restart(Input{}))
	if len(a) != 2 || a[0].Output.OK || a[0].Output.Error.Kind != ops.KindNotFound || !a[1].Output.OK {
		t.Errorf("%+v", a)
	}
}

// The load's warnings are the envelope's; each resume's stay in its output.
func TestWarnings(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Minute, noTranscript)
	f.write(uuid(5), "lifecycle.json", "not json")
	f.selects(0, uuid(1))
	env := f.restart(Input{})
	if len(env.Warnings) != 1 || env.Warnings[0].Kind != "unusable-file" {
		t.Errorf("warnings %+v", env.Warnings)
	}
	a := actions(t, env)
	var kinds []string
	for _, w := range a[0].Output.Warnings {
		kinds = append(kinds, w.Kind)
	}
	if len(a) != 1 || !slices.Contains(kinds, ops.KindTranscriptMissing) {
		t.Errorf("%+v", a)
	}
	// And they go with a failure after the load.
	f.noTTY = true
	env = f.restart(Input{})
	wantError(t, env, KindUnavailable, "no-terminal")
	if len(env.Warnings) != 1 {
		t.Errorf("warnings %+v", env.Warnings)
	}
}

// No candidates: ok, no actions, and fzf is not run, nor a terminal needed.
func TestNoCandidates(t *testing.T) {
	f := newFixture(t)
	f.noTTY = true
	f.add(1, time.Minute, headless)
	f.selects(0, uuid(1))
	if a := actions(t, f.restart(Input{})); len(a) != 0 || f.ran() {
		t.Errorf("%+v, ran %v", a, f.ran())
	}
	f2 := newFixture(t) // no sessions at all, no state directory
	f2.noTTY = true
	if a := actions(t, f2.restart(Input{})); len(a) != 0 || f2.ran() {
		t.Errorf("%+v, ran %v", a, f2.ran())
	}
}

// The Errors order: invalid-input, environment, corrupt, unavailable (fzf),
// terminal, the load's, unavailable (no-terminal), then fzf's outcome.
func TestErrorOrder(t *testing.T) {
	f := newFixture(t)
	f.add(1, time.Minute)
	f.selects(1)

	// invalid-input comes before everything.
	_, e := ops.DecodeInput([]byte(`{"query":1,"args":"x","other":true}`), DecodeInput)
	if e == nil || e.Kind != ops.KindInvalidInput {
		t.Fatalf("%+v", e)
	}

	// Everything wrong at once, undone one at a time.
	f.noFzf, f.noTTY = true, true
	delete(f.vars, "KITTY_LISTEN_ON")
	f.environ = []string{"SESSHIN_PICK_OPTS='unterminated"}
	cfg := filepath.Join(f.loc.ConfigDir, "config.toml")
	if err := os.MkdirAll(f.loc.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("retain_days = [oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.loc.SessionsDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.loc.SessionsDir(), nil, 0o600); err != nil { // the load fails: sessions/ is a file
		t.Fatal(err)
	}

	step := func(wantKind, wantReason string) {
		t.Helper()
		got := Restart(Input{Args: []string{}}, f.env())
		checkEnvelope(t, got)
		wantError(t, got, wantKind, wantReason)
		if f.ran() {
			t.Errorf("%s: fzf ran", wantKind)
		}
	}
	f.noHome = true
	step(ops.KindEnvironment, "")
	f.noHome = false
	step(ops.KindCorrupt, "")
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	step(KindUnavailable, "fzf-missing")
	f.noFzf = false
	f.setFile("version", "0.62.9\n")
	step(KindUnavailable, "fzf-too-old")
	f.setFile("version", "0.74.4\n")
	step(KindUnavailable, "fzf-failed") // SESSHIN_PICK_OPTS does not split
	f.environ = nil
	step(ops.KindTerminal, "unavailable")
	f.vars["KITTY_LISTEN_ON"] = "unix:/kitty"
	step(ops.KindIO, "") // the load
	if err := os.Remove(f.loc.SessionsDir()); err != nil {
		t.Fatal(err)
	}
	f.add(1, time.Minute)
	step(KindUnavailable, "no-terminal")
	f.noTTY = false
	f.selects(130)
	wantError(t, f.restart(Input{}), KindCancelled, "")
}

// fzf --version's failures.
func TestFzfVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		status  string
		reason  string
		details map[string]any
	}{
		{"ok", "0.63.0 (abc)\n", "", "", nil},
		{"newer, dev suffix", "0.99.1-dev (abc)\n", "", "", nil},
		{"too old", "0.62.0\n", "", "fzf-too-old", map[string]any{"found": "0.62.0", "required": "0.63.0"}},
		{"not a version", "hello\n", "", "fzf-too-old", map[string]any{"found": "hello", "required": "0.63.0"}},
		{"empty", "", "", "fzf-too-old", map[string]any{"found": "", "required": "0.63.0"}},
		{"fails", "", "3", "fzf-failed", map[string]any{"status": 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.add(1, time.Minute)
			f.selects(1)
			f.setFile("version", tc.version)
			if tc.status != "" {
				f.setFile("version-status", tc.status)
				f.setFile("version-err", "unknown option: --bogus\n")
			}
			env := f.restart(Input{})
			if tc.reason == "" {
				actions(t, env)
				return
			}
			wantError(t, env, KindUnavailable, tc.reason)
			for k, v := range tc.details {
				if env.Error.Details[k] != v {
					t.Errorf("details %+v, want %s=%v", env.Error.Details, k, v)
				}
			}
			if f.ran() {
				t.Error("fzf ran")
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]struct {
		found string
		v     version
		ok    bool
	}{
		"0.63.0 (devel)": {"0.63.0", version{0, 63, 0}, true},
		"0.75.0-dev":     {"0.75.0-dev", version{0, 75, 0}, true},
		"1.2":            {"1.2", version{}, false},
		"1.2.x":          {"1.2.x", version{}, false},
		"1.+2.3":         {"1.+2.3", version{}, false},
		"":               {"", version{}, false},
	} {
		found, v, ok := parseVersion([]byte(in))
		if found != want.found || ok != want.ok || ok && v != want.v {
			t.Errorf("%q: %q %v %v", in, found, v, ok)
		}
	}
}

func TestInputChecks(t *testing.T) {
	for _, tc := range []struct {
		in     string
		field  string // "": accepted
		schema bool   // the published schema rejects it too
	}{
		{`{}`, "", false},
		{`{"query":"killed","args":["--model","opus"]}`, "", false},
		{`{"query":""}`, "", false},
		{`{"args":["--","$(a)",""]}`, "", false},
		{`{"query":1}`, "/query", true},
		{`{"query":null}`, "/query", true},
		{`{"args":"x"}`, "/args", true},
		{`{"args":["a",1]}`, "/args/1", true},
		{`{"args":["a","b\u0000"]}`, "/args/1", false},
		{`{"session":"12"}`, "/session", true},
		{`{"job":"api"}`, "/job", true},
	} {
		in, e := ops.DecodeInput([]byte(tc.in), DecodeInput)
		switch {
		case tc.field == "" && e != nil:
			t.Errorf("%s: %+v", tc.in, e)
		case tc.field != "":
			if e == nil {
				t.Errorf("%s: accepted", tc.in)
				continue
			}
			if ps := e.Details["problems"].([]model.Problem); e.Kind != ops.KindInvalidInput || ps[0].Field != tc.field {
				t.Errorf("%s: %+v", tc.in, e)
			}
		}
		if tc.field == "" && in.Args == nil {
			t.Errorf("%s: nil args", tc.in)
		}
		if ok, _ := schematest.Check(t, "restart-input", []byte(tc.in)); ok == (tc.field != "" && tc.schema) {
			t.Errorf("%s: the schema says %v", tc.in, ok)
		}
	}
}
