package record

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// Design-spec, Format versions: a file in another format is left alone, never
// replaced, by every hook; only session-start logs it.

const (
	pendingSesshin = `{"schema": 2, "id": null, "job": null, "source": "hook", "placement": null, "extra": {}}`
	sesshinAt      = `{"schema": %d, "id": 7, "job": null, "source": "hook", "placement": {"terminal": "kitty", "socket": "unix:/x", "window_id": 4}, "extra": {}}`
)

func bytesOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(data)
}

// wantFormatLog checks the log for a hook of kind: the message for
// session-start alone, nothing for any other.
func wantFormatLog(t *testing.T, f *fix, kind Kind, msg string) {
	t.Helper()
	got := f.logged()
	if kind == SessionStart {
		if !strings.Contains(got, msg) {
			t.Errorf("log %q, want %q", got, msg)
		}
	} else if got != "" {
		t.Errorf("%v logged %q", kind, got)
	}
}

func TestLifecycleOtherFormat(t *testing.T) {
	for _, kind := range kinds {
		for _, schema := range []int{0, 2, 99} {
			t.Run(strconv.Itoa(int(kind))+"/"+strconv.Itoa(schema), func(t *testing.T) {
				f := newFix(t)
				f.rec(Event{Kind: PostToolUse})
				content := `{"schema": ` + strconv.Itoa(schema) + `, "kept": true}`
				f.write(f.sessionPath(sid, "lifecycle.json"), content)
				sesshinBefore := bytesOf(t, f.sessionPath(sid, "sesshin.json"))
				stateBefore := bytesOf(t, f.path("state.json"))
				// Whatever it returns, nothing is written.
				_ = recordWith(f.env, Event{Kind: kind, Cwd: "/new"})
				for name, want := range map[string]string{
					f.sessionPath(sid, "lifecycle.json"): content,
					f.sessionPath(sid, "sesshin.json"):   sesshinBefore,
					f.path("state.json"):                 stateBefore,
				} {
					if got := bytesOf(t, name); got != want {
						t.Errorf("%s changed:\n%s\nwant\n%s", name, got, want)
					}
				}
				wantFormatLog(t, f, kind, "lifecycle.json in format "+strconv.Itoa(schema)+", not 1; left alone")
			})
		}
	}
}

func TestSesshinOtherFormat(t *testing.T) {
	for _, kind := range kinds {
		for _, schema := range []int{1, 3, 99} {
			t.Run(strconv.Itoa(int(kind))+"/"+strconv.Itoa(schema), func(t *testing.T) {
				f := newFix(t)
				place, _, _ := placed(kitty(5))
				f.env.Placement = place
				f.rec(Event{Kind: PostToolUse})
				content := strings.Replace(sesshinAt, "%d", strconv.Itoa(schema), 1)
				f.write(f.sessionPath(sid, "sesshin.json"), content)
				stateBefore := bytesOf(t, f.path("state.json"))
				_ = recordWith(f.env, Event{Kind: kind, Source: "resume"})
				if got := bytesOf(t, f.sessionPath(sid, "sesshin.json")); got != content {
					t.Errorf("sesshin.json changed:\n%s", got)
				}
				if got := bytesOf(t, f.path("state.json")); got != stateBefore {
					t.Errorf("state.json changed:\n%s", got)
				}
				wantFormatLog(t, f, kind, "sesshin.json in format "+strconv.Itoa(schema)+", not 2; left alone")
			})
		}
	}
}

// With state.json in another format no ID is issued: sesshin.json is written
// without one, and state.json is never rewritten.
func TestStateOtherFormat(t *testing.T) {
	for _, kind := range kinds {
		for _, schema := range []int{1, 3} {
			t.Run(strconv.Itoa(int(kind))+"/"+strconv.Itoa(schema), func(t *testing.T) {
				f := newFix(t)
				content := `{"schema": ` + strconv.Itoa(schema) + `, "last_id": 40}`
				f.write(f.path("state.json"), content)
				if kind == SessionEnd {
					f.rec2(Event{Kind: PostToolUse}) // adopts: the session exists
				}
				_ = recordWith(f.env, Event{Kind: kind})
				if got := bytesOf(t, f.path("state.json")); got != content {
					t.Errorf("state.json changed:\n%s", got)
				}
				if kind == SessionEnd {
					f.logs = nil
				} else if h := f.sesshin(sid); h.ID != nil {
					t.Errorf("id %d issued", *h.ID)
				}
				wantFormatLog(t, f, kind, "state.json in format "+strconv.Itoa(schema)+", not 2; left alone")
			})
		}
	}
}

// The pending sesshin.json stays pending while state.json is in another
// format, and the next lifecycle hook completes it once state.json is back.
func TestStateOtherFormatThenReplaced(t *testing.T) {
	f := newFix(t)
	f.write(f.path("state.json"), `{"schema": 1, "last_id": 40}`)
	f.rec2(Event{Kind: SessionStart, Source: "startup"})
	f.rec2(Event{Kind: PostToolUse})
	if got := bytesOf(t, f.sessionPath(sid, "sesshin.json")); !strings.Contains(got, `"id": null`) {
		t.Fatalf("sesshin.json %s", got)
	}
	if n := strings.Count(f.logged(), "state.json in format 1, not 2; left alone"); n != 1 {
		t.Errorf("logged %d times: %q", n, f.logged())
	}
	f.write(f.path("state.json"), `{"schema": 2, "last_id": 5, "migration": 1}`)
	f.rec(Event{Kind: PostToolUse})
	if f.sesshinID(sid) != 6 || f.lastID() != 6 || f.migration() != 1 {
		t.Errorf("id %d, last_id %d, migration %d", f.sesshinID(sid), f.lastID(), f.migration())
	}
}

// An existing null-id sesshin.json keeps it.
func TestStateOtherFormatKeepsNullID(t *testing.T) {
	f := newFix(t)
	f.write(f.sessionPath(sid, "sesshin.json"), pendingSesshin)
	f.write(f.path("state.json"), `{"schema": 3, "last_id": 40}`)
	_ = recordWith(f.env, Event{Kind: PostToolUse})
	if got := bytesOf(t, f.sessionPath(sid, "sesshin.json")); !strings.Contains(got, `"id": null`) {
		t.Errorf("sesshin.json %s", got)
	}
}

// rec2 records ev and ignores its error: the ID can't be issued.
func (f *fix) rec2(ev Event) { _ = recordWith(f.env, ev) }

// A rebuild that finds a sesshin.json in another format, whose id it can't
// read, issues no ID.
func TestRebuildForeignSesshin(t *testing.T) {
	for name, damage := range map[string]func(f *fix){
		"missing": func(f *fix) { os.Remove(f.path("state.json")) },
		"corrupt": func(f *fix) { f.write(f.path("state.json"), "{") },
	} {
		for _, kind := range []Kind{SessionStart, PostToolUse} {
			t.Run(name+"/"+strconv.Itoa(int(kind)), func(t *testing.T) {
				f := newFix(t)
				for _, id := range []string{sid, idB} {
					if err := recordWith(f.as(id), Event{Kind: PostToolUse}); err != nil {
						t.Fatal(err)
					}
				}
				foreign := strings.Replace(sesshinAt, "%d", "1", 1)
				f.write(f.sessionPath(idB, "sesshin.json"), foreign)
				damage(f)
				stateBefore := bytesOf(t, f.path("state.json"))
				// sid's own sesshin.json is not counted as another session's:
				// remove it so the new session is idC.
				_ = recordWith(f.as(idC), Event{Kind: kind})
				if got := f.sesshinID(idC); got != 0 {
					t.Errorf("id %d issued", got)
				}
				if got := bytesOf(t, f.sessionPath(idC, "sesshin.json")); !strings.Contains(got, `"id": null`) {
					t.Errorf("sesshin.json %s", got)
				}
				if got := bytesOf(t, f.path("state.json")); got != stateBefore {
					t.Errorf("state.json written: %s", got)
				}
				if got := bytesOf(t, f.sessionPath(idB, "sesshin.json")); got != foreign {
					t.Errorf("foreign sesshin.json changed: %s", got)
				}
				msg := "last_id not rebuilt: sesshin.json in another format"
				got := f.logged()
				if (kind == SessionStart) != strings.Contains(got, msg) {
					t.Errorf("%v log %q", kind, got)
				}
				// A corrupt state.json is logged by every hook; the
				// follow-up line is silent unless session-start.
				if kind != SessionStart && strings.Contains(got, "without an id") {
					t.Errorf("%v log %q", kind, got)
				}
			})
		}
	}
}
