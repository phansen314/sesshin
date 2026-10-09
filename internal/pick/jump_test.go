package pick

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/placement"
	"github.com/phansen314/sesshin/internal/placement/placementtest"
	"github.com/phansen314/sesshin/internal/proc"
	"github.com/phansen314/sesshin/internal/schematest"
)

// ---- the order ----

// sv is a session view for the order and the lines: an attention ("" for a
// null one), a cache ("" for a null prompt_cache; else warm, cold or
// unknown), the expiry of a warm one, and how long ago its last event was.
func sv(name, attention, cache string, expires time.Duration, quiet time.Duration) ops.SessionView {
	v := ops.SessionView{
		SessionID:   name,
		Name:        name,
		LastEventAt: model.FormatTimestamp(now.Add(-quiet)),
	}
	if attention != "" {
		v.Attention = &attention
	}
	if cache != "" {
		v.PromptCache = &ops.PromptCache{State: cache}
		if cache == "warm" {
			e := string(model.FormatTimestamp(now.Add(expires)))
			v.PromptCache.ExpiresAt = &e
		}
	}
	return v
}

func names(views []ops.SessionView) []string {
	var out []string
	for _, v := range views {
		out = append(out, v.Name)
	}
	return out
}

// The jump order over every attention, cache state and quiet time, ties
// included (picker-spec.md, Jump order). The sessions come in session order,
// which is the input's order here, and break ties.
func TestJumpOrder(t *testing.T) {
	m, h, d := time.Minute, time.Hour, 24*time.Hour
	in := []ops.SessionView{
		// Ties first, so session order is not alphabetical or by anything else.
		sv("tie-cold-blocked-1", "blocked", "cold", 0, 2*h),
		sv("tie-cold-blocked-2", "blocked", "cold", 0, 2*h),
		sv("tie-warm-yt", "your_turn", "warm", 30*m, 9*m),
		sv("tie-warm-blocked", "blocked", "warm", 30*m, 7*m),
		sv("working-new", "working", "warm", 5*m, 3*m),
		sv("unknown-old", "unknown", "", 0, 5*d),
		sv("idle-old", "idle", "cold", 0, 3*d),
		sv("selfwaking-new", "self_waking", "warm", 2*m, 4*m),
		sv("yt-nullcache-new", "your_turn", "", 0, 1*m),
		sv("yt-cold-old", "your_turn", "cold", 0, 2*d),
		sv("blocked-unknown-state", "blocked", "unknown", 0, 6*h),
		sv("stalled-cold", "stalled", "cold", 0, 3*h),
		sv("blocked-cold-new", "blocked", "cold", 0, 1*h),
		sv("working-old", "working", "", 0, 38*m),
		sv("yt-warm-late", "your_turn", "warm", 55*m, 10*m),
		sv("stalled-warm", "stalled", "warm", 20*m, 3*d),
		sv("yt-cold-new", "your_turn", "cold", 0, 5*m),
		sv("idle-new", "idle", "warm", 10*m, 5*m),
		sv("stalled-nullcache", "stalled", "", 0, 9*h),
		sv("null-attention", "", "", 0, 1*d),
		sv("selfwaking-old", "self_waking", "cold", 0, 9*h),
		sv("yt-unknown-state", "your_turn", "unknown", 0, 2*m),
		sv("blocked-warm-first", "blocked", "warm", 5*m, 1*d),
		sv("warm-no-expiry", "your_turn", "warm", 0, 1*m),
	}
	// A warm cache with no expires_at (a time no timestamp holds).
	in[len(in)-1].PromptCache.ExpiresAt = nil

	want := []string{
		// Tier 1, warm: the earliest expiry first, whatever the session wants.
		"blocked-warm-first", // +5m
		"stalled-warm",       // +20m
		"tie-warm-yt",        // +30m, tied with the next: session order
		"tie-warm-blocked",
		"yt-warm-late",   // +55m
		"warm-no-expiry", // no expires_at: after the rest of the warm
		// Cold: blocked, stalled, your_turn. Blocked by the earliest event
		// first (the longest waiting), your_turn by the latest.
		"tie-cold-blocked-1", // 2h
		"tie-cold-blocked-2", // 2h
		"blocked-cold-new",   // 1h
		"stalled-cold",
		"yt-cold-new", // 5m
		"yt-cold-old", // 2d
		// Unknown cache (a null prompt_cache counts), the same inside.
		"blocked-unknown-state",
		"stalled-nullcache",
		"yt-nullcache-new", // 1m
		"yt-unknown-state", // 2m
		// Then idle (the latest first), self_waking and working (the earliest
		// first), and unknown.
		"idle-new",
		"idle-old",
		"selfwaking-old",
		"selfwaking-new",
		"working-old",
		"working-new",
		"unknown-old",
		"null-attention",
	}
	got := slices.Clone(in)
	sortJump(got)
	if !slices.Equal(names(got), want) {
		t.Errorf("order:\n got  %q\n want %q", names(got), want)
	}

	// Ties keep the session order whatever it is: the reverse input keeps the
	// tied pairs reversed.
	rev := slices.Clone(in)
	slices.Reverse(rev)
	sortJump(rev)
	pos := func(vs []ops.SessionView, name string) int {
		return slices.IndexFunc(vs, func(v ops.SessionView) bool { return v.Name == name })
	}
	if pos(rev, "tie-cold-blocked-2") > pos(rev, "tie-cold-blocked-1") || pos(rev, "tie-warm-blocked") > pos(rev, "tie-warm-yt") {
		t.Errorf("ties not in session order, reversed: %q", names(rev))
	}
	// Everything else is the same whatever the input order.
	strip := func(vs []ops.SessionView) []string {
		var out []string
		for _, v := range vs {
			if !strings.HasPrefix(v.Name, "tie-") {
				out = append(out, v.Name)
			}
		}
		return out
	}
	if !slices.Equal(strip(got), strip(rev)) {
		t.Errorf("order depends on the input order:\n %q\n %q", strip(got), strip(rev))
	}

	// compareJump is antisymmetric and agrees with the order.
	for i, a := range got {
		for j, b := range got {
			if x, y := compareJump(a, b), compareJump(b, a); x != -y {
				t.Errorf("compare(%s,%s) = %d but reversed %d", a.Name, b.Name, x, y)
			}
			if (i < j) && compareJump(a, b) > 0 {
				t.Errorf("%s sorted before %s but compares greater", a.Name, b.Name)
			}
		}
	}
}

// Each key on its own, so a failure names the rule.
func TestCompareJump(t *testing.T) {
	m, h := time.Minute, time.Hour
	for _, tc := range []struct {
		name string
		a, b ops.SessionView // a sorts before b
	}{
		{"tier: your_turn over idle", sv("a", "your_turn", "cold", 0, 1*m), sv("b", "idle", "warm", 5*m, 1*m)},
		{"tier: idle over self_waking", sv("a", "idle", "", 0, 1*m), sv("b", "self_waking", "", 0, 9*h)},
		{"tier: self_waking over working", sv("a", "self_waking", "", 0, 1*m), sv("b", "working", "", 0, 9*h)},
		{"tier: working over unknown", sv("a", "working", "", 0, 1*m), sv("b", "unknown", "", 0, 9*h)},
		{"tier: unknown over a null attention's later", sv("a", "unknown", "", 0, 9*h), sv("b", "", "", 0, 1*m)},
		{"cache: warm over cold", sv("a", "your_turn", "warm", 50*m, 1*m), sv("b", "blocked", "cold", 0, 9*h)},
		{"cache: cold over unknown", sv("a", "your_turn", "cold", 0, 1*m), sv("b", "blocked", "unknown", 0, 9*h)},
		{"cache: cold over null", sv("a", "your_turn", "cold", 0, 1*m), sv("b", "blocked", "", 0, 9*h)},
		{"warm: earliest expiry, whatever it wants", sv("a", "your_turn", "warm", 10*m, 1*m), sv("b", "blocked", "warm", 20*m, 9*h)},
		{"cold: blocked over stalled", sv("a", "blocked", "cold", 0, 1*m), sv("b", "stalled", "cold", 0, 9*h)},
		{"cold: stalled over your_turn", sv("a", "stalled", "cold", 0, 1*m), sv("b", "your_turn", "cold", 0, 9*h)},
		{"blocked: the longest waiting", sv("a", "blocked", "cold", 0, 9*h), sv("b", "blocked", "cold", 0, 1*m)},
		{"stalled: the longest waiting", sv("a", "stalled", "", 0, 9*h), sv("b", "stalled", "", 0, 1*m)},
		{"your_turn: the latest first", sv("a", "your_turn", "unknown", 0, 1*m), sv("b", "your_turn", "unknown", 0, 9*h)},
		{"idle: the latest first", sv("a", "idle", "", 0, 1*m), sv("b", "idle", "", 0, 9*h)},
		{"self_waking: the earliest first", sv("a", "self_waking", "", 0, 9*h), sv("b", "self_waking", "", 0, 1*m)},
		{"working: the earliest first", sv("a", "working", "", 0, 9*h), sv("b", "working", "", 0, 1*m)},
		{"unknown: the earliest first", sv("a", "unknown", "", 0, 9*h), sv("b", "unknown", "", 0, 1*m)},
	} {
		if c := compareJump(tc.a, tc.b); c >= 0 {
			t.Errorf("%s: compare = %d, want < 0", tc.name, c)
		}
		if c := compareJump(tc.b, tc.a); c <= 0 {
			t.Errorf("%s: reversed compare = %d, want > 0", tc.name, c)
		}
	}
	// Ties.
	x, y := sv("x", "blocked", "cold", 0, h), sv("y", "blocked", "cold", 0, h)
	if compareJump(x, y) != 0 {
		t.Error("equal sessions do not tie")
	}
	w1, w2 := sv("w1", "your_turn", "warm", 10*m, m), sv("w2", "blocked", "warm", 10*m, h)
	if compareJump(w1, w2) != 0 {
		t.Error("equal expiries do not tie")
	}
}

// ---- the lines ----

func TestJumpLines(t *testing.T) {
	// 2:00PM, with a 1-hour cache: the spec's example.
	at := time.Date(2026, 10, 4, 14, 0, 0, 0, time.UTC)
	m, h := time.Minute, time.Hour
	view := func(n int64, att, cache string, exp time.Duration, tokens int64, quiet time.Duration, job, cwd, name string) ops.SessionView {
		v := ops.SessionView{
			ID: &n, SessionID: uuid(int(n)), Name: name, Attention: &att,
			LastEventAt: model.FormatTimestamp(at.Add(-quiet)),
		}
		if job != "" {
			v.Job = &job
		}
		v.Cwd = &cwd
		switch cache {
		case "warm":
			e := string(model.FormatTimestamp(at.Add(exp)))
			v.PromptCache = &ops.PromptCache{State: "warm", ExpiresAt: &e}
		case "cold":
			v.PromptCache = &ops.PromptCache{State: "cold", RecacheTokens: &tokens}
		}
		return v
	}
	views := []ops.SessionView{
		view(9, "your_turn", "warm", 48*m, 0, 12*m, "", "/home/p/code/sesshin", "attention design"),
		view(12, "blocked", "warm", 56*m, 0, 4*m, "api", "/home/p/code/api", "fix auth"),
		view(3, "stalled", "cold", 0, 80000, 3*h, "docs", "/home/p/notes", "#3"),
		view(7, "your_turn", "cold", 0, 45000, 2*h, "", "/home/p/code/web", "triage"),
		view(8, "idle", "", 0, 0, 5*m, "", "/home/p/code/infra", "#8"),
		view(5, "self_waking", "warm", 57*m, 0, 3*m, "", "/home/p/code/infra", "nightly"),
		view(4, "working", "warm", 22*m, 0, 38*m, "ci", "/home/p/code/api", "run e2e"),
	}
	rows := []string{
		"🙋  #9   —     your_turn    ♨️ until 2:48PM  12m  ~/code/sesshin  attention design",
		"🔐  #12  api   blocked      ♨️ until 2:56PM  4m   ~/code/api      fix auth",
		"⛔  #3   docs  stalled      🧊 ~80k          3h   ~/notes         #3",
		"🙋  #7   —     your_turn    🧊 ~45k          2h   ~/code/web      triage",
		"💤  #8   —     idle         —                5m   ~/code/infra    #8",
		"⏳  #5   —     self_waking  ♨️ until 2:57PM  3m   ~/code/infra    nightly",
		"🏃  #4   ci    working      ♨️ until 2:22PM  38m  ~/code/api      run e2e",
	}
	var want []string
	for i, r := range rows {
		want = append(want, views[i].SessionID+"\t"+r)
	}
	got := renderJumpLines(views, at, "/home/p")
	if !slices.Equal(got, want) {
		t.Errorf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Every column starts at the same cell on every line, each emoji two.
	for _, line := range got {
		_, shown, _ := strings.Cut(line, "\t")
		if w := cells(shown[:strings.LastIndex(shown, "  ")]); w != cells(rows[0][:strings.LastIndex(rows[0], "  ")]) {
			t.Errorf("columns not aligned on %q: %d", line, w)
		}
	}

	// No ID, no cwd, an unknown attention, a null attention.
	null := ops.SessionView{SessionID: uuid(1), Name: "abcd1234", LastEventAt: model.FormatTimestamp(at.Add(-2 * 24 * h))}
	unknown := "unknown"
	other := null
	other.Attention = &unknown
	for _, v := range []ops.SessionView{null, other} {
		l := renderJumpLines([]ops.SessionView{v}, at, "/home/p")
		if want := uuid(1) + "\t❓  —  —  unknown  —  2d  —  abcd1234"; l[0] != want {
			t.Errorf("line %q, want %q", l[0], want)
		}
	}
}

// What go-runewidth counts for the emoji, and what jump counts.
func TestCells(t *testing.T) {
	for s, want := range map[string]int{
		"♨️": 2, // U+2668 U+FE0F: runewidth says 1
		"🧊":  2,
		"🔐":  2, "⛔": 2, "🙋": 2, "💤": 2, "⏳": 2, "🏃": 2, "❓": 2,
		"♨️ until 2:48PM": 2 + 1 + 5 + 1 + 6,
		"abc":             3,
		"日本":              4,
		"️":               0,
	} {
		if got := cells(s); got != want {
			t.Errorf("cells(%q) = %d, want %d", s, got, want)
		}
	}
}

// The cache column renders as the statusline does, in now's location, and
// is — when unknown or null.
func TestCacheColumn(t *testing.T) {
	zone := time.FixedZone("UTC+2", 2*3600)
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC).In(zone)
	expires := "2026-10-04T13:05:00Z"
	tokens := func(n int64) *int64 { return &n }
	for _, tc := range []struct {
		name string
		pc   *ops.PromptCache
		want string
	}{
		{"null", nil, "—"},
		{"unknown", &ops.PromptCache{State: "unknown"}, "—"},
		{"warm", &ops.PromptCache{State: "warm", ExpiresAt: &expires}, "♨️ until 3:05PM"},
		{"warm without expires_at", &ops.PromptCache{State: "warm"}, "—"},
		{"cold 45k", &ops.PromptCache{State: "cold", RecacheTokens: tokens(45000)}, "🧊 ~45k"},
		{"cold 80 tokens", &ops.PromptCache{State: "cold", RecacheTokens: tokens(80)}, "🧊 ~80"},
		{"cold 1.2M", &ops.PromptCache{State: "cold", RecacheTokens: tokens(1234567)}, "🧊 ~1.2M"},
		{"cold, cost unknown", &ops.PromptCache{State: "cold"}, "🧊 cold"},
	} {
		if got := cacheColumn(ops.SessionView{PromptCache: tc.pc}, at); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Hour: "0s", 0: "0s", 59 * time.Second: "59s", time.Minute: "1m",
		59*time.Minute + time.Second: "59m", time.Hour: "1h", 24 * time.Hour: "1d", 400 * 24 * time.Hour: "400d",
	} {
		if got := elapsed(d); got != want {
			t.Errorf("elapsed(%v) = %q, want %q", d, got, want)
		}
	}
}

// Hostile fields stay on one line, under their own key.
func TestJumpHostileFields(t *testing.T) {
	job, cwd := "j\tk\n", "/tmp/a\tb"
	v := sv("x\t"+uuid(99)+"\tpwn\nnext\x1b[31m", "blocked", "", 0, time.Minute)
	v.SessionID, v.Job, v.Cwd = uuid(1), &job, &cwd
	lines := renderJumpLines([]ops.SessionView{v}, now, "/home/p")
	key, rest, _ := strings.Cut(lines[0], "\t")
	if len(lines) != 1 || key != uuid(1) || strings.ContainsAny(rest, "\t\n\x1b") {
		t.Errorf("line %q", lines)
	}
}

// ---- jump against the fake fzf ----

type focusCall struct {
	Socket string
	Window int64
}

// jumpFixture is the fixture with live sessions, a fake kitty, and a fake
// terminal that shows failures.
type jumpFixture struct {
	*fixture
	table    map[int64]string
	focuses  []focusCall
	focusErr error
	// window is whether the window lookup finds the session's window.
	window  bool
	shown   []string
	showErr error
	// hook, when set, wraps the file system in a fault.
	hook fsys.Hook
	// dirsAtFocus is the preview directories found each time focus looked
	// for the window: all gone before it runs.
	dirsAtFocus [][]string
}

const jumpPlacement = `{"terminal":"kitty","socket":"unix:/old","window_id":4}`

func newJumpFixture(t *testing.T) *jumpFixture {
	t.Helper()
	return &jumpFixture{fixture: newFixture(t), table: map[int64]string{}, window: true}
}

func (f *jumpFixture) env() JumpEnv {
	pe := f.fixture.env()
	re := pe.ReadEnv
	re.Backends = []placement.Backend{placementtest.Kitty{
		FindFn: func(socket string, pid int64) (int64, error) {
			left, _ := filepath.Glob(filepath.Join(f.run, "sesshin-*"))
			f.dirsAtFocus = append(f.dirsAtFocus, left)
			if f.window {
				return 22, nil
			}
			return 0, errors.New("no window on " + socket)
		},
		FocusFn: func(socket string, window int64) error {
			f.focuses = append(f.focuses, focusCall{socket, window})
			return f.focusErr
		},
	}}
	if f.hook != nil {
		re.FS = fsys.Fault{FS: re.FS, Hook: f.hook}
	}
	re.StartedAt = func(pid int64) (string, error) {
		if s, ok := f.table[pid]; ok {
			return s, nil
		}
		return "", proc.ErrNoProcess
	}
	sys := pe.Sys
	sys.ShowFailure = func(msg string) error {
		f.shown = append(f.shown, msg)
		return f.showErr
	}
	return JumpEnv{
		FocusEnv: ops.FocusEnv{ReadEnv: re},
		Sys:      sys,
	}
}

// statuses are the lifecycle's for each attention.
func status(attention string) func(*model.LifecycleFile) {
	return func(l *model.LifecycleFile) {
		switch attention {
		case "blocked":
			l.Status = model.StatusNeedsApproval
		case "stalled":
			r := "rate_limit"
			l.Status, l.StallReason = model.StatusWaiting, &r
		case "your_turn":
			l.Status = model.StatusWaiting
		case "self_waking":
			n := int64(1)
			l.Status, l.BackgroundTasks = model.StatusWaiting, &n
		case "idle":
			l.Status = model.StatusIdle
		case "working":
			l.Status = model.StatusWorking
		}
	}
}

// live adds session n, live, with the attention, its last event quiet ago, and
// a placement.
func (f *jumpFixture) live(n int, attention string, quiet time.Duration, mod ...func(*model.LifecycleFile)) string {
	f.t.Helper()
	pid := int64(1000 + n)
	f.table[pid] = fmt.Sprintf("s%d", pid)
	started := fmt.Sprintf("s%d", pid)
	id := f.add(n, quiet, append([]func(*model.LifecycleFile){func(l *model.LifecycleFile) {
		l.EndedAt, l.EndReason = nil, nil
		l.PID, l.PIDStartedAt = &pid, &started
		day := model.FormatTimestamp(now.Add(-48 * time.Hour))
		l.StartedAt, l.LastStartAt = day, day
		l.Status = "idle"
	}, status(attention)}, mod...)...)
	f.sesshin(n, "", jumpPlacement)
	return id
}

// cache gives session n a statusline with the prompt_cache JSON.
func (f *jumpFixture) cache(id, promptCache string) {
	f.t.Helper()
	b := fmt.Sprintf(`{"schema":1,"received_at":%q,"received_ns":1,"payload":{"prompt_cache":%s},"git_branch":null,"cost_sample":null,"burn_usd_per_hour":null,"pid":null,"pid_started_at":null}`,
		model.FormatTimestamp(now.Add(-time.Minute)), promptCache)
	if _, r := model.ReadStatusline([]byte(b)); !r.Usable {
		f.t.Fatalf("fixture statusline.json unusable: %s", r.Reason())
	}
	f.write(id, "statusline.json", b)
}

func (f *jumpFixture) warm(id string, expires time.Duration) {
	f.cache(id, fmt.Sprintf(`{"warm":true,"caching_observed":true,"expires_at":%d,"recache_tokens_if_cold":45000}`, now.Add(expires).Unix()))
}

func (f *jumpFixture) cold(id string, tokens int) {
	f.cache(id, fmt.Sprintf(`{"warm":false,"caching_observed":true,"recache_tokens_if_cold":%d}`, tokens))
}

// jump runs jump against the fixture and checks its envelope against the
// schemas.
func (f *jumpFixture) jump(in JumpInput) ops.Envelope {
	f.t.Helper()
	env := Jump(in, f.env())
	checkEnvelopeAs(f.t, env, "jump-output")
	return env
}

func jumpActions(t *testing.T, env ops.Envelope) []JumpAction {
	t.Helper()
	if !env.OK {
		t.Fatalf("jump failed: %+v", env.Error)
	}
	a := env.Result.(JumpOutput).Actions
	if a == nil {
		t.Fatal("actions is nil, not empty")
	}
	return a
}

// Live sessions in jump order, as lines: the headless and the ended are not
// candidates.
func TestJumpFixtureLines(t *testing.T) {
	f := newJumpFixture(t)
	m, h := time.Minute, time.Hour
	a := f.live(1, "working", 38*m, cwdOf(f.home+"/code/api"), title("run e2e"))
	f.cache(a, `{"warm":true,"caching_observed":true,"expires_at":1,"recache_tokens_if_cold":1}`) // expired: cold
	b := f.live(2, "blocked", 4*m, cwdOf(f.home+"/code/api"), title("fix auth"))
	f.warm(b, 56*m)
	f.sesshin(2, "api", jumpPlacement)
	c := f.live(3, "stalled", 3*h, title("docs"))
	f.cold(c, 80000)
	f.live(4, "your_turn", 12*m)
	f.add(5, time.Hour)            // ended
	f.live(6, "idle", m, headless) // headless
	f.live(7, "self_waking", 3*m, noTranscript)
	f.selects(1)

	env := f.jump(JumpInput{Query: "'blocked"})
	if len(jumpActions(t, env)) != 0 {
		t.Fatalf("%+v", env)
	}
	got := f.stdin()
	var order []string
	for _, line := range got {
		order = append(order, strings.SplitN(line, "\t", 2)[0])
	}
	if want := []string{uuid(2), uuid(3), uuid(4), uuid(7), uuid(1)}; !slices.Equal(order, want) {
		t.Errorf("order %q, want %q\n%s", order, want, strings.Join(got, "\n"))
	}
	fields := func(i int) []string { _, rest, _ := strings.Cut(got[i], "\t"); return strings.Fields(rest) }
	for i, want := range [][]string{
		{"🔐", "#2", "api", "blocked", "♨️", "until", "12:56PM", "4m", "~/code/api", "fix", "auth"},
		{"⛔", "#3", "—", "stalled", "🧊", "~80k", "3h", f.cwd, "docs"},
		{"🙋", "#4", "—", "your_turn", "—", "12m", f.cwd, "#4"},
		{"⏳", "#7", "—", "self_waking", "—", "3m", f.cwd, "#7"},
		{"🏃", "#1", "—", "working", "🧊", "~1", "38m", "~/code/api", "run", "e2e"},
	} {
		if !slices.Equal(fields(i), want) {
			t.Errorf("line %d: %q, want %q", i, fields(i), want)
		}
	}
}

// jump's fzf arguments: the options undone, then its own, then the person's.
func TestJumpOptions(t *testing.T) {
	f := newJumpFixture(t)
	f.live(1, "your_turn", time.Minute)
	f.selects(1)
	f.environ = []string{"SESSHIN_PICK_OPTS=--height 60% --layout 'reverse'"}
	f.jump(JumpInput{Query: "killed words"})
	want := append(append([]string{}, undone...),
		"--with-shell", "sh -c", "--no-multi", "--no-sort", "--delimiter", "\t", "--with-nth", "2..")
	cmd := f.argv()[len(want)+1] // the preview command, checked in TestJumpPreviewDir
	want = append(want, "--preview", cmd, "--preview-window", "down,50%",
		"--query", "killed words", "--height", "60%", "--layout", "reverse")
	if got := f.argv(); !slices.Equal(got, want) {
		t.Errorf("argv %q\nwant %q", got, want)
	}
	for _, a := range f.argv() {
		if a == "--multi" || a == "--bind" || a == "--tiebreak" {
			t.Errorf("restart's option %s passed", a)
		}
	}
	if f.catches != 1 {
		t.Errorf("interrupts caught %d times", f.catches)
	}
	// restart's arguments are unchanged: --multi, the preview, ctrl-a.
	ra := args("/d", "q", nil)
	if !slices.Contains(ra, "--multi") || !slices.Contains(ra, "ctrl-a:select-all") || slices.Contains(ra, "--no-sort") {
		t.Errorf("restart's args %q", ra)
	}
}

func TestJumpOutcomes(t *testing.T) {
	f := newJumpFixture(t)
	f.live(1, "your_turn", time.Minute)
	f.live(2, "blocked", time.Minute)

	// Enter: the line under the cursor, the first key.
	f.selects(0, uuid(1)+"\tx", uuid(2))
	a := jumpActions(t, f.jump(JumpInput{}))
	if len(a) != 1 || a[0].Operation != "focus" || a[0].Input.Session != uuid(1) || !a[0].Output.OK {
		t.Fatalf("actions %+v", a)
	}
	if len(f.focuses) != 1 {
		t.Errorf("focuses %v", f.focuses)
	}

	// A key not offered is ignored.
	f.focuses = nil
	f.selects(0, uuid(99))
	if a := jumpActions(t, f.jump(JumpInput{})); len(a) != 0 || len(f.focuses) != 0 {
		t.Errorf("actions %+v, focuses %v", a, f.focuses)
	}

	// Nothing matching: ok, nothing focused.
	f.selects(1)
	if a := jumpActions(t, f.jump(JumpInput{})); len(a) != 0 || len(f.focuses) != 0 {
		t.Errorf("actions %+v, focuses %v", a, f.focuses)
	}

	// Esc: cancelled.
	f.selects(130)
	env := f.jump(JumpInput{})
	wantError(t, env, KindCancelled, "")
	if env.Error.Message != "nothing was focused" || len(env.Error.Details) != 0 || len(f.focuses) != 0 {
		t.Errorf("%+v, focuses %v", env.Error, f.focuses)
	}

	// Anything else: fzf-failed, with the status.
	f.selects(2)
	env = f.jump(JumpInput{})
	wantError(t, env, KindUnavailable, "fzf-failed")
	if env.Error.Details["status"] != 2 {
		t.Errorf("%+v", env.Error)
	}
}

// focus: success, unverified, and failure are all recorded in actions, and
// jump succeeds whatever focus did.
func TestJumpFocus(t *testing.T) {
	f := newJumpFixture(t)
	f.live(1, "your_turn", time.Minute)
	f.selects(0, uuid(1))

	a := jumpActions(t, f.jump(JumpInput{}))
	out := a[0].Output
	if !out.OK || !out.Result.(ops.FocusOutput).Verified || len(f.focuses) != 1 || f.focuses[0].Window != 22 {
		t.Errorf("verified: %+v, focuses %v", out, f.focuses)
	}
	if got := out.Result.(ops.FocusOutput).Attention; got == nil || *got != "your_turn" {
		t.Errorf("attention %v", got)
	}

	f.window, f.focuses = false, nil
	a = jumpActions(t, f.jump(JumpInput{}))
	if fo := a[0].Output.Result.(ops.FocusOutput); !a[0].Output.OK || fo.Verified || len(f.focuses) != 1 || f.focuses[0] != (focusCall{"unix:/old", 4}) {
		t.Errorf("unverified: %+v, focuses %v", a[0].Output, f.focuses)
	}

	f.focusErr = errors.New("kitten @ focus-window: exit status 1")
	env := f.jump(JumpInput{})
	a = jumpActions(t, env) // jump itself ran
	if !env.OK || a[0].Output.OK || a[0].Output.Error.Kind != ops.KindTerminal || !strings.Contains(a[0].Output.Error.Message, "exit status 1") {
		t.Errorf("failed: %+v", a[0].Output)
	}
}

// Nothing to pick: ok, with no actions, fzf never opened, and no terminal
// needed.
func TestJumpNoCandidates(t *testing.T) {
	f := newJumpFixture(t)
	f.add(1, time.Hour)            // ended
	f.live(2, "idle", 1, headless) // headless
	f.noTTY = true
	f.selects(0, uuid(1))
	env := f.jump(JumpInput{})
	if len(jumpActions(t, env)) != 0 || f.ran() || f.catches != 0 {
		t.Errorf("%+v ran %v", env, f.ran())
	}
}

func TestJumpWarnings(t *testing.T) {
	f := newJumpFixture(t)
	f.live(1, "idle", time.Minute)
	f.write(uuid(1), "statusline.json", `{"schema":9}`) // another format: a warning, not a failure
	f.selects(1)
	env := f.jump(JumpInput{})
	if !env.OK || len(env.Warnings) == 0 {
		t.Errorf("%+v", env)
	}
}

// jump's checks come in the Errors order, with no terminal backend check.
func TestJumpErrorOrder(t *testing.T) {
	f := newJumpFixture(t)
	f.live(1, "idle", time.Minute)
	f.selects(1)

	_, e := ops.DecodeInput([]byte(`{"query":1,"args":[]}`), DecodeJumpInput)
	if e == nil || e.Kind != ops.KindInvalidInput {
		t.Fatalf("%+v", e)
	}

	f.noFzf, f.noTTY = true, true
	for _, k := range []string{"KITTY_LISTEN_ON", "KITTY_WINDOW_ID"} {
		delete(f.vars, k) // restart would fail with terminal here
	}
	f.environ = []string{"SESSHIN_PICK_OPTS='unterminated"}
	cfg := filepath.Join(f.loc.ConfigDir, "config.toml")
	if err := os.MkdirAll(f.loc.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("retain_days = [oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	live := f.loc.SessionsDir()
	moved := live + ".moved"
	if err := os.Rename(live, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, nil, 0o600); err != nil { // the load fails: sessions/ is a file
		t.Fatal(err)
	}

	step := func(wantKind, wantReason string) {
		t.Helper()
		got := f.jump(JumpInput{})
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
	step(ops.KindIO, "") // the load
	if err := os.Remove(live); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, live); err != nil {
		t.Fatal(err)
	}
	step(KindUnavailable, "no-terminal")
	f.noTTY = false
	f.selects(130)
	wantError(t, f.jump(JumpInput{}), KindCancelled, "")
	// No kitty in the environment, and jump still runs: focus reports.
	f.selects(0, uuid(1))
	a := jumpActions(t, f.jump(JumpInput{}))
	if len(a) != 1 {
		t.Errorf("%+v", a)
	}
}

// Step 6: a failure's message is shown on the terminal, for the failures
// jump raises and focus's, and not for a cancel or a success.
func TestShowFailure(t *testing.T) {
	f := newJumpFixture(t)
	f.live(1, "your_turn", time.Minute)
	show := func(env ops.Envelope) []string {
		t.Helper()
		f.shown = nil
		ShowFailure(env, f.env().Sys)
		return f.shown
	}

	// focus failed.
	f.selects(0, uuid(1))
	f.focusErr = errors.New("kitten @ focus-window: exit status 1")
	env := f.jump(JumpInput{})
	got := show(env)
	if len(got) != 1 || !strings.Contains(got[0], "exit status 1") || strings.ContainsAny(got[0], "\n\t") {
		t.Errorf("focus failed: %q", got)
	}
	// focus failed for a session with no placement: a conflict, also shown.
	f.focusErr = nil
	f.sesshin(1, "", "")
	if got := show(f.jump(JumpInput{})); len(got) != 1 || !strings.Contains(got[0], "no kitty placement") {
		t.Errorf("no placement: %q", got)
	}
	f.sesshin(1, "", jumpPlacement)

	// Success, verified and not: nothing shown.
	if got := show(f.jump(JumpInput{})); len(got) != 0 {
		t.Errorf("verified: %q", got)
	}
	f.window = false
	if got := show(f.jump(JumpInput{})); len(got) != 0 {
		t.Errorf("unverified: %q", got)
	}
	f.window = true

	// Cancelled, nothing picked: nothing shown.
	f.selects(130)
	if got := show(f.jump(JumpInput{})); len(got) != 0 {
		t.Errorf("cancelled: %q", got)
	}
	f.selects(1)
	if got := show(f.jump(JumpInput{})); len(got) != 0 {
		t.Errorf("nothing picked: %q", got)
	}

	// Failures before fzf: fzf missing, no terminal, fzf failed.
	f.noFzf = true
	if got := show(f.jump(JumpInput{})); len(got) != 1 || !strings.Contains(got[0], "fzf not found on PATH") {
		t.Errorf("fzf-missing: %q", got)
	}
	f.noFzf, f.noTTY = false, true
	if got := show(f.jump(JumpInput{})); len(got) != 1 || !strings.Contains(got[0], "no terminal") {
		t.Errorf("no-terminal: %q", got)
	}
	f.noTTY = false
	f.selects(2)
	if got := show(f.jump(JumpInput{})); len(got) != 1 || !strings.Contains(got[0], "status 2") {
		t.Errorf("fzf-failed: %q", got)
	}

	// A load error.
	if err := os.RemoveAll(f.loc.SessionsDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.loc.SessionsDir(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := show(f.jump(JumpInput{})); len(got) != 1 {
		t.Errorf("load error: %q", got)
	}

	// A terminal that does not open is not an error; nor is no seam.
	f.showErr = errors.New("no tty")
	if got := show(f.jump(JumpInput{})); len(got) != 1 {
		t.Errorf("%q", got)
	}
	ShowFailure(f.jump(JumpInput{}), System{})
}

func TestFailureMessage(t *testing.T) {
	msg, ok := failureMessage(ops.Failed(&ops.Error{Kind: "x", Message: "two\nlines\tand a tab"}))
	if !ok || msg != "x: two lines and a tab" {
		t.Errorf("%q %v", msg, ok)
	}
	if _, ok := failureMessage(ops.Succeeded(JumpOutput{Actions: []JumpAction{}})); ok {
		t.Error("success shown")
	}
	if _, ok := failureMessage(ops.Failed(&ops.Error{Kind: KindCancelled, Message: "cancelled"})); ok {
		t.Error("cancelled shown")
	}
}

// ---- the schemas ----

// The decoder and jump-input agree on every mutation of a valid input.
func TestJumpInputAgreesWithSchema(t *testing.T) {
	base := `{"query": "blocked"}`
	docs := append(schematest.Mutations(t, base), base, `{}`, `[]`, `null`, `1`, `{"query":1}`, `{"args":[]}`)
	for _, doc := range docs {
		_, e := ops.DecodeInput([]byte(doc), DecodeJumpInput)
		ok, _ := schematest.Check(t, "jump-input", []byte(doc))
		switch {
		case e == nil && !ok:
			t.Errorf("the decoder accepts %s, the schema rejects it", doc)
		case e != nil && ok:
			t.Errorf("the decoder rejects %s (%v), the schema accepts it", doc, e.Details["problems"])
		}
	}
	t.Logf("%d documents", len(docs))
}
