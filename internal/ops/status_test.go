package ops

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func (f *pruneFixture) writeStateFile(content string) {
	f.t.Helper()
	if err := os.MkdirAll(f.loc.StateDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.statePath(), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// Every unusable-file warning has a reason.
func TestUnusableFileReasons(t *testing.T) {
	f := newPruneFixture(t)
	f.running(uuidA, time.Minute, 11)
	f.write(uuidA, "sesshin.json", []byte(`{"schema":2,"id":0}`)) // corrupt
	f.write(uuidA, "statusline.json", []byte(`{"schema":2}`))     // another format
	f.running(uuidB, time.Minute, 12)
	if err := os.MkdirAll(filepath.Join(f.loc.SessionDir(uuidB), "statusline.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	env, _ := f.list(`{"liveness":"all"}`)
	got := map[string]any{}
	for _, w := range env.Warnings {
		got[filepath.Base(filepath.Dir(w.Details["path"].(string)))+"/"+filepath.Base(w.Details["path"].(string))] = w.Details["reason"]
	}
	want := map[string]any{
		uuidA + "/sesshin.json":    ReasonCorrupt,
		uuidA + "/statusline.json": ReasonUnsupportedFormat,
		uuidB + "/statusline.json": ReasonUnreadable,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reasons %v, want %v", got, want)
	}
}

type statusCase struct {
	name   string
	state  *string // nil: no state.json
	kind   string  // "" for none
	detail map[string]any
}

var statusCases = []statusCase{
	{"current", ptrTo(`{"schema":2,"last_id":1,"migration":1}`), "", nil},
	{"missing", nil, "", nil},
	{"corrupt", ptrTo(`nope`), "", nil},
	{"behind", ptrTo(`{"schema":2,"last_id":1,"migration":0}`), KindMigrationPending, map[string]any{"recorded": int64(0), "latest": int64(1)}},
	{"schema 1", ptrTo(`{"schema":1,"last_id":1}`), KindMigrationPending, map[string]any{"recorded": int64(0), "latest": int64(1)}},
	{"ahead", ptrTo(`{"schema":2,"last_id":1,"migration":2}`), KindMigrationAhead, map[string]any{"recorded": int64(2), "latest": int64(1)}},
	{"newer format", ptrTo(`{"schema":3,"last_id":1,"migration":9}`), KindMigrationAhead, map[string]any{"recorded": nil, "latest": int64(1)}},
}

// checkStatus requires the migration warning of c to be the envelope's first,
// and the only one of its kind, and none when there is nothing to compare.
func checkStatus(t *testing.T, op string, c statusCase, env Envelope) {
	t.Helper()
	var first *Warning
	if len(env.Warnings) > 0 {
		first = &env.Warnings[0]
	}
	if c.kind == "" {
		for _, w := range env.Warnings {
			if w.Kind == KindMigrationPending || w.Kind == KindMigrationAhead {
				t.Errorf("%s/%s: unexpected %+v", op, c.name, w)
			}
		}
		return
	}
	if first == nil || first.Kind != c.kind || !reflect.DeepEqual(first.Details, c.detail) {
		t.Errorf("%s/%s: first warning %+v, want %s %v", op, c.name, first, c.kind, c.detail)
	}
	for _, w := range env.Warnings[1:] {
		if w.Kind == c.kind {
			t.Errorf("%s/%s: twice", op, c.name)
		}
	}
}

// Every operation that reads the state directory but uninstall warns first of
// a migration pending or ahead; version, uninstall, and migrate do not.
func TestMigrationStatus(t *testing.T) {
	for _, c := range statusCases {
		f := newSendFixture(t)
		if c.state != nil {
			f.writeStateFile(*c.state)
		}
		f.live(uuidA, 1, "", "")
		sessions := map[string]Envelope{}
		listed, _ := f.list(`{}`)
		sessions["list"] = listed
		sessions["show"] = f.show("zzzzzzzz", false) // not found: the warning goes with a failure too
		sessions["prune"] = f.run(PruneInput{DryRun: true})
		sessions["send"] = f.send("zzzzzzzz", `"text":"x"`)
		sessions["resume"] = f.resume("zzzzzzzz")
		sessions["spawn"] = f.spawn()
		sessions["focus"] = Focus(FocusInput{Selector: Selector{Raw: "zzzzzzzz"}}, FocusEnv{ReadEnv: f.sendEnv().ReadEnv})
		sessions["update"] = f.update("zzzzzzzz", `{"merge":{"a":1}}`)
		for op, env := range sessions {
			checkStatus(t, op, c, env)
		}

		// install, over its own fixture and the same state directory content.
		g := newFixture(t)
		if c.state != nil {
			g.write(filepath.Join(g.state(), "state.json"), *c.state)
		}
		env := Install(InstallInput{DryRun: true}, g.s)
		checkEnvelope(t, env, "install-output")
		checkStatus(t, "install", c, env)

		// migrate reports its own state, never a status.
		m := newPruneFixture(t)
		if c.state != nil {
			m.writeStateFile(*c.state)
		}
		env = m.migrate(true)
		for _, w := range env.Warnings {
			if w.Kind == KindMigrationPending || w.Kind == KindMigrationAhead {
				t.Errorf("migrate/%s: %+v", c.name, w)
			}
		}
	}
	// uninstall reads only install.json.
	g := newFixture(t)
	g.write(filepath.Join(g.state(), "state.json"), `{"schema":2,"last_id":1,"migration":0}`)
	env := Uninstall(UninstallInput{DryRun: true}, g.s)
	checkEnvelope(t, env, "uninstall-output")
	if len(env.Warnings) != 0 {
		t.Errorf("uninstall: %+v", env.Warnings)
	}
}
