package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/placement/kitty"
	"github.com/phansen314/sesshin/internal/proc"
	"github.com/phansen314/sesshin/internal/schematest"
)

// A stored kitty placement with the keys only the sync writes.
const syncedPlacement = `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":"api review","user_vars":{"project":"api","b":"2"}}`

// endedSession writes an ended session that can be resumed: cwd is the
// fixture's, and its transcript is there. placement is the JSON of its
// placement, "" for null; job "" for none.
func (f *spawnFixture) endedSession(id string, sesshinID int64, job, placement string, mod ...func(*model.LifecycleFile)) {
	f.t.Helper()
	transcript := filepath.Join(f.cwd, id+".jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	cwd := f.cwd
	f.session(id, time.Hour, append([]func(*model.LifecycleFile){func(l *model.LifecycleFile) {
		l.Cwd, l.TranscriptPath = &cwd, &transcript
	}}, mod...)...)
	f.sesshinFile(id, sesshinID, job, placement)
}

// sesshinFile writes a usable sesshin.json with the job and the placement's JSON.
func (f *spawnFixture) sesshinFile(id string, sesshinID int64, job, placement string) {
	f.t.Helper()
	jobJSON := "null"
	if job != "" {
		jobJSON = fmt.Sprintf("%q", job)
	}
	if placement == "" {
		placement = "null"
	}
	b := fmt.Sprintf(`{"schema": 2,"id":%d,"job":%s,"source":"spawn","placement":%s,"extra":{}}`, sesshinID, jobJSON, placement)
	if _, r := model.ReadSesshin([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture sesshin.json unusable: %s", r.Reason())
	}
	f.write(id, "sesshin.json", []byte(b))
}

func withTranscript(path *string) func(*model.LifecycleFile) {
	return func(l *model.LifecycleFile) { l.TranscriptPath = path }
}

// resumeRaw runs resume on the JSON input as given.
func (f *spawnFixture) resumeRaw(in string) Envelope {
	f.t.Helper()
	ri, e := DecodeInput([]byte(in), DecodeResumeInput)
	if e != nil {
		env := Failed(e)
		checkEnvelope(f.t, env, "resume-output")
		return env
	}
	env := Resume(ri, f.spawnEnv())
	checkEnvelope(f.t, env, "resume-output")
	return env
}

func (f *spawnFixture) resume(session string, members ...string) Envelope {
	f.t.Helper()
	s, _ := json.Marshal(session)
	return f.resumeRaw("{" + strings.Join(append([]string{`"session":` + string(s)}, members...), ",") + "}")
}

// resumed is a resume that must have succeeded.
func (f *spawnFixture) resumed(session string, members ...string) (ResumeOutput, []Warning) {
	f.t.Helper()
	env := f.resume(session, members...)
	if !env.OK {
		f.t.Fatalf("resume failed: %+v", env.Error)
	}
	return env.Result.(ResumeOutput), env.Warnings
}

// Every input check, at its field; and what the schema also says.
func TestResumeInputChecks(t *testing.T) {
	long := strings.Repeat("a", 64)
	for _, tc := range []struct {
		in     string
		field  string // "": accepted
		schema bool   // the published schema rejects it too
	}{
		{`{"session":"12"}`, "", false},
		{`{"session":"api","job":"web","args":["--model","opus"],"start_timeout_secs":120}`, "", false},
		{`{"session":"job:deadbeef","args":[],"start_timeout_secs":0}`, "", false},
		{`{"session":"` + uuidA + `","job":"` + long + `"}`, "", false},
		{`{"session":"12","args":["--","$(a)",""]}`, "", false},
		{`{}`, "/session", true},
		{`{"session":12}`, "/session", true},
		{`{"session":""}`, "/session", true},
		{`{"session":"012"}`, "/session", true},
		{`{"session":"job:12"}`, "/session", true},
		{`{"session":"a b"}`, "/session", true},
		{`{"session":"12","job":"12"}`, "/job", true},
		{`{"session":"12","job":null}`, "/job", true},
		{`{"session":"12","job":"` + long + `a"}`, "/job", true},
		{`{"session":"12","args":"x"}`, "/args", true},
		{`{"session":"12","args":["a",1]}`, "/args/1", true},
		{`{"session":"12","args":["a","b\u0000"]}`, "/args/1", false},
		{`{"session":"12","start_timeout_secs":-1}`, "/start_timeout_secs", true},
		{`{"session":"12","start_timeout_secs":121}`, "/start_timeout_secs", true},
		{`{"session":"12","start_timeout_secs":"5"}`, "/start_timeout_secs", true},
		{`{"session":"12","cwd":"/w"}`, "/cwd", true},
		{`{"session":"12","prompt":"x"}`, "/prompt", true},
	} {
		_, e := DecodeInput([]byte(tc.in), DecodeResumeInput)
		switch {
		case tc.field == "" && e != nil:
			t.Errorf("%s: %+v", tc.in, e)
		case tc.field != "":
			if e == nil {
				t.Errorf("%s: accepted", tc.in)
				continue
			}
			checkEnvelope(t, Failed(e), "")
			if ps := e.Details["problems"].([]model.Problem); e.Kind != KindInvalidInput || ps[0].Field != tc.field {
				t.Errorf("%s: %+v", tc.in, e)
			}
		}
		if ok, _ := schematest.Check(t, "resume-input", []byte(tc.in)); ok == (tc.field != "" && tc.schema) {
			t.Errorf("%s: the schema says %v", tc.in, ok)
		}
	}
	f := newSpawnFixture(t)
	env := f.resumeRaw(`{"job":"X_","args":[1],"start_timeout_secs":-1}`)
	wantKind(t, env, KindInvalidInput)
	var fields []string
	for _, p := range env.Error.Details["problems"].([]model.Problem) {
		fields = append(fields, p.Field)
	}
	if want := []string{"/args/0", "/job", "/session", "/start_timeout_secs"}; !slices.Equal(fields, want) {
		t.Errorf("problems at %v, want %v", fields, want)
	}
}

func TestResumeInputDefaults(t *testing.T) {
	in, e := DecodeInput([]byte(`{"session":"api"}`), DecodeResumeInput)
	if e != nil {
		t.Fatal(e)
	}
	want := ResumeInput{Selector: Selector{Raw: "api", Job: "api"}, Args: []string{}, StartTimeoutSecs: 15}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("%+v, want %+v", in, want)
	}
	in, _ = DecodeInput([]byte(`{"session":"job:deadbeef","job":"j","args":["x"],"start_timeout_secs":0}`), DecodeResumeInput)
	want = ResumeInput{Selector: Selector{Raw: "job:deadbeef", Job: "deadbeef"}, Job: "j", Args: []string{"x"}}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("%+v, want %+v", in, want)
	}
}

// The Errors table's order: invalid-input, environment, corrupt, not-found,
// ambiguous, conflict live, the cwd, terminal unavailable, busy, conflict
// job-taken, and then the launch's.
func TestResumeErrorOrder(t *testing.T) {
	f := newSpawnFixture(t)
	gone := filepath.Join(f.cwd, "gone")
	f.endedSession(uuidA, 1, "api", "")
	f.running(uuidB, time.Minute, 11) // shares uuidA's prefix, and holds the job
	f.sesshinFile(uuidB, 2, "web", "")
	f.running(uuidC, time.Minute, 12, func(l *model.LifecycleFile) { l.Cwd = &gone })
	f.sesshinFile(uuidC, 3, "api", "")
	f.writeLifecycle(f.lifecycle(uuidA, f.ago(time.Hour), func(l *model.LifecycleFile) { l.Cwd = &gone })) // the cwd is gone
	f.sesshinFile(uuidA, 1, "api", "")
	f.config("retain_days = -1")
	f.vars["KITTY_LISTEN_ON"] = ""
	root, err := fsys.OS{}.OpenRoot(f.loc.SessionsDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	home := f.home

	f.home = ""
	wantKind(t, f.resumeRaw(`{"session":"Bad Selector"}`), KindInvalidInput)
	wantKind(t, f.resume("1"), KindEnvironment)
	f.home = home
	wantKind(t, f.resume("1"), KindCorrupt)
	f.config("")
	env := f.resume("99")
	wantKind(t, env, KindNotFound)
	if !reflect.DeepEqual(env.Error.Details, map[string]any{"selectors": []string{"99"}, "paths": []string{}}) {
		t.Errorf("details %+v", env.Error.Details)
	}
	wantKind(t, f.resume("0b6c5a3e"), KindAmbiguous)
	env = f.resume("2")
	wantKind(t, env, KindConflict)
	if env.Error.Details["rule"] != "live" {
		t.Errorf("details %+v", env.Error.Details)
	}
	if refs := env.Error.Details["sessions"].([]SessionRef); len(refs) != 1 || refs[0].SessionID != uuidB {
		t.Errorf("sessions %+v", refs)
	}
	env = f.resume("1")
	wantKind(t, env, KindNotFound)
	if !reflect.DeepEqual(env.Error.Details, map[string]any{"selectors": []string{}, "paths": []string{gone}}) {
		t.Errorf("details %+v", env.Error.Details)
	}
	f.writeLifecycle(f.lifecycle(uuidA, f.ago(time.Hour), func(l *model.LifecycleFile) { l.Cwd = &f.cwd }))
	env = f.resume("1")
	wantKind(t, env, KindTerminal)
	if env.Error.Details["reason"] != "unavailable" || env.Error.Details["terminal"] != nil {
		t.Errorf("details %+v", env.Error.Details)
	}
	f.vars["KITTY_LISTEN_ON"] = "unix:/kitty"
	wantKind(t, f.resume("1"), KindBusy) // 500 ms, real
	lock.Unlock()
	f.running(uuidD, time.Minute, 13)
	f.sesshinFile(uuidD, 4, "api", "")
	env = f.resume("1")
	wantKind(t, env, KindConflict)
	if env.Error.Details["rule"] != "job-taken" || !strings.Contains(env.Error.Message, "job") {
		t.Errorf("details %+v", env.Error)
	}
	if len(f.launches) != 0 {
		t.Error("launched before an error")
	}
	f.launchErr = &kitty.LaunchError{Err: errors.New("no")}
	wantKind(t, f.resume("1", `"job":"other"`), KindTerminal)
}

// A cwd that was never recorded is not-found with no paths; one that is not
// a directory is one with the path; anything else the OS says is io.
func TestResumeCwd(t *testing.T) {
	f := newSpawnFixture(t)
	file := filepath.Join(f.cwd, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rel := "relative/dir"
	for _, tc := range []struct {
		cwd  *string
		path []string
	}{
		{nil, []string{}},
		{new(string), []string{}},
		{&file, []string{file}},
		{&rel, []string{rel}},
	} {
		f.endedSession(uuidA, 1, "", "", func(l *model.LifecycleFile) { l.Cwd = tc.cwd })
		env := f.resume("1")
		wantKind(t, env, KindNotFound)
		if !reflect.DeepEqual(env.Error.Details, map[string]any{"selectors": []string{}, "paths": tc.path}) {
			t.Errorf("%v: %+v", tc.cwd, env.Error.Details)
		}
	}
	f.endedSession(uuidA, 1, "", "")
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpStat && op.Path == f.cwd {
			return syscall.EACCES
		}
		return nil
	}
	env := f.resume("1")
	wantKind(t, env, KindIO)
	if len(f.launches) != 0 {
		t.Error("launched")
	}
}

func TestResumeTerminalUnavailable(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"not in kitty":       {"KITTY_LISTEN_ON": "", "KITTY_WINDOW_ID": ""},
		"remote control off": {"KITTY_LISTEN_ON": "", "KITTY_WINDOW_ID": "3"},
		"tmux":               {"TMUX": "/tmp/tmux-1000/default,1,0"},
		"screen":             {"STY": "123.pts-0.host"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSpawnFixture(t)
			f.endedSession(uuidA, 1, "api", "")
			for k, v := range vars {
				f.vars[k] = v
			}
			env := f.resume("1")
			wantKind(t, env, KindTerminal)
			if env.Error.Details["reason"] != "unavailable" || len(f.launches) != 0 || f.reserved("api") {
				t.Errorf("%+v", env.Error.Details)
			}
		})
	}
}

// The launch: the request the backend gets, the reservation, and the
// result.
func TestResumeLaunch(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "api", syncedPlacement)
	out, warnings := f.resumed("1", `"args":["--model","opus"]`, `"start_timeout_secs":0`)
	if len(warnings) != 0 || out.Session != nil || out.Job == nil || *out.Job != "api" || enc(t, out.Placement) != launched {
		t.Errorf("%+v, warnings %+v", out, warnings)
	}
	want := kitty.LaunchSpec{
		Socket: "unix:/kitty",
		Type:   "tab",
		Cwd:    f.cwd,
		Title:  "api review",
		Vars:   []kitty.Var{{Name: "project", Value: "api"}, {Name: "b", Value: "2"}},
		Env:    []kitty.Var{{Name: "SESSHIN_JOB", Value: "api"}, {Name: "SESSHIN_TOKEN", Value: token(1)}},
		Argv:   []string{"/bin/zsh", "-l", "-i", "-c", `exec "$@"`, "sesshin", "claude", "--resume", uuidA, "--model", "opus"},
	}
	if len(f.launches) != 1 || !reflect.DeepEqual(f.launches[0], want) {
		t.Errorf("launched %+v\nwant     %+v", f.launches, want)
	}
	r, ok := f.reservation("api")
	if !ok || r.Token != token(1) || enc(t, r.Placement) != launched || enc(t, r.Extra) != "{}" || r.Job == nil || *r.Job != "api" {
		t.Errorf("reservation %+v", r)
	}
	if _, err := os.Stat(filepath.Join(f.loc.ReservationsDir(), "api_"+token(1)+".json")); err != nil {
		t.Errorf("reservation file: %v", err)
	}
	if len(f.sleeps) != 0 {
		t.Errorf("waited with start_timeout_secs 0: %d pauses", len(f.sleeps))
	}
	// An args list that ends in an option that takes a value has none: there
	// is no prompt after -- for it to take.
	f.removeReservation("api")
	f.resumed("1", `"args":["--model"]`, `"start_timeout_secs":0`)
	if got := f.launches[1].Argv[7:]; !slices.Equal(got, []string{"--resume", uuidA, "--model"}) {
		t.Errorf("argv %q", f.launches[1].Argv)
	}
}

// The title is the stored placement's, else the session's name; the user
// variables are those of kitty's placement.
func TestResumeTitleAndVars(t *testing.T) {
	for _, tc := range []struct {
		name      string
		placement string
		mod       []func(*model.LifecycleFile)
		title     string
		vars      []kitty.Var
	}{
		{"synced", syncedPlacement, nil, "api review", []kitty.Var{{Name: "project", Value: "api"}, {Name: "b", Value: "2"}}},
		{"title only", `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":"t"}`, nil, "t", nil},
		{"vars only", `{"terminal":"kitty","socket":"unix:/old","window_id":4,"user_vars":{"p":"1"}}`, nil, "#1", []kitty.Var{{Name: "p", Value: "1"}}},
		{"empty title", `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":""}`, nil, "#1", nil},
		{"bare kitty", `{"terminal":"kitty","socket":"unix:/old","window_id":4}`, nil, "#1", nil},
		{"no placement", "", nil, "#1", nil},
		{"invalid kitty: title not a string", `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":1,"user_vars":{"p":"1"}}`, nil, "#1", nil},
		{"invalid kitty: vars not strings", `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":"t","user_vars":{"p":1}}`, nil, "#1", nil},
		{"invalid kitty: no socket", `{"terminal":"kitty","window_id":4,"tab_title":"t"}`, nil, "#1", nil},
		{"another terminal's", `{"terminal":"tmux","pane":"%1","tab_title":"t","user_vars":{"p":"1"}}`, nil, "#1", nil},
		{"named by /rename", "", []func(*model.LifecycleFile){func(l *model.LifecycleFile) { s := "my work"; l.SessionTitle = &s }}, "my work", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpawnFixture(t)
			f.endedSession(uuidA, 1, "", tc.placement, tc.mod...)
			f.resumed("1", `"start_timeout_secs":0`)
			if got := f.launches[0]; got.Title != tc.title || !slices.Equal(got.Vars, tc.vars) || got.Type != "tab" {
				t.Errorf("type %q title %q vars %+v; want %q, %+v", got.Type, got.Title, got.Vars, tc.title, tc.vars)
			}
		})
	}
}

// With no job, no lock, no reservation, no state written beyond reading, and
// the environment names no job.
func TestResumeWithoutJob(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "", "")
	locks := 0
	f.hook = func(op fsys.Op) error {
		if op.Mutating || op.Name == fsys.OpLock {
			locks++
		}
		return nil
	}
	out, warnings := f.resumed("1", `"start_timeout_secs":0`)
	if len(warnings) != 0 || out.Job != nil || out.Session != nil || locks != 0 {
		t.Errorf("%+v, warnings %+v, %d locks", out, warnings, locks)
	}
	if spec := f.launches[0]; len(spec.Env) != 0 {
		t.Errorf("spec %+v", spec)
	}
	if _, err := os.Stat(f.loc.ReservationsDir()); !os.IsNotExist(err) {
		t.Errorf("reservations/: %v", err)
	}
}

// The job to resume under: job, else the session's, as readers report it.
func TestResumeJob(t *testing.T) {
	t.Run("stored", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.endedSession(uuidA, 1, "api", "")
		out, _ := f.resumed("1", `"start_timeout_secs":0`)
		if *out.Job != "api" || !f.reserved("api") {
			t.Errorf("%+v", out)
		}
	})
	t.Run("job names another", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.endedSession(uuidA, 1, "api", "")
		out, _ := f.resumed("1", `"job":"api-old"`, `"start_timeout_secs":0`)
		if *out.Job != "api-old" || !f.reserved("api-old") || f.reserved("api") {
			t.Errorf("%+v", out)
		}
		spec := f.launches[0]
		if want := []kitty.Var{{Name: "SESSHIN_JOB", Value: "api-old"}, {Name: "SESSHIN_TOKEN", Value: token(1)}}; !slices.Equal(spec.Env, want) {
			t.Errorf("env %+v", spec.Env)
		}
	})
	t.Run("job for a session with none", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.endedSession(uuidA, 1, "", "")
		out, _ := f.resumed("1", `"job":"api"`, `"start_timeout_secs":0`)
		if *out.Job != "api" || !f.reserved("api") {
			t.Errorf("%+v", out)
		}
	})
	t.Run("a job a live session holds is taken, and job names another", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.endedSession(uuidA, 1, "api", "")
		f.running(uuidC, time.Minute, 12)
		f.sesshinFile(uuidC, 3, "api", "")
		env := f.resume("1")
		wantKind(t, env, KindConflict)
		if refs := env.Error.Details["sessions"].([]SessionRef); env.Error.Details["rule"] != "job-taken" || len(refs) != 1 || refs[0].SessionID != uuidC {
			t.Errorf("%+v", env.Error.Details)
		}
		if len(f.launches) != 0 || f.reserved("api") {
			t.Error("launched or reserved")
		}
		out, _ := f.resumed("1", `"job":"api-old"`, `"start_timeout_secs":0`)
		if *out.Job != "api-old" {
			t.Errorf("%+v", out)
		}
	})
	t.Run("a fresh reservation holds it, with no session named", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.endedSession(uuidA, 1, "api", "")
		f.reserveToken("api", tokenB, 30*time.Second, "")
		env := f.resume("1")
		wantKind(t, env, KindConflict)
		if refs := env.Error.Details["sessions"].([]SessionRef); env.Error.Details["rule"] != "job-taken" || len(refs) != 0 {
			t.Errorf("%+v", env.Error.Details)
		}
		// Stranded, it is replaced.
		f.now = f.now.Add(121 * time.Second)
		f.resumed("1", `"start_timeout_secs":0`)
		if r, _ := f.reservation("api"); r.Token != token(1) {
			t.Errorf("token %s", r.Token)
		}
	})
	t.Run("an ended holder holds nothing", func(t *testing.T) {
		f := newSpawnFixture(t)
		f.endedSession(uuidA, 1, "api", "")
		f.endedSession(uuidC, 3, "api", "")
		f.resumed(uuidA, `"start_timeout_secs":0`)
	})
}

// A headless session resumes like any other.
func TestResumeHeadless(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "", "", nested)
	f.resumed("1", `"start_timeout_secs":0`)
	if len(f.launches) != 1 {
		t.Error("not launched")
	}
}

// The job form of the selector, among ended sessions, and show's among all.
func TestSelectorJob(t *testing.T) {
	f := newSpawnFixture(t)
	older, newer := uuidA, uuidC
	f.endedSession(older, 1, "api", "")
	f.session(newer, 10*time.Minute, func(l *model.LifecycleFile) { l.Cwd = &f.cwd })
	f.sesshinFile(newer, 3, "api", "")
	f.endedSession(uuidD, 4, "deadbeef", "")
	f.endedSession(uuidE, 5, "web", "")

	t.Run("resume takes the one last seen", func(t *testing.T) {
		for _, sel := range []string{"api", "job:api"} {
			f.launches = nil
			f.removeReservationIfAny("api")
			f.resumed(sel, `"start_timeout_secs":0`)
			if got := f.launches[0].Argv; got[len(got)-1] != newer || got[len(got)-2] != "--resume" {
				t.Errorf("%s: argv %q", sel, got)
			}
		}
	})
	t.Run("a job that looks like a UUID prefix is job:", func(t *testing.T) {
		f.launches = nil
		wantKind(t, f.resume("deadbeef"), KindNotFound)
		f.resumed("job:deadbeef", `"start_timeout_secs":0`)
		if got := f.launches[0].Argv; got[len(got)-1] != uuidD {
			t.Errorf("argv %q", got)
		}
	})
	t.Run("a job nothing reports is not-found", func(t *testing.T) {
		env := f.resume("nope")
		wantKind(t, env, KindNotFound)
		if !reflect.DeepEqual(env.Error.Details["selectors"], []string{"nope"}) {
			t.Errorf("%+v", env.Error.Details)
		}
	})
	t.Run("a job only a live session holds is not-found among ended ones", func(t *testing.T) {
		g := newSpawnFixture(t)
		g.running(uuidB, time.Minute, 11)
		g.sesshinFile(uuidB, 2, "live-job", "")
		env := g.resume("live-job")
		wantKind(t, env, KindNotFound)
		if len(g.launches) != 0 {
			t.Error("launched")
		}
		// By sesshin ID it is the live conflict instead.
		env = g.resume("2")
		wantKind(t, env, KindConflict)
	})
	t.Run("never ambiguous", func(t *testing.T) {
		g := newSpawnFixture(t)
		for i := range 25 {
			id := fmt.Sprintf("abcdef00-0000-4000-8000-%012d", i)
			g.endedSession(id, int64(10+i), "many", "")
		}
		g.resumed("many", `"start_timeout_secs":0`)
	})
	t.Run("the invalid forms", func(t *testing.T) {
		for _, sel := range []string{"job:", "job:12", "a b", "api-", "#12"} {
			wantKind(t, f.resume(sel), KindInvalidInput)
		}
	})
}

func (f *spawnFixture) removeReservationIfAny(job string) {
	if f.reserved(job) {
		f.removeReservation(job)
	}
}

// show selects a job's live session, else the last one seen; an unusable
// sesshin.json is warned of as for a sesshin ID.
func TestShowSelectorJob(t *testing.T) {
	f := newPruneFixture(t)
	f.session(uuidA, 3*time.Hour)
	f.sesshinWith(uuidA, 1, "api", "spawn")
	f.session(uuidB, 10*time.Minute)
	f.sesshinWith(uuidB, 2, "api", "spawn")
	f.session(uuidC, 2*time.Hour)
	f.sesshinWith(uuidC, 3, "deadbeef", "spawn")
	for _, tc := range []struct {
		sel, want string
	}{
		{"api", uuidB}, {"job:api", uuidB}, {"job:deadbeef", uuidC},
	} {
		if got := f.shown(tc.sel)["session_id"]; got != tc.want {
			t.Errorf("%s: %v, want %s", tc.sel, got, tc.want)
		}
	}
	// A hex-looking bare job is a UUID prefix.
	wantKind(t, f.show("deadbeef", false), KindNotFound)
	wantKind(t, f.show("nope", false), KindNotFound)

	// The live one wins over a newer ended one.
	f.running(uuidD, 5*time.Hour, 14)
	f.sesshinWith(uuidD, 4, "api", "spawn")
	if got := f.shown("api")["session_id"]; got != uuidD {
		t.Errorf("live: %v", got)
	}
	// And a session of unknown liveness counts as live.
	f.tableErr[14] = errors.New("table unreadable")
	if v := f.shown("api"); v["session_id"] != uuidD || v["liveness"] != "unknown" {
		t.Errorf("unknown: %v", v)
	}
	// A session that doesn't report the job, because a live one holds it, is
	// not selected by it.
	f.running(uuidE, 20*time.Minute, 15, startedAgo(f, time.Hour))
	f.sesshinWith(uuidE, 5, "api", "spawn")
	if got := f.shown("api")["session_id"]; got != uuidD && got != uuidE {
		t.Errorf("held: %v", got)
	}
	// Warnings of an unusable sesshin.json come with the not-found, as with an ID.
	g := newPruneFixture(t)
	g.session(uuidA, time.Hour)
	g.write(uuidA, "sesshin.json", []byte("{"))
	env := g.show("api", false)
	wantKind(t, env, KindNotFound)
	if got := warnKinds(env); !slices.Equal(got, []string{"unusable-file"}) {
		t.Errorf("warnings %v", got)
	}
}

func TestSelectorParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Selector
	}{
		{"12", Selector{Raw: "12", ID: 12}},
		{"0b6c5a3e", Selector{Raw: "0b6c5a3e", Prefix: "0b6c5a3e"}},
		{"DEADBEEF", Selector{Raw: "DEADBEEF", Prefix: "deadbeef"}},
		{"cafe-1234", Selector{Raw: "cafe-1234", Prefix: "cafe-1234"}},
		{"api", Selector{Raw: "api", Job: "api"}},
		{"decade", Selector{Raw: "decade", Job: "decade"}}, // 6 characters: not a prefix
		{"job:api", Selector{Raw: "job:api", Job: "api"}},
		{"job:deadbeef", Selector{Raw: "job:deadbeef", Job: "deadbeef"}},
		{"Api", Selector{Raw: "Api", Job: "Api"}}, // case kept: API and api are two jobs
		{"job:DEADBEEF", Selector{Raw: "job:DEADBEEF", Job: "DEADBEEF"}},
		{"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", Selector{Raw: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", Job: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}}, // 40
	} {
		got, reason := parseSelector(tc.in)
		if reason != "" || got != tc.want {
			t.Errorf("%s: %+v %q, want %+v", tc.in, got, reason, tc.want)
		}
		if ok, _ := schematest.Check(t, "selector", []byte(`"`+tc.in+`"`)); !ok {
			t.Errorf("%s: the schema rejects it", tc.in)
		}
	}
	for _, in := range []string{"", "job:", "job:12", "-a", "a-", "a_b", "a b", "012", "job:job:a"} {
		if _, reason := parseSelector(in); reason == "" {
			t.Errorf("%q accepted", in)
		}
		if ok, _ := schematest.Check(t, "selector", []byte(`"`+in+`"`)); ok && in != "012" {
			t.Errorf("%q: the schema accepts it", in)
		}
	}
}

// transcript-missing: the transcript is not where it was recorded.
func TestResumeTranscriptMissing(t *testing.T) {
	f := newSpawnFixture(t)
	missing := filepath.Join(f.cwd, "gone.jsonl")
	f.endedSession(uuidA, 1, "", "", withTranscript(&missing))
	out, warnings := f.resumed("1", `"start_timeout_secs":0`)
	if out.Placement == nil || len(f.launches) != 1 {
		t.Errorf("not launched: %+v", out)
	}
	if len(warnings) != 1 || warnings[0].Kind != "transcript-missing" {
		t.Fatalf("warnings %+v", warnings)
	}
	d := warnings[0].Details
	if ref, ok := d["session"].(SessionRef); !ok || ref.SessionID != uuidA || ref.ID == nil || *ref.ID != 1 || d["path"] != missing {
		t.Errorf("details %+v", d)
	}
	// None recorded, or relative: nothing is known, so nothing is said.
	rel := "t.jsonl"
	for _, p := range []*string{nil, &rel} {
		g := newSpawnFixture(t)
		g.endedSession(uuidA, 1, "", "", withTranscript(p))
		if _, warnings := g.resumed("1", `"start_timeout_secs":0`); len(warnings) != 0 {
			t.Errorf("%v: %+v", p, warnings)
		}
	}
	// It goes with a launch the backend refused, too.
	f.launchErr = &kitty.LaunchError{Err: errors.New("x")}
	env := f.resume("1")
	if got := warnKinds(env); !slices.Equal(got, []string{"transcript-missing"}) {
		t.Errorf("warnings %v", got)
	}
}

// The wait ends when the session is live again, reading only its directory.
func TestResumeWait(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "api", syncedPlacement)
	f.endedSession(uuidB, 2, "web", "") // not read by the wait
	pid := int64(31)
	f.onSleep = func(n int) {
		if n == 3 {
			f.table[pid] = "s31"
			cwd, started := f.cwd, "s31"
			f.writeLifecycle(f.lifecycle(uuidA, f.now, notEnded, withPID(pid, started), func(l *model.LifecycleFile) { l.Cwd = &cwd }))
		}
	}
	f.hook = func(op fsys.Op) error {
		if len(f.launches) > 0 && op.Name == fsys.OpReadDir && filepath.Base(op.Root) == "sessions" {
			t.Errorf("the wait listed sessions/")
		}
		if len(f.launches) > 0 && strings.Contains(op.Path, uuidB) {
			t.Errorf("the wait read another session: %s", op.Path)
		}
		return nil
	}
	out, warnings := f.resumed("1", `"start_timeout_secs":5`)
	if len(warnings) != 0 || out.Session == nil {
		t.Fatalf("%+v, warnings %+v", out, warnings)
	}
	if s := out.Session; s.SessionID != uuidA || s.ID == nil || *s.ID != 1 || s.Liveness != "live" {
		t.Errorf("session %+v", s)
	}
	if want := []time.Duration{pollInterval, pollInterval, pollInterval}; !slices.Equal(f.sleeps, want) {
		t.Errorf("pauses %v", f.sleeps)
	}
	f.hook = nil
	if shown := f.shown("1"); !reflect.DeepEqual(asMap(t, out.Session), shown) {
		t.Errorf("view differs from show's:\n%v\n%v", asMap(t, out.Session), shown)
	}
}

// A session that does not come back: not-started, with the session named; a
// liveness of unknown is not live.
func TestResumeNotStarted(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "api", "")
	out, warnings := f.resumed("1", `"start_timeout_secs":2`)
	if out.Session != nil || enc(t, out.Placement) != launched || *out.Job != "api" {
		t.Errorf("%+v", out)
	}
	if len(warnings) != 1 || warnings[0].Kind != "not-started" {
		t.Fatalf("warnings %+v", warnings)
	}
	d := warnings[0].Details
	ref, ok := d["session"].(SessionRef)
	if !ok || ref.SessionID != uuidA || d["job"] == nil || *d["job"].(*string) != "api" || enc(t, d["placement"]) != launched || d["waited_secs"] != int64(2) {
		t.Errorf("details %+v", d)
	}
	if len(f.sleeps) != 20 {
		t.Errorf("%d pauses", len(f.sleeps))
	}
	// The reservation stays, placed, for the session that starts.
	if r, ok := f.reservation("api"); !ok || enc(t, r.Placement) != launched {
		t.Errorf("reservation %+v", r)
	}

	g := newSpawnFixture(t)
	g.endedSession(uuidA, 1, "", "")
	g.onSleep = func(n int) {
		if n == 1 {
			g.table[41] = "s41"
			g.tableErr[41] = errors.New("table unreadable")
			g.writeLifecycle(g.lifecycle(uuidA, g.now, notEnded, withPID(41, "s41"), func(l *model.LifecycleFile) { l.Cwd = &g.cwd }))
		}
	}
	// Liveness unknown counts as started, as spawn's wait counts it.
	out, warnings = g.resumed("1", `"start_timeout_secs":1`)
	if out.Session == nil || out.Session.Liveness != "unknown" || out.Job != nil || len(warnings) != 0 {
		t.Errorf("%+v, warnings %+v", out, warnings)
	}
}

// start_timeout_secs 0 returns at once: no read, no pause, no warning.
func TestResumeNoWait(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "api", "")
	reads := 0
	f.hook = func(op fsys.Op) error {
		if len(f.launches) > 0 && op.Name == fsys.OpReadFile && strings.HasSuffix(op.Path, "lifecycle.json") {
			reads++
		}
		return nil
	}
	out, warnings := f.resumed("1", `"start_timeout_secs":0`)
	if out.Session != nil || len(warnings) != 0 || len(f.sleeps) != 0 || reads != 0 {
		t.Errorf("%+v, %+v, %v, %d reads", out, warnings, f.sleeps, reads)
	}
}

// The launch's outcomes, as spawn's: a refusal frees the job, an unknown
// outcome keeps the reservation, and the record may fail.
func TestResumeLaunchOutcomes(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "api", "")
	f.launchErr = &kitty.LaunchError{Err: errors.New("exit status 1: no such socket")}
	env := f.resume("1")
	wantKind(t, env, KindTerminal)
	want := map[string]any{"reason": "launch-failed", "terminal": "kitty", "detail": "exit status 1: no such socket"}
	if !reflect.DeepEqual(env.Error.Details, want) {
		t.Errorf("details %+v", env.Error.Details)
	}
	if f.reserved("api") {
		t.Error("the reservation was kept")
	}
	// The session is as it was, so a retry works at once.
	f.launchErr = &kitty.LaunchError{Unknown: true, Err: errors.New("10s limit passed")}
	env = f.resume("1")
	wantKind(t, env, KindTerminal)
	if env.Error.Details["reason"] != "launch-unknown" {
		t.Errorf("details %+v", env.Error.Details)
	}
	if r, ok := f.reservation("api"); !ok || r.Placement != nil {
		t.Errorf("reservation %+v", r)
	}
	// A retry while it is fresh is refused.
	wantKind(t, f.resume("1"), KindConflict)

	// The record failing is a warning, and the resume stands.
	g := newSpawnFixture(t)
	g.endedSession(uuidA, 1, "api", "")
	locks := 0
	g.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpLock {
			locks++
		}
		return failAt(op, fsys.OpLock, locks, 2, syscall.EAGAIN)
	}
	out, warnings := g.resumed("1", `"start_timeout_secs":0`)
	if enc(t, out.Placement) != launched || len(warnings) != 1 || warnings[0].Kind != "placement-not-recorded" || warnings[0].Details["job"] != "api" {
		t.Errorf("%+v, warnings %+v", out, warnings)
	}
}

// The files read while selecting, checking the job, and waiting that cannot be
// used are warned of, each once.
func TestResumeUnusableFiles(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "api", "")
	f.running(uuidC, time.Minute, 12)
	f.write(uuidC, "sesshin.json", []byte("{"))
	f.onSleep = func(n int) {
		if n == 1 {
			f.table[31] = "s31"
			f.writeLifecycle(f.lifecycle(uuidA, f.now, notEnded, withPID(31, "s31"), func(l *model.LifecycleFile) { l.Cwd = &f.cwd }))
			f.write(uuidA, "statusline.json", []byte("{")) // unusable, and found by the wait only
		}
	}
	out, warnings := f.resumed("1", `"start_timeout_secs":5`)
	if out.Session == nil {
		t.Fatalf("not live: %+v", warnings)
	}
	var paths []string
	for _, w := range warnings {
		if w.Kind != "unusable-file" {
			t.Errorf("warning %+v", w)
		}
		paths = append(paths, filepath.Base(filepath.Dir(w.Details["path"].(string)))+"/"+filepath.Base(w.Details["path"].(string)))
	}
	if want := []string{uuidC + "/sesshin.json", uuidA + "/statusline.json"}; !slices.Equal(paths, want) {
		t.Errorf("warnings for %v, want %v", paths, want)
	}
	// They go with a selection that fails, too.
	env := f.resume("99")
	wantKind(t, env, KindNotFound)
	if got := warnKinds(env); !slices.Equal(got, []string{"unusable-file", "unusable-file"}) {
		t.Errorf("warnings %v", got)
	}
}

// A fault reading the sessions during the wait is io.
func TestResumeWaitIOFault(t *testing.T) {
	f := newSpawnFixture(t)
	f.endedSession(uuidA, 1, "", "")
	f.hook = func(op fsys.Op) error {
		if len(f.launches) > 0 && op.Name == fsys.OpReadFile && strings.HasSuffix(op.Path, "lifecycle.json") {
			return syscall.EIO
		}
		return nil
	}
	wantKind(t, f.resume("1", `"start_timeout_secs":1`), KindIO)
}

func TestResumeEnv(t *testing.T) {
	// resume runs on spawn's environment: OSSpawnEnv has everything it uses.
	env := OSSpawnEnv()
	if env.Launch == nil || env.Token == nil || env.Sleep == nil || env.Now == nil {
		t.Errorf("%+v", env)
	}
}

// resume self behaves as resume of the caller's own live session's ID: a live
// session is refused (conflict live), and no job is claimed.
func TestResumeSelf(t *testing.T) {
	f := newSpawnFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.sesshinFile(uuidA, 1, "api", "")
	f.lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{PID: 11, StartedAt: "s11"} }
	env := f.resume("self")
	wantKind(t, env, KindConflict)
	if refs := env.Error.Details["sessions"].([]SessionRef); env.Error.Details["rule"] != "live" || len(refs) != 1 || refs[0].SessionID != uuidA {
		t.Errorf("details %+v", env.Error.Details)
	}
	// Outside any session it is not-found.
	f.lookup = nil
	wantKind(t, f.resume("self"), KindNotFound)
	if len(f.launches) != 0 {
		t.Error("launched")
	}
}

// A resume under a job refuses a sesshin.json in another format, before any
// reservation; with no job it resumes as usual.
func TestResumeOtherFormat(t *testing.T) {
	for _, c := range []struct{ name, file, want string }{
		{"older", `{"schema":1,"id":1,"job":null,"source":"hook","placement":null}`, "sesshin migrate"},
		{"newer", `{"schema":3,"id":1}`, "upgrade sesshin"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newSpawnFixture(t)
			f.endedSession(uuidA, 1, "", "")
			f.write(uuidA, "sesshin.json", []byte(c.file))
			path := filepath.Join(f.loc.SessionDir(uuidA), "sesshin.json")
			env := f.resume(uuidA, `"job":"api"`)
			wantKind(t, env, KindConflict)
			d := env.Error.Details
			refs, _ := d["sessions"].([]SessionRef)
			if d["rule"] != "other-format" || d["path"] != path || len(refs) != 1 || refs[0].SessionID != uuidA {
				t.Errorf("details %+v", d)
			}
			if !strings.Contains(env.Error.Message, c.want) {
				t.Errorf("message %q", env.Error.Message)
			}
			if len(f.launches) != 0 || f.reserved("api") {
				t.Error("launched or reserved")
			}
			f.vars["KITTY_LISTEN_ON"] = ""
			wantKind(t, f.resume(uuidA, `"job":"api"`), KindTerminal) // checked before the file
			f.vars["KITTY_LISTEN_ON"] = "unix:/kitty"
			out, _ := f.resumed(uuidA, `"start_timeout_secs":0`)
			if out.Job != nil || len(f.launches) != 1 {
				t.Errorf("%+v", out)
			}
		})
	}
}
