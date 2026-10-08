package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

const (
	tokenA = "3fa85f6457174562b3fc2c963f66afa6"
	tokenB = "9b2e4c1a7f3d4e8ba6c50d1f2e3a4b5c"
)

// kittyAt is a kitty placement as a reservation stores it.
func kittyAt(socket string, window int) string {
	return fmt.Sprintf(`{"terminal":"kitty","socket":%q,"window_id":%d}`, socket, window)
}

// rn is the name of the reservation of job (job-less when "") and tokenA.
func rn(job string) string { return model.ReservationName(job, tokenA) }

// reserve writes the reservation of job and tokenA (job-less when job is ""),
// a usable one created ago before now; placement is the JSON of its
// placement, "" for null.
func (f *pruneFixture) reserve(job string, ago time.Duration, placement string) {
	f.t.Helper()
	f.reserveToken(job, tokenA, ago, placement)
}

func (f *pruneFixture) reserveToken(job, token string, ago time.Duration, placement string) {
	f.t.Helper()
	if placement == "" {
		placement = "null"
	}
	jobText := "null"
	if job != "" {
		jobText = fmt.Sprintf("%q", job)
	}
	b := fmt.Sprintf(`{"schema":2,"job":%s,"token":%q,"created_at":%q,"placement":%s,"extra":{}}`,
		jobText, token, model.FormatTimestamp(f.ago(ago)), placement)
	name := model.ReservationName(job, token)
	if _, r := model.ReadReservation([]byte(b), name); !r.Usable {
		f.t.Fatalf("fixture reservation unusable: %s", r.Reason())
	}
	f.writeReservation(name, b)
}

func (f *pruneFixture) writeReservation(name, content string) {
	f.t.Helper()
	if err := os.MkdirAll(f.loc.ReservationsDir(), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.loc.ReservationsDir(), name), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// reserved reports whether the reservation of job and tokenA is there.
func (f *pruneFixture) reserved(job string) bool { return f.reservedFile(rn(job)) }

func (f *pruneFixture) reservedFile(name string) bool {
	_, err := os.Stat(filepath.Join(f.loc.ReservationsDir(), name))
	return err == nil
}

// removed is the output's reservations as "label:reason", in output order:
// the label is the stored job, else the file's name without its token and
// .json.
func removed(o PruneOutput) []string {
	out := []string{}
	for _, r := range o.ReservationsRemoved {
		out = append(out, label(r)+":"+r.Reason)
	}
	return out
}

func label(r ReservationItem) string {
	if r.Job != nil {
		return *r.Job
	}
	name := strings.TrimSuffix(r.File, ".json")
	if k, _, ok := model.ParseReservationName(r.File); ok && k != "" {
		return k
	}
	return name
}

// windows is a backend with one answer per socket: the window IDs it lists.
// A socket not in answers is no answer. asked counts the questions per
// socket.
type backend struct {
	answers map[string][]int64
	asked   map[string]int
}

func newBackend(answers map[string][]int64) *backend {
	return &backend{answers: answers, asked: map[string]int{}}
}

func (b *backend) windows(p *jsonio.Object) ([]int64, bool) {
	socket, _ := p.Get("socket")
	s, _ := socket.(string)
	b.asked[s]++
	ids, ok := b.answers[s]
	return ids, ok
}

// Each reason removes its reservation, and a reservation of each fresh kind
// is kept (design-spec.md, Reservations; operations.md, prune).
func TestPruneReservationReasons(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 0) // sessions/ exists, for the state lock
	be := newBackend(map[string][]int64{"unix:/gone": {1, 2}, "unix:/here": {7}})
	f.windows = be.windows

	// Removed, one for each reason.
	f.reserve("stranded", 121*time.Second, "")
	f.reserve("expired-null", 86401*time.Second, "")
	f.reserve("expired-launched", 86401*time.Second, kittyAt("unix:/here", 7))
	f.reserve("window-gone", time.Hour, kittyAt("unix:/gone", 9))
	hour := model.FormatTimestamp(f.ago(time.Hour))
	f.writeReservation(rn("unusable-json"), `{"schema":2`)
	f.writeReservation(rn("unusable-schema"), fmt.Sprintf(`{"schema":1,"job":"unusable-schema","token":%q,"created_at":%q,"placement":null}`, tokenA, hour)) // another format
	f.writeReservation(rn("unusable-job"), fmt.Sprintf(`{"schema":2,"job":"other","token":%q,"created_at":%q,"placement":null,"extra":{}}`, tokenA, hour))
	f.writeReservation(rn("unusable-token"), fmt.Sprintf(`{"schema":2,"job":"unusable-token","token":"XYZ","created_at":%q,"placement":null,"extra":{}}`, hour))
	f.writeReservation(rn("unusable-key"), fmt.Sprintf(`{"schema":2,"job":"unusable-key","token":%q,"created_at":%q,"placement":null,"extra":1}`, tokenA, hour))
	f.writeReservation(rn("unusable-time"), fmt.Sprintf(`{"schema":2,"job":"unusable-time","token":%q,"created_at":"2026-02-30T00:00:00Z","placement":null,"extra":{}}`, tokenA))
	// Named before tokens (<key>.json), or by a token that is not the file's:
	// unusable whatever it holds.
	f.writeReservation("legacy.json", fmt.Sprintf(`{"schema":2,"job":"legacy","token":%q,"created_at":%q,"placement":null,"extra":{}}`, tokenA, hour))
	f.writeReservation(rn("unusable-name"), fmt.Sprintf(`{"schema":2,"job":"unusable-name","token":%q,"created_at":%q,"placement":null,"extra":{}}`, tokenB, hour))
	// Unusable and old at once: unusable comes first. Expired and gone: expired.
	f.writeReservation(rn("unusable-old"), `{"schema":2,"job":"unusable-old","token":"x","created_at":"2020-01-01T00:00:00Z","placement":null,"extra":{}}`)
	// A reservation with no job is named for its token alone.
	f.reserveToken("", tokenB, 86401*time.Second, "")

	// Kept.
	f.reserve("fresh-null", 119*time.Second, "")
	f.reserve("edge-stranded", 120*time.Second, "") // more than 120, not 120
	f.reserve("edge-expired", 86400*time.Second, kittyAt("unix:/here", 7))
	f.reserve("launched", 2*time.Hour, kittyAt("unix:/here", 7))
	f.reserve("launched-unsure", 2*time.Hour, kittyAt("unix:/silent", 7)) // no answer
	f.reserve("other-terminal", 2*time.Hour, `{"terminal":"wezterm","pane":3}`)
	f.reserve("bad-kitty", 2*time.Hour, `{"terminal":"kitty","window_id":3}`) // the backend can't use it

	out, warnings := f.output(PruneInput{})
	want := []string{
		tokenB + ":expired", "expired-launched:expired", "expired-null:expired", "legacy:unusable", "stranded:stranded",
		"unusable-job:unusable", "unusable-json:unusable", "unusable-key:unusable", "unusable-name:unusable", "unusable-old:unusable",
		"unusable-schema:unusable", "unusable-time:unusable", "unusable-token:unusable", "window-gone:window-gone",
	}
	if got := removed(out); !slices.Equal(got, want) {
		t.Errorf("removed %v\nwant    %v", got, want)
	}
	for _, r := range out.ReservationsRemoved {
		if (r.CreatedAt == nil) != (r.Reason == "unusable") || (r.Job == nil) != (r.CreatedAt == nil && r.Reason == "unusable" || label(r) == tokenB) {
			t.Errorf("%s: job %v, created_at %v", r.File, r.Job, r.CreatedAt)
		}
		if r.Job != nil && *r.Job == "stranded" && *r.CreatedAt != model.FormatTimestamp(f.ago(121*time.Second)) {
			t.Errorf("stranded created_at %s", *r.CreatedAt)
		}
	}
	for _, job := range []string{"fresh-null", "edge-stranded", "edge-expired", "launched", "launched-unsure", "other-terminal", "bad-kitty"} {
		if !f.reserved(job) {
			t.Errorf("%s was removed", job)
		}
	}
	for _, r := range out.ReservationsRemoved {
		if f.reservedFile(r.File) {
			t.Errorf("%s was reported removed and is still there", r.File)
		}
	}
	if out.ReservationsSkippedLocked || out.SkippedLocked != 0 || len(out.Pruned) != 0 {
		t.Errorf("%+v", out)
	}
	// Each unusable reservation warns, with its path, and nothing else does.
	var paths []string
	for _, w := range warnings {
		if w.Kind != KindUnusableFile || !strings.Contains(w.Message, "reservation") || !strings.Contains(w.Message, "removed") {
			t.Errorf("warning %+v", w)
		}
		paths = append(paths, filepath.Base(w.Details["path"].(string)))
	}
	slices.Sort(paths)
	wantPaths := []string{"legacy.json", rn("unusable-job"), rn("unusable-json"), rn("unusable-key"), rn("unusable-name"), rn("unusable-old"), rn("unusable-schema"), rn("unusable-time"), rn("unusable-token")}
	slices.Sort(wantPaths)
	if !slices.Equal(paths, wantPaths) {
		t.Errorf("warned of %v, want %v", paths, wantPaths)
	}
	// The window question: once per socket, and never for a reservation
	// already stale by age or not launched.
	if want := map[string]int{"unix:/gone": 1, "unix:/here": 1, "unix:/silent": 1}; fmt.Sprint(be.asked) != fmt.Sprint(want) {
		t.Errorf("asked %v, want %v", be.asked, want)
	}
}

// A backend that gives no answer, or none at all, leaves a launched
// reservation to age alone.
func TestPruneReservationNoAnswer(t *testing.T) {
	for name, windows := range map[string]func(*jsonio.Object) ([]int64, bool){
		"no backend":        nil,
		"a failing backend": func(*jsonio.Object) ([]int64, bool) { return nil, false },
	} {
		t.Run(name, func(t *testing.T) {
			f := newPruneFixture(t)
			f.session(pidA, 0)
			f.windows = windows
			f.reserve("launched", 2*time.Hour, kittyAt("unix:/x", 1))
			f.reserve("old", 86401*time.Second, kittyAt("unix:/x", 1))
			out, _ := f.output(PruneInput{})
			if got := removed(out); !slices.Equal(got, []string{"old:expired"}) || !f.reserved("launched") {
				t.Errorf("removed %v", got)
			}
		})
	}
	// An answer that lists no window at all is an answer: the window is gone.
	f := newPruneFixture(t)
	f.session(pidA, 0)
	f.windows = func(*jsonio.Object) ([]int64, bool) { return []int64{}, true }
	f.reserve("launched", 2*time.Hour, kittyAt("unix:/x", 1))
	if out, _ := f.output(PruneInput{}); !slices.Equal(removed(out), []string{"launched:window-gone"}) {
		t.Errorf("removed %v", removed(out))
	}
}

// A window answer counts only for the token and placement it was asked about.
func TestPruneReservationAnswerVoided(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replace func(f *pruneFixture)
		want    []string
	}{
		{"nothing changes", func(f *pruneFixture) {}, []string{"job:window-gone"}},
		{"replaced by a new launch (another token, another name)", func(f *pruneFixture) {
			os.Remove(filepath.Join(f.loc.ReservationsDir(), rn("job")))
			f.reserveToken("job", tokenB, time.Hour, kittyAt("unix:/s", 9))
		}, nil},
		{"the token inside changes", func(f *pruneFixture) {
			f.writeReservation(rn("job"), fmt.Sprintf(`{"schema":2,"job":"job","token":%q,"created_at":%q,"placement":%s,"extra":{}}`,
				tokenB, model.FormatTimestamp(f.ago(time.Hour)), kittyAt("unix:/s", 9)))
		}, []string{"job:unusable"}},
		{"the placement changes to another window", func(f *pruneFixture) {
			f.reserve("job", time.Hour, kittyAt("unix:/s", 3))
		}, nil},
		{"the placement changes to another socket", func(f *pruneFixture) {
			f.reserve("job", time.Hour, kittyAt("unix:/other", 9))
		}, nil},
		{"the placement is cleared", func(f *pruneFixture) {
			f.reserve("job", time.Hour, "")
		}, []string{"job:stranded"}},
		{"replaced by an expired one", func(f *pruneFixture) {
			f.reserve("job", 2*day, kittyAt("unix:/s", 9))
		}, []string{"job:expired"}},
		{"replaced by an unusable one", func(f *pruneFixture) {
			f.writeReservation(rn("job"), "{")
		}, []string{"job:unusable"}},
		{"removed", func(f *pruneFixture) {
			os.Remove(filepath.Join(f.loc.ReservationsDir(), rn("job")))
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPruneFixture(t)
			f.session(pidA, 0) // sessions/ exists, for the state lock
			be := newBackend(map[string][]int64{"unix:/s": {1, 2, 3}, "unix:/other": {1}})
			f.windows = be.windows
			f.reserve("job", time.Hour, kittyAt("unix:/s", 9))
			f.hook = func(op fsys.Op) error {
				// Between the first read and the lock: the state lock is the
				// one on sessions/ itself.
				if op.Name == fsys.OpLock && filepath.Base(op.Root) == "sessions" {
					tc.replace(f)
				}
				return nil
			}
			out, _ := f.output(PruneInput{})
			if got := removed(out); !slices.Equal(got, tc.want) {
				t.Errorf("removed %v, want %v", got, tc.want)
			}
			if be.asked["unix:/s"] != 1 {
				t.Errorf("asked %v", be.asked)
			}
		})
	}
}

// A held state lock leaves every reservation for the next run, and says so;
// the sessions are judged all the same, first.
func TestPruneReservationsStateLockHeld(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry_run=%v", dry), func(t *testing.T) {
			f := newPruneFixture(t)
			be := newBackend(map[string][]int64{"unix:/s": {1}})
			f.windows = be.windows
			f.session(pidA, 40*day)
			f.reserve("old", 2*day, "")
			f.reserve("gone", time.Hour, kittyAt("unix:/s", 9))
			f.writeReservation(rn("bad"), "{")
			root, err := fsys.OS{}.OpenRoot(f.loc.SessionsDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			lock, err := root.Lock(0)
			if err != nil {
				t.Fatal(err)
			}
			out, warnings := f.output(PruneInput{DryRun: dry})
			if !out.ReservationsSkippedLocked || len(out.ReservationsRemoved) != 0 || len(warnings) != 0 {
				t.Errorf("%+v, warnings %+v", out, warnings)
			}
			if !slices.Equal(ids(out), []string{pidA}) || out.SkippedLocked != 0 {
				t.Errorf("sessions: pruned %v, skipped_locked %d", ids(out), out.SkippedLocked)
			}
			for _, job := range []string{"old", "gone", "bad"} {
				if !f.reserved(job) {
					t.Errorf("%s was removed", job)
				}
			}
			// Released, the next run takes them.
			lock.Unlock()
			out, _ = f.output(PruneInput{DryRun: dry})
			if out.ReservationsSkippedLocked || !slices.Equal(removed(out), []string{"bad:unusable", "gone:window-gone", "old:expired"}) {
				t.Errorf("after unlock: %+v", out)
			}
		})
	}
}

// A session lock held doesn't stop the reservations, and none is held while
// they are judged: the sessions' locks are released first.
func TestPruneReservationsAfterSessions(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 40*day)
	f.session(pidB, 41*day)
	f.reserve("old", 2*day, "")
	root, err := fsys.OS{}.OpenRoot(f.loc.SessionDir(pidA))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := root.Lock(0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	var locks []string
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpLock {
			locks = append(locks, filepath.Base(op.Root))
		}
		if op.Name == fsys.OpRemove || op.Name == fsys.OpRename {
			locks = append(locks, "-"+op.Name)
		}
		return nil
	}
	out, _ := f.output(PruneInput{})
	if out.SkippedLocked != 1 || !slices.Equal(removed(out), []string{"old:expired"}) {
		t.Errorf("%+v", out)
	}
	// B's lock and rename (it is the oldest), A's lock (held, skipped), then
	// the state lock, then the unlink.
	want := []string{pidB, "-rename", pidA, "sessions", "-remove"}
	var got []string
	for _, l := range locks {
		if l != "-remove" || len(got) < len(want) {
			got = append(got, l)
		}
	}
	if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Errorf("order %v, want it to begin %v", got, want)
	}
}

// A created_at later than now makes the clock unusable, as a session's
// last_event_at does: nothing is removed, sessions or reservations.
func TestPruneReservationUnusableClock(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry_run=%v", dry), func(t *testing.T) {
			f := newPruneFixture(t)
			f.windows = newBackend(nil).windows
			f.session(pidA, 400*day)
			f.reserve("old", 2*day, "")
			f.reserve("future", -time.Second, "")
			f.writeReservation(rn("bad"), "{")
			before := f.entries()
			beforeRes := reservationEntries(t, f)
			out, warnings := f.output(PruneInput{DryRun: dry})
			if out.Cutoff != nil || out.HeadlessCutoff != nil || len(out.Pruned) != 0 || len(out.ReservationsRemoved) != 0 ||
				out.ReservationsSkippedLocked || len(warnings) != 0 {
				t.Errorf("%+v %+v", out, warnings)
			}
			if !slices.Equal(before, f.entries()) || !slices.Equal(beforeRes, reservationEntries(t, f)) {
				t.Error("the disk changed")
			}
		})
	}
	// A created_at exactly now is usable.
	f := newPruneFixture(t)
	f.session(pidA, 0)
	f.reserve("now", 0, "")
	f.reserve("old", 2*day, "")
	if out, _ := f.output(PruneInput{}); !slices.Equal(removed(out), []string{"old:expired"}) {
		t.Errorf("removed %v", removed(out))
	}
	// An unusable reservation's created_at is not read: it can't date the clock.
	f = newPruneFixture(t)
	f.session(pidA, 0)
	f.writeReservation(rn("future"), fmt.Sprintf(`{"schema":2,"job":"future","token":"x","created_at":%q,"placement":null,"extra":{}}`, model.FormatTimestamp(f.now.Add(time.Hour))))
	if out, _ := f.output(PruneInput{}); out.Cutoff == nil || !slices.Equal(removed(out), []string{"future:unusable"}) {
		t.Errorf("%+v", out)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func reservationEntries(t *testing.T, f *pruneFixture) []string {
	t.Helper()
	var out []string
	ents, err := os.ReadDir(f.loc.ReservationsDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %v %d %d", e.Name(), info.Mode(), info.Size(), info.ModTime().UnixNano()))
	}
	return out
}

// dry_run says what a run would remove, through the same locks, and removes
// nothing.
func TestPruneReservationsDryRun(t *testing.T) {
	f := newPruneFixture(t)
	f.windows = newBackend(map[string][]int64{"unix:/s": {1}}).windows
	f.session(pidA, 0)
	f.reserve("old", 2*day, "")
	f.reserve("gone", time.Hour, kittyAt("unix:/s", 9))
	f.reserve("kept", time.Hour, kittyAt("unix:/s", 1))
	f.writeReservation(rn("bad"), "{")
	before := reservationEntries(t, f)
	dry, dryWarnings := f.output(PruneInput{DryRun: true})
	if !dry.DryRun || !slices.Equal(before, reservationEntries(t, f)) {
		t.Fatalf("dry_run %v, or the disk changed", dry.DryRun)
	}
	if !slices.Equal(removed(dry), []string{"bad:unusable", "gone:window-gone", "old:expired"}) {
		t.Errorf("removed %v", removed(dry))
	}
	if len(dryWarnings) != 1 || !strings.Contains(dryWarnings[0].Message, "would be removed") {
		t.Errorf("warnings %+v", dryWarnings)
	}
	real, _ := f.output(PruneInput{})
	dry.DryRun = false
	if a, b := mustJSON(t, dry), mustJSON(t, real); a != b {
		t.Errorf("dry run said %s, the run %s", a, b)
	}
	if !f.reserved("kept") || f.reserved("old") || f.reserved("gone") || f.reserved("bad") {
		t.Error("the run left the wrong reservations")
	}
	// Retry-safe: nothing is left for the next one.
	if out, _ := f.output(PruneInput{}); len(out.ReservationsRemoved) != 0 {
		t.Errorf("rerun removed %v", removed(out))
	}
}

// Only a visible regular file whose name ends in .json is a reservation;
// everything else is left alone, whatever it holds. A .json name that is not
// <key>_<token>.json or <token>.json is unusable, and removed.
func TestPruneReservationsIgnoresEntries(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 0)
	old := fmt.Sprintf(`{"schema":2,"job":"x","token":%q,"created_at":"2020-01-01T00:00:00Z","placement":null,"extra":{}}`, tokenA)
	for _, name := range []string{".hidden.json", ".sesshin-tmp-123", "notes.txt", "job.json.bak", "job", rn("a") + ".bak", ".json"} {
		f.writeReservation(name, old)
	}
	if err := os.MkdirAll(filepath.Join(f.loc.ReservationsDir(), "dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.writeReservation("dir.json/inner.json", old)
	if err := os.Symlink(filepath.Join(f.loc.ReservationsDir(), "notes.txt"), filepath.Join(f.loc.ReservationsDir(), "link.json")); err != nil {
		t.Fatal(err)
	}
	before := reservationEntries(t, f)
	out, warnings := f.output(PruneInput{})
	if len(out.ReservationsRemoved) != 0 || len(warnings) != 0 || out.ReservationsSkippedLocked {
		t.Errorf("%+v %+v", out, warnings)
	}
	if !slices.Equal(before, reservationEntries(t, f)) {
		t.Error("an ignored entry changed")
	}
	// The valid names beside them are judged: a 64-character job is one.
	long := strings.Repeat("a", 64)
	f.reserve(long, 2*day, "")
	f.reserve("a", 2*day, "")
	f.reserve("1-2", 2*day, "")
	if out, _ := f.output(PruneInput{}); !slices.Equal(removed(out), []string{"1-2:expired", "a:expired", long + ":expired"}) {
		t.Errorf("removed %v", removed(out))
	}
	// Other names ending in .json are unusable, however they came by it.
	for _, name := range []string{"Bad_Job.json", "12.json", "-a.json", "a-.json", "a.b.json", strings.Repeat("a", 65) + ".json", "api_x.json", "api.json"} {
		f.writeReservation(name, old)
	}
	out, _ = f.output(PruneInput{})
	if len(out.ReservationsRemoved) != 8 {
		t.Fatalf("removed %v", removed(out))
	}
	for _, r := range out.ReservationsRemoved {
		if r.Reason != "unusable" || r.Job != nil || r.CreatedAt != nil {
			t.Errorf("%+v", r)
		}
	}
}

// No reservations/ is no reservations, whether or not sessions/ is there,
// and prune never creates it. An empty one is as good.
func TestPruneReservationsMissingDirectory(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 40*day)
	out, warnings := f.output(PruneInput{})
	if len(out.ReservationsRemoved) != 0 || out.ReservationsSkippedLocked || len(warnings) != 0 || len(out.Pruned) != 1 {
		t.Errorf("%+v", out)
	}
	if _, err := os.Stat(f.loc.ReservationsDir()); !os.IsNotExist(err) {
		t.Errorf("reservations/ was created: %v", err)
	}
	if err := os.MkdirAll(f.loc.ReservationsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, _ := f.output(PruneInput{}); len(out.ReservationsRemoved) != 0 || out.ReservationsSkippedLocked {
		t.Errorf("%+v", out)
	}
	// The output always carries both fields, empty or not.
	if out.ReservationsRemoved == nil {
		t.Error("reservations_removed is null")
	}
}

// A removal that fails stops the run with io for that path; the ones before
// it stay removed, and a rerun finishes the rest.
func TestPruneReservationRemoveFails(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 0)
	for _, job := range []string{"a-job", "b-job", "c-job"} {
		f.reserve(job, 2*day, "")
	}
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpRemove && strings.Contains(op.Path, "b-job") {
			return syscall.EACCES
		}
		return nil
	}
	env := f.run(PruneInput{})
	path := filepath.Join(f.loc.ReservationsDir(), rn("b-job"))
	if env.OK || env.Error.Kind != KindIO || env.Error.Details["path"] != path || env.Error.Details["code"] != "EACCES" {
		t.Fatalf("%+v", env)
	}
	if f.reserved("a-job") || !f.reserved("b-job") || !f.reserved("c-job") {
		t.Errorf("a %v, b %v, c %v", f.reserved("a-job"), f.reserved("b-job"), f.reserved("c-job"))
	}
	f.hook = nil
	if out, _ := f.output(PruneInput{}); !slices.Equal(removed(out), []string{"b-job:expired", "c-job:expired"}) {
		t.Errorf("rerun removed %v", removed(out))
	}
}

// Reservations are never written, and state.json is never touched.
func TestPruneReservationsWriteNothingElse(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 0)
	f.reserve("old", 2*day, "")
	state := filepath.Join(f.loc.StateDir, "state.json")
	if err := os.WriteFile(state, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.output(PruneInput{})
	if b, _ := os.ReadFile(state); string(b) != "not json" {
		t.Errorf("state.json is %q", b)
	}
	left, _ := os.ReadDir(f.loc.StateDir)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	if want := []string{"reservations", "sessions", "state.json"}; !slices.Equal(names, want) {
		t.Errorf("state directory holds %v, want %v", names, want)
	}
}

// Prune reads <key>_<token>.json and <token>.json: a usable reservation is
// reported as its job is stored, in its case, and with its file name; a name
// that is not one of the two, or whose key is not the job's, is unusable.
func TestPruneReservationKey(t *testing.T) {
	f := newPruneFixture(t)
	f.session(pidA, 0) // sessions/ exists, for the state lock
	old := func(job string) string {
		return fmt.Sprintf(`{"schema":2,"job":%q,"token":%q,"created_at":%q,"placement":null,"extra":{}}`, job, tokenA, model.FormatTimestamp(f.ago(time.Hour)))
	}
	f.writeReservation(rn("api"), old("API"))
	f.writeReservation("Web_"+tokenA+".json", old("Web"))
	f.writeReservation(rn("bad"), old("other"))
	f.writeReservation("legacy.json", old("legacy"))
	f.reserveToken("", tokenB, time.Hour, "")

	out, _ := f.output(PruneInput{})
	var got []string
	for _, r := range out.ReservationsRemoved {
		job := "<null>"
		if r.Job != nil {
			job = *r.Job
		}
		got = append(got, r.File+" "+job+" "+r.Reason)
	}
	want := []string{
		model.ReservationName("", tokenB) + " <null> stranded",
		"Web_" + tokenA + ".json <null> unusable",
		rn("api") + " API stranded",
		rn("bad") + " <null> unusable",
		"legacy.json <null> unusable",
	}
	if !slices.Equal(got, want) {
		t.Errorf("removed\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, name := range want {
		if f.reservedFile(strings.Fields(name)[0]) {
			t.Errorf("%s was not removed", name)
		}
	}
}
