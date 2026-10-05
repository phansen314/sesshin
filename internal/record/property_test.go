package record

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/proc"
)

// junk is what a payload might hold: well-formed values, values that fail
// their guards, control characters, line breaks, U+2028, invalid UTF-8,
// non-ASCII, and very long strings.
var junk = []string{
	"", "x", "auto", "manual", "startup", "resume", "clear", "fork", "compact", "other", "logout",
	"Bad Value", "UPPER", "a:b", "9lives", "with space", "ünï", "日本語", "line\nbreak", "cr\rlf", "tab\there",
	"nul\x00byte", "del\x7f", "c1\u0085nel", "ls ps ", "bad\xffutf8", "\xc3", "\xe2\x80",
	"acceptEdits", "plan", "default", "bypassPermissions", "a_b9", "p-1", "/home/me/proj", "\"quoted\"", "<&>",
	strings.Repeat("a", 64), strings.Repeat("a", 65), strings.Repeat("ü", 5000),
}

func pick(r *rand.Rand) string { return junk[r.IntN(len(junk))] }

var kinds = []Kind{SessionStart, UserPromptSubmit, PostToolUse, PostToolUseFailure, Stop, StopFailure,
	PermissionPrompt, ElicitationDialog, ElicitationComplete, PreCompact, PostCompact, SessionEnd}

// randomEvent is an event of a random kind with random values, some of which
// are junk.
func randomEvent(r *rand.Rand) Event {
	ev := Event{
		Kind:            kinds[r.IntN(len(kinds))],
		PromptID:        pick(r),
		PermissionMode:  pick(r),
		Cwd:             pick(r),
		TranscriptPath:  pick(r),
		Model:           pick(r),
		Source:          pick(r),
		Trigger:         pick(r),
		Reason:          pick(r),
		Error:           pick(r),
		BackgroundTasks: int64(r.IntN(5)) - 1,
		SessionCrons:    int64(r.IntN(5)) - 1,
		SessionTitle:    pick(r),
	}
	if r.IntN(4) == 0 {
		ev.BackgroundTasks = 1 << 62
	}
	switch r.IntN(3) {
	case 0:
		ev.Claude = &proc.Claude{}
	case 1:
		ev.Claude = &proc.Claude{PID: int64(r.IntN(1<<20)) + 1, StartedAt: "linux:" + pick(r)}
		if r.IntN(2) == 0 {
			n := r.IntN(2) == 0
			ev.Claude.Nested = &n
		}
	}
	return ev
}

// randomPayload is a JSON payload with fields of random types and values,
// decoded as a hook decodes it.
func randomPayload(r *rand.Rand) payload.Payload {
	value := func() string {
		switch r.IntN(5) {
		case 0:
			return fmt.Sprint(r.IntN(100))
		case 1:
			return "null"
		case 2:
			return `["a", {"b": 1}]`
		default:
			return fmt.Sprintf("%q", pick(r))
		}
	}
	var b strings.Builder
	b.WriteString(`{"session_id": "` + sid + `"`)
	for _, k := range []string{"hook_event_name", "prompt_id", "notification_type", "cwd", "transcript_path", "session_title",
		"model", "source", "reason", "trigger", "error", "permission_mode", "background_tasks", "session_crons"} {
		if r.IntN(3) > 0 {
			b.WriteString(`, "` + k + `": ` + value())
		}
	}
	b.WriteString("}")
	return payload.Decode(strings.NewReader(b.String()))
}

// damage replaces one of a session's files with something unusable, or
// removes it.
func damage(f *fix, r *rand.Rand, id string) {
	files := []string{f.sessionPath(id, "lifecycle.json"), f.sessionPath(id, "sesshin.json"), f.path("state.json")}
	path := files[r.IntN(len(files))]
	switch r.IntN(4) {
	case 0:
		os.Remove(path)
	case 1:
		os.WriteFile(path, []byte("{"), 0o600)
	case 2:
		os.WriteFile(path, []byte(`{"schema": 2}`), 0o600)
	case 3:
		os.WriteFile(path, []byte(`{"schema": 1, "last_id": "x", "id": -1, "session_id": 1}`), 0o600)
	}
}

// Hooks-spec, H6 and Format versions: every file the pipeline writes reads
// back usable through its model.Read*, over randomized events, payload
// values, environments, and damaged files; and event_seq counts exactly the
// events recorded on a usable file.
func TestWrittenFilesReadBack(t *testing.T) {
	for seed := uint64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) {
			r := rand.New(rand.NewPCG(seed, seed*7919))
			f := newFix(t)
			ids := []string{sid, idB, idC}
			place, _, _ := placed(kitty(1))
			f.env.Placement = place
			for i := range 250 {
				id := ids[r.IntN(len(ids))]
				env := f.as(id)
				env.Now = t0.Add(time.Duration(r.IntN(1e6)) * time.Second)
				f.setenv("CLAUDE_CODE_ENTRYPOINT", []string{"", "cli", "sdk-cli", "Bad Value"}[r.IntN(4)])
				f.setenv("TMUX", []string{"", "", "x"}[r.IntN(3)])
				f.setenv("CLAUDE_PID", []string{"", "1234", "x"}[r.IntN(3)])
				if r.IntN(6) == 0 {
					env.Lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{} }
				}
				if r.IntN(10) == 0 {
					damage(f, r, id)
				}
				var ev Event
				if r.IntN(2) == 0 {
					ev = randomEvent(r)
				} else {
					ev = FromPayload(kinds[r.IntN(len(kinds))], randomPayload(r))
				}
				var seq int64
				if data, err := os.ReadFile(f.sessionPath(id, "lifecycle.json")); err == nil {
					if l, res := model.ReadLifecycle(data, id); res.Usable {
						seq = l.EventSeq
					}
				}
				stateBefore, _ := os.ReadFile(f.path("state.json"))
				err := Record(env, ev)
				if err != nil && err != ErrNothingToRecord {
					t.Fatalf("event %d (%+v): %v\nlog:\n%s", i, ev, err, f.logged())
				}
				if err == ErrNothingToRecord {
					if ev.Kind != SessionEnd {
						t.Fatalf("event %d: nothing to record for %v", i, ev.Kind)
					}
					continue
				}
				l := f.life(id) // fails unless usable
				if want := seq + 1; l.EventSeq != want {
					t.Fatalf("event %d: event_seq %d, want %d", i, l.EventSeq, want)
				}
				f.sesshin(id)
				// state.json is written only when an ID is issued; a damaged one
				// the event had no use for is left as it was.
				if after, _ := os.ReadFile(f.path("state.json")); string(after) != string(stateBefore) && f.lastID() < 0 {
					t.Fatalf("event %d: no state.json", i)
				}
				if l.LastEventAt != model.FormatTimestamp(env.Now) {
					t.Fatalf("event %d: last_event_at %s", i, l.LastEventAt)
				}
			}
		})
	}
}

// Hooks-spec, Recording an event step 3 and design-spec, Event ordinal: 8
// writers of one session, 100 events each, leave event_seq at exactly 800.
// Each Record opens its own roots, as separate hooks would, so the locks
// contend as they do between processes.
func TestConcurrentWriters(t *testing.T) {
	f := newFix(t)
	const writers, each = 8, 100
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				kind := []Kind{PostToolUse, PostToolUseFailure, PostCompact, Stop}[(w+i)%4]
				if err := recordWith(f.env, Event{Kind: kind, PromptID: fmt.Sprint(w, "-", i)}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	l := f.life(sid)
	if l.EventSeq != writers*each {
		t.Errorf("event_seq %d, want %d", l.EventSeq, writers*each)
	}
	if f.sesshinID(sid) != 1 || f.lastID() != 1 {
		t.Errorf("id %d, last_id %d; one session, issued once", f.sesshinID(sid), f.lastID())
	}
	if got := f.logged(); got != "" {
		t.Errorf("log %q", got)
	}
}

// Sessions racing to be created each get their own ID, none twice: the state
// lock serializes issuing.
func TestConcurrentSessions(t *testing.T) {
	f := newFix(t)
	const n = 12
	var wg sync.WaitGroup
	for i := range n {
		id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := recordWith(f.as(id), Event{Kind: SessionStart, Source: "startup"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	seen := map[int64]bool{}
	for i := range n {
		id := f.sesshinID(fmt.Sprintf("%08d-0000-4000-8000-000000000000", i))
		if id < 1 || id > n || seen[id] {
			t.Errorf("id %d issued twice, or out of range", id)
		}
		seen[id] = true
	}
	if f.lastID() != n {
		t.Errorf("last_id %d, want %d", f.lastID(), n)
	}
}
