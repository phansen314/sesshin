package statusline

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/proc"
)

const sessionID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

// rig is one tick's directories and log.
type rig struct {
	t    *testing.T
	dir  string // the session directory
	logs []string
	tick Tick
}

// newRig makes a session directory with a usable lifecycle.json, the model's
// fixture.
func newRig(t *testing.T, stdin string) *rig {
	t.Helper()
	r := &rig{t: t, dir: filepath.Join(t.TempDir(), "sessions", sessionID)}
	lc, err := os.ReadFile("../model/testdata/lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(r.dir, model.LifecycleName), string(lc))
	r.tick = Tick{
		FS: fsys.OS{}, SessionDir: r.dir, Now: tickStart, Stdin: []byte(stdin),
		Payload: payload.Payload{SessionID: sessionID},
		Lookup:  func(fsys.FS, string) proc.Claude { return proc.Claude{} },
		Log:     func(s string) { r.logs = append(r.logs, s) },
	}
	return r
}

// run does steps 1–4 and 6 of the tick, as Run does, without rendering.
func (r *rig) run() {
	r.t.Helper()
	ses := openSession(r.tick)
	defer ses.close()
	s := defaultSteps()
	write(r.tick, ses, collect(r.tick, ses, s), s)
}

// collect does steps 1–4 only, returning the session it opened for the caller
// to close.
func (r *rig) collect() (*session, Found) {
	r.t.Helper()
	ses := openSession(r.tick)
	r.t.Cleanup(ses.close)
	return ses, collect(r.tick, ses, defaultSteps())
}

// render does step 5 for payload p, which names this rig's session unless it
// says otherwise.
func (r *rig) render(f Found, p payload.Payload, render func(View) []byte) []byte {
	r.t.Helper()
	if p.SessionID == "" {
		p.SessionID = sessionID
	}
	r.tick.Payload = p
	ses := openSession(r.tick)
	defer ses.close()
	return renderTick(r.tick, ses, f, zone, render)
}

func (r *rig) stored() (model.StatuslineFile, []byte) {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, model.StatuslineName))
	if err != nil {
		r.t.Fatal(err)
	}
	s, res := model.ReadStatusline(data)
	if !res.Usable {
		r.t.Fatalf("stored file unusable: %s\n%s", res.Reason(), data)
	}
	return s, data
}

func (r *rig) noFile() {
	r.t.Helper()
	if _, err := os.Stat(filepath.Join(r.dir, model.StatuslineName)); !os.IsNotExist(err) {
		r.t.Errorf("statusline.json written: %v", err)
	}
	r.noTemp()
}

// noTemp checks that no temp file was left behind.
func (r *rig) noTemp() {
	r.t.Helper()
	es, _ := os.ReadDir(r.dir)
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".") {
			r.t.Errorf("leftover %s", e.Name())
		}
	}
}

// A tick writes the file, the payload verbatim (key order, number text,
// escapes), and what steps 1–4 found, and the file reads back usable
// (hooks-spec.md, statusline, Effects).
func TestWrite(t *testing.T) {
	const body = `{"zeta":1.50,"cwd":"CWD","alpha":[1e2,"\u0041"],"session_id":"` + sessionID + `","cost":{"total_cost_usd":3.10}}`
	repo := t.TempDir()
	put(t, repo+"/.git/HEAD", "ref: refs/heads/main\n")
	r := newRig(t, strings.ReplaceAll(body, "CWD", repo))
	r.tick.Payload.Cwd = repo
	r.tick.Payload.Cost.TotalCostUSD = cost(3.1)
	r.tick.Lookup = func(_ fsys.FS, claudePID string) proc.Claude {
		if claudePID != "4242" {
			t.Errorf("CLAUDE_PID %q", claudePID)
		}
		return proc.Claude{PID: 4242, StartedAt: "linux:b:99"}
	}
	r.tick.ClaudePID = "4242"
	r.run()
	s, data := r.stored()
	if len(r.logs) != 0 {
		t.Errorf("logged %q", r.logs)
	}
	if s.ReceivedAt != "2026-10-03T18:31:51Z" || s.ReceivedNS != tickStart.UnixNano() {
		t.Errorf("received %s %d", s.ReceivedAt, s.ReceivedNS)
	}
	if s.GitBranch == nil || *s.GitBranch != "main" {
		t.Errorf("git_branch %v", s.GitBranch)
	}
	if s.CostSample == nil || s.CostSample.USD != 3.1 || s.CostSample.At != s.ReceivedAt || s.BurnUSDPerHour != nil {
		t.Errorf("cost_sample %+v, burn %v", s.CostSample, s.BurnUSDPerHour)
	}
	if s.PID == nil || *s.PID != 4242 || s.PIDStartedAt == nil || *s.PIDStartedAt != "linux:b:99" {
		t.Errorf("pid %v, started %v", s.PID, s.PIDStartedAt)
	}
	// Key order and number text survive the read back, as written.
	b, _ := s.Payload.MarshalJSON()
	if !strings.Contains(string(b), `"zeta":1.50`) || strings.Index(string(b), `"zeta"`) > strings.Index(string(b), `"cwd"`) {
		t.Errorf("payload read back as %s", b)
	}
	// And the stored bytes keep the escapes and number text as received.
	for _, want := range []string{`"zeta": 1.50`, `1e2`, `"\u0041"`, `"total_cost_usd": 3.10`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("file lacks %s:\n%s", want, data)
		}
	}
	if !bytes.HasSuffix(data, []byte("}\n")) {
		t.Errorf("no single trailing newline")
	}
	r.noTemp()
}

// A pid lookup that finds nothing writes null for both pid fields.
func TestWriteNoClaude(t *testing.T) {
	r := newRig(t, `{"cwd":"/"}`)
	r.run()
	s, _ := r.stored()
	if s.PID != nil || s.PIDStartedAt != nil || s.GitBranch != nil || s.CostSample != nil {
		t.Errorf("%+v", s)
	}
}

// The process is looked up on every tick, never reused from the previous
// one (hooks-spec.md, statusline step 2).
func TestFindEveryTick(t *testing.T) {
	r := newRig(t, `{}`)
	calls := 0
	r.tick.Lookup = func(fsys.FS, string) proc.Claude {
		calls++
		return proc.Claude{PID: int64(100 + calls), StartedAt: "linux:b:" + strconv.Itoa(calls)}
	}
	for i := 1; i <= 2; i++ {
		r.tick.Now = tickStart.Add(time.Duration(i) * time.Second)
		r.run()
		if s, _ := r.stored(); *s.PID != int64(100+i) {
			t.Errorf("tick %d: pid %d", i, *s.PID)
		}
	}
	if calls != 2 {
		t.Errorf("%d lookups", calls)
	}
}

// Two ticks: the sample carries forward, the rate is computed past the floor,
// and the previous file is read for it.
func TestSampleAcrossTicks(t *testing.T) {
	r := newRig(t, `{}`)
	for i, step := range []struct {
		at   time.Duration
		usd  float64
		rate *float64
		base float64
	}{
		{0, 1, nil, 1},
		{30 * time.Second, 2, nil, 1},
		{90 * time.Second, 4, f64(120), 1},
		{400 * time.Second, 5, f64(1.0 / 400 * 3600 * 4), 5},
	} {
		r.tick.Now = tickStart.Add(step.at)
		r.tick.Payload.Cost.TotalCostUSD = cost(step.usd)
		r.run()
		s, _ := r.stored()
		if s.CostSample.USD != step.base {
			t.Errorf("tick %d: sample %+v, want usd %v", i, s.CostSample, step.base)
		}
		if (s.BurnUSDPerHour == nil) != (step.rate == nil) || step.rate != nil && *s.BurnUSDPerHour != *step.rate {
			t.Errorf("tick %d: burn %v, want %v", i, s.BurnUSDPerHour, step.rate)
		}
	}
	if len(r.logs) != 0 {
		t.Errorf("logged %q", r.logs)
	}
}

// No write without a usable lifecycle.json (hooks-spec.md, statusline,
// Degraded); a missing one is silent, an unusable one is logged.
func TestNoLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		set    func(dir string)
		logged bool
	}{
		{"missing", func(d string) { os.Remove(d + "/lifecycle.json") }, false},
		{"corrupt", func(d string) { put(t, d+"/lifecycle.json", "{") }, true},
		{"another session's", func(d string) {
			b, _ := os.ReadFile(d + "/lifecycle.json")
			put(t, d+"/lifecycle.json", strings.Replace(string(b), sessionID, "0b0d3e5a-6c4e-4a43-9f43-5f1f0b1c2d3e", 1))
		}, true},
		{"another format", func(d string) { put(t, d+"/lifecycle.json", `{"schema": 99}`) }, false},
		{"no session directory", func(d string) { os.RemoveAll(d) }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, `{}`)
			tc.set(r.dir)
			r.run()
			if _, err := os.Stat(filepath.Join(r.dir, model.StatuslineName)); !os.IsNotExist(err) {
				t.Errorf("written: %v", err)
			}
			if _, err := os.Stat(r.dir); err == nil {
				r.noTemp()
			}
			if (len(r.logs) > 0) != tc.logged {
				t.Errorf("logged %q", r.logs)
			}
		})
	}
}

// A payload that can't be stored writes nothing, and is logged once
// (hooks-spec.md, statusline, Degraded): by the statusline as `payload not
// stored` when decoding accepted it, and otherwise by the hook that decoded
// it, so not again here.
func TestRefusedPayload(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 70) + `1` + strings.Repeat(`}`, 70)
	for _, tc := range []struct {
		name, body string
		logs       int // by the statusline
	}{
		{"invalid UTF-8", "{\"a\":\"\xff\"}", 1},
		{"too deep", deep, 1},
		{"not an object", `[1]`, 0},
		{"cut short", `{"a":`, 0},
		{"trailing data", `{"a":1} x`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.body)
			r.tick.Payload = payload.Decode(strings.NewReader(tc.body))
			r.tick.Payload.SessionID = sessionID
			if (r.tick.Payload.Err != nil) != (tc.logs == 0) {
				t.Fatalf("Decode error %v", r.tick.Payload.Err)
			}
			r.run()
			r.noFile()
			if len(r.logs) != tc.logs || tc.logs == 1 && !strings.HasPrefix(r.logs[0], "payload not stored: ") {
				t.Errorf("logged %q", r.logs)
			}
		})
	}
}

// Run opens the session directory once, and reads lifecycle.json once, for
// all of its steps.
func TestRunOpensOnce(t *testing.T) {
	r := newRig(t, `{"cwd":"/"}`)
	opens, reads := 0, 0
	r.tick.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		switch {
		case op.Name == fsys.OpOpenRoot && op.Path == r.dir:
			opens++
		case op.Name == fsys.OpReadFile && strings.HasSuffix(op.Path, model.LifecycleName):
			reads++
		}
		return nil
	}}
	Run(r.tick, zone)
	r.stored()
	if opens != 1 || reads != 1 {
		t.Errorf("%d opens of the session directory, %d reads of lifecycle.json; want 1 and 1", opens, reads)
	}
}

// The received_ns race (hooks-spec.md, statusline step 6): a newer stored
// tick wins, up to a minute ahead; an older, equal, or unusable one is
// replaced.
func TestReceivedNSRace(t *testing.T) {
	stored := func(ns int64) string {
		return strings.Replace(readFixture(t), "1791051112408117312", strconv.FormatInt(ns, 10), 1)
	}
	mine := tickStart.UnixNano()
	tests := []struct {
		name   string
		stored func() (string, bool) // content, present
		wins   bool                  // the stored file is kept
	}{
		{"newer", func() (string, bool) { return stored(mine + 1), true }, true},
		{"equal", func() (string, bool) { return stored(mine), true }, false},
		{"older", func() (string, bool) { return stored(mine - 1), true }, false},
		// A time further ahead than a minute was written before the clock
		// stepped back: it is replaced, though it is newer.
		{"a minute ahead", func() (string, bool) { return stored(mine + int64(time.Minute)), true }, true},
		{"past a minute ahead", func() (string, bool) { return stored(mine + int64(time.Minute) + 1), true }, false},
		{"unusable", func() (string, bool) { return "{", true }, false},
		{"missing", func() (string, bool) { return "", false }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, `{"mine":true}`)
			content, ok := tc.stored()
			if ok {
				put(t, r.dir+"/"+model.StatuslineName, content)
			}
			// The previous file is read at step 1, so a newer one landing
			// later is the race: collect first, then land it.
			ses, f := r.collect()
			if ok {
				put(t, r.dir+"/"+model.StatuslineName, content)
			}
			write(r.tick, ses, f, defaultSteps())
			data, _ := os.ReadFile(r.dir + "/" + model.StatuslineName)
			if kept := string(data) == content; kept != tc.wins {
				t.Errorf("stored file kept: %v, want %v", kept, tc.wins)
			}
			r.noTemp()
		})
	}
}

func readFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../model/testdata/statusline.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Each of steps 1–4 and 6 recovers its own panic and logs it, and costs only
// its own result.
func TestStepPanics(t *testing.T) {
	boom := func() { panic("boom") }
	good := defaultSteps()
	tests := []struct {
		name  string
		apply func(*steps)
		step  string
		check func(*testing.T, Found, *rig)
	}{
		{"step 1", func(s *steps) { s.previous = func(Tick, *session) *model.StatuslineFile { boom(); return nil } }, "step 1",
			func(t *testing.T, f Found, r *rig) {
				if f.Previous != nil || f.GitBranch == nil || f.Claude.PID != 7 {
					t.Errorf("%+v", f)
				}
			}},
		{"step 2", func(s *steps) { s.claude = func(Tick) proc.Claude { boom(); return proc.Claude{} } }, "step 2",
			func(t *testing.T, f Found, r *rig) {
				if f.Claude.PID != 0 || f.GitBranch == nil {
					t.Errorf("%+v", f)
				}
			}},
		{"step 3", func(s *steps) { s.branch = func(Tick) *string { boom(); return nil } }, "step 3",
			func(t *testing.T, f Found, r *rig) {
				if f.GitBranch != nil || f.Claude.PID != 7 || f.CostSample == nil {
					t.Errorf("%+v", f)
				}
			}},
		{"step 4", func(s *steps) { s.burn = func(Tick, *model.StatuslineFile) burnResult { boom(); return burnResult{} } }, "step 4",
			func(t *testing.T, f Found, r *rig) {
				if f.CostSample != nil || f.BurnUSDPerHour != nil || f.GitBranch == nil {
					t.Errorf("%+v", f)
				}
			}},
		{"step 6", func(s *steps) { s.write = func(Tick, *session, Found) { boom() } }, "step 6",
			func(t *testing.T, f Found, r *rig) { r.noFile() }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			put(t, repo+"/.git/HEAD", "ref: refs/heads/main\n")
			r := newRig(t, `{}`)
			r.tick.Payload.Cwd, r.tick.Payload.Cost.TotalCostUSD = repo, cost(1)
			r.tick.Lookup = func(fsys.FS, string) proc.Claude { return proc.Claude{PID: 7, StartedAt: "linux:b:1"} }
			s := good
			tc.apply(&s)
			ses := openSession(r.tick)
			defer ses.close()
			f := collect(r.tick, ses, s)
			write(r.tick, ses, f, s)
			tc.check(t, f, r)
			if len(r.logs) != 1 || !strings.Contains(r.logs[0], tc.step) || !strings.HasSuffix(r.logs[0], "panic: boom") {
				t.Errorf("logged %q", r.logs)
			}
		})
	}
}

// A panic in the write between Prepare and the rename leaves no temp file.
func TestWritePanicAfterPrepare(t *testing.T) {
	r := newRig(t, `{}`)
	n := 0
	r.tick.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpReadFile && strings.HasSuffix(op.Path, model.StatuslineName) {
			if n++; n == 2 { // the re-read
				panic("mid-write")
			}
		}
		return nil
	}}
	r.run()
	r.noFile()
	if len(r.logs) != 1 || !strings.HasSuffix(r.logs[0], "panic: mid-write") {
		t.Errorf("logged %q", r.logs)
	}
}

// A failed write is logged, and leaves nothing.
func TestWriteFails(t *testing.T) {
	r := newRig(t, `{}`)
	r.tick.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpRename {
			return syscall.EIO
		}
		return nil
	}}
	r.run()
	r.noFile()
	if len(r.logs) != 1 || !strings.Contains(r.logs[0], "write statusline.json") {
		t.Errorf("logged %q", r.logs)
	}
}

// An unusable previous statusline.json is no previous tick, and is replaced.
func TestUnusablePrevious(t *testing.T) {
	r := newRig(t, `{}`)
	put(t, r.dir+"/"+model.StatuslineName, `{"schema":"x"}`)
	r.tick.Payload.Cost.TotalCostUSD = cost(1)
	r.run()
	s, _ := r.stored()
	if s.CostSample == nil || s.CostSample.USD != 1 {
		t.Errorf("%+v", s)
	}
	if len(r.logs) != 1 || !strings.HasPrefix(r.logs[0], "statusline.json unusable") {
		t.Errorf("logged %q", r.logs)
	}
}

// A stored statusline.json in another format is left alone, not logged, and
// is no previous tick (design-spec.md, Format versions).
func TestOtherFormatStored(t *testing.T) {
	for _, schema := range []string{"0", "2", "99"} {
		r := newRig(t, `{}`)
		content := `{"schema":` + schema + `,"kept":true}`
		put(t, r.dir+"/"+model.StatuslineName, content)
		r.tick.Payload.Cost.TotalCostUSD = cost(1)
		r.run()
		if got, _ := os.ReadFile(r.dir + "/" + model.StatuslineName); string(got) != content {
			t.Errorf("schema %s: statusline.json is now %s", schema, got)
		}
		r.noTemp()
		if len(r.logs) != 0 {
			t.Errorf("schema %s: logged %q", schema, r.logs)
		}
	}
}

// The tier rule (design-spec.md, Two tiers): recording never reads sesshin.json,
// and nothing in statusline.json depends on it.
func TestNoSesshinJSON(t *testing.T) {
	var mu sync.Mutex
	var touched []string
	run := func(withSesshin bool) []byte {
		r := newRig(t, `{"cwd":"/"}`)
		if withSesshin {
			put(t, r.dir+"/sesshin.json", `{"schema": 2,"id":41,"job":null,"source":"hook","placement":null,"extra":{}}`)
		}
		r.tick.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
			mu.Lock()
			defer mu.Unlock()
			for _, p := range []string{op.Path, op.NewPath} {
				if strings.Contains(p, "sesshin.json") {
					touched = append(touched, op.Name+" "+p)
				}
			}
			return nil
		}}
		r.run()
		_, data := r.stored()
		return data
	}
	with, without := run(true), run(false)
	if len(touched) != 0 {
		t.Errorf("touched sesshin.json: %q", touched)
	}
	if !bytes.Equal(with, without) {
		t.Errorf("sesshin.json changed the file:\n%s\n%s", with, without)
	}
}

// The log's grammar (hooks-spec.md, Log): `read <file>: <error>` for an OS
// error, with the OS's words and no package prefix.
func TestLogGrammar(t *testing.T) {
	t.Run("previous unreadable", func(t *testing.T) {
		r := newRig(t, `{}`)
		r.tick.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, model.StatuslineName, 1, syscall.EIO)}
		r.run()
		if len(r.logs) != 1 || r.logs[0] != "read statusline.json: input/output error" {
			t.Errorf("logged %q", r.logs)
		}
		r.stored() // and the tick still wrote its own
	})
	t.Run("lifecycle unreadable", func(t *testing.T) {
		r := newRig(t, `{}`)
		r.tick.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadFile, model.LifecycleName, 1, syscall.EACCES)}
		r.run()
		r.noFile()
		if len(r.logs) != 1 || r.logs[0] != "read lifecycle.json: permission denied" {
			t.Errorf("logged %q", r.logs)
		}
	})
	t.Run("session directory", func(t *testing.T) {
		r := newRig(t, `{}`)
		r.tick.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpOpenRoot, "", 1, syscall.EACCES)}
		Run(r.tick, zone)
		if len(r.logs) != 1 || r.logs[0] != "open session directory: permission denied" {
			t.Errorf("logged %q", r.logs)
		}
	})
}
