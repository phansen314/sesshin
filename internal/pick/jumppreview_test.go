package pick

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/ops"
)

// ---- jumpPreview ----

// at is the spec's example time, 2:00PM.
var at = time.Date(2026, 10, 8, 14, 0, 0, 0, time.UTC)

func ref[T any](v T) *T { return &v }

// pv is a bare session view for the preview: #12, blocked, quiet for 4m.
func pv() ops.SessionView {
	return ops.SessionView{
		ID: ref(int64(12)), SessionID: uuid(12), Name: "fix auth",
		Liveness: "live", Status: "needs_approval", Attention: ref("blocked"),
		LastEventAt: model.FormatTimestamp(at.Add(-4 * time.Minute)),
	}
}

// previewRows are text's rows, in order: label, then value. The extra block
// is not parsed; its label, when it is a row, is.
func previewRows(text string) (labels []string, values map[string]string) {
	values = map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if line == "extra:" { // the block follows
			labels = append(labels, "extra")
			break
		}
		label, val, _ := strings.Cut(line, ":")
		labels = append(labels, label)
		values[label] = strings.TrimLeft(val, " ")
	}
	return labels, values
}

func jp(v ops.SessionView) string { return jumpPreview(v, at, "/home/p", titleOf) }

func TestJumpPreviewExample(t *testing.T) {
	v := pv()
	v.Job, v.Cwd, v.GitBranch = ref("api"), ref("/home/p/code/api"), ref("main")
	v.Model, v.PermissionMode = ref("claude-opus-5-5"), ref("acceptEdits")
	e := string(model.FormatTimestamp(at.Add(56 * time.Minute)))
	v.PromptCache = &ops.PromptCache{
		State: "warm", ExpiresAt: &e, HitRatio: ref(0.92), Misses: ref(int64(3)),
		LastMissCause: &[]string{"ttl", "edit"},
	}
	v.Metrics = &ops.Metrics{
		CostUSD: ref(4.12), BurnUSDPerHour: ref(1.80),
		ContextPercent: ref(56.4), ContextTokens: ref(int64(112000)), ContextWindow: ref(int64(200000)),
	}
	v.Placement = obj(t, `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":"api review"}`)
	v.Extra = obj(t, `{"ticket":"auth-4","note":"waiting on review"}`)
	want := `attention:       🔐 blocked
status:          needs_approval
quiet:           4m
cache:           ♨️ until 2:56PM
hit ratio:       92% (3 misses; last: ttl, edit)
context:         56% 112k/200k
cost:            $4.12 ($1.80/h)
session:         #12 fix auth
job:             api
cwd:             ~/code/api
git_branch:      main
model:           claude-opus-5-5
permission_mode: acceptEdits
tab title:       api review
extra:
{
  "ticket": "auth-4",
  "note": "waiting on review"
}
`
	if got := jp(v); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Each part in order, and the rows that show only when they say something.
func TestJumpPreviewWants(t *testing.T) {
	// Everything present: part 1 in order.
	v := pv()
	v.Liveness, v.StallReason = "unknown", ref("rate_limit")
	v.Pending = &ops.Pending{BackgroundTasks: 2, SessionCrons: 1}
	labels, vals := previewRows(jp(v))
	want := []string{"attention", "status", "liveness", "quiet", "stall_reason", "pending", "cache", "hit ratio", "context", "cost",
		"session", "job", "cwd", "git_branch", "model", "permission_mode", "tab title", "extra"}
	if !slices.Equal(labels, want) {
		t.Errorf("rows %q\nwant %q", labels, want)
	}
	for k, w := range map[string]string{"attention": "🔐 blocked", "liveness": "unknown", "quiet": "4m", "stall_reason": "rate_limit",
		"pending": "2 background, 1 cron", "status": "needs_approval"} {
		if vals[k] != w {
			t.Errorf("%s: %q, want %q", k, vals[k], w)
		}
	}

	for _, tc := range []struct {
		name      string
		mod       func(*ops.SessionView)
		attention string
		absent    []string
	}{
		{"blocked", func(v *ops.SessionView) {}, "🔐 blocked", []string{"liveness", "stall_reason", "pending"}},
		{"stalled", func(v *ops.SessionView) { v.Attention, v.StallReason = ref("stalled"), ref("billing") }, "⛔ stalled", []string{"liveness", "pending"}},
		{"self-waking", func(v *ops.SessionView) {
			v.Attention, v.Pending = ref("self_waking"), &ops.Pending{BackgroundTasks: 1}
		}, "⏳ self_waking", []string{"liveness", "stall_reason"}},
		{"pending both 0", func(v *ops.SessionView) { v.Pending = &ops.Pending{} }, "🔐 blocked", []string{"pending"}},
		{"pending crons only", func(v *ops.SessionView) { v.Pending = &ops.Pending{SessionCrons: 3} }, "🔐 blocked", nil},
		{"no attention", func(v *ops.SessionView) { v.Attention = nil }, "❓ unknown", nil},
		{"unknown value", func(v *ops.SessionView) { v.Attention = ref("odd\tvalue") }, "❓ odd value", nil},
		{"liveness unknown", func(v *ops.SessionView) { v.Liveness = "unknown" }, "🔐 blocked", []string{"stall_reason", "pending"}},
	} {
		v := pv()
		tc.mod(&v)
		_, vals := previewRows(jp(v))
		if vals["attention"] != tc.attention {
			t.Errorf("%s: attention %q, want %q", tc.name, vals["attention"], tc.attention)
		}
		for _, a := range tc.absent {
			if _, ok := vals[a]; ok {
				t.Errorf("%s: has a %s row", tc.name, a)
			}
		}
	}
	// Pending with only crons is shown with its zero.
	v = pv()
	v.Pending = &ops.Pending{SessionCrons: 3}
	if _, vals := previewRows(jp(v)); vals["pending"] != "0 background, 3 cron" {
		t.Errorf("pending %q", vals["pending"])
	}
	// Quiet is the Quiet column's.
	v = pv()
	v.LastEventAt = model.FormatTimestamp(at.Add(-50 * time.Hour))
	if _, vals := previewRows(jp(v)); vals["quiet"] != "2d" {
		t.Errorf("quiet %q", vals["quiet"])
	}
}

func TestJumpPreviewCache(t *testing.T) {
	warm := string(model.FormatTimestamp(at.Add(56 * time.Minute)))
	for _, tc := range []struct {
		name string
		pc   *ops.PromptCache
		want string
	}{
		{"warm", &ops.PromptCache{State: "warm", ExpiresAt: &warm}, "♨️ until 2:56PM"},
		{"cold", &ops.PromptCache{State: "cold", RecacheTokens: ref(int64(80000))}, "🧊 ~80k"},
		{"cold, no tokens", &ops.PromptCache{State: "cold"}, "🧊 cold"},
		{"unknown", &ops.PromptCache{State: "unknown"}, "—"},
		{"null", nil, "—"},
	} {
		v := pv()
		v.PromptCache = tc.pc
		if _, vals := previewRows(jp(v)); vals["cache"] != tc.want {
			t.Errorf("%s: cache %q, want %q", tc.name, vals["cache"], tc.want)
		}
		// The Cache column's own function gives it.
		if got := cacheColumn(v, at); got != tc.want {
			t.Errorf("%s: cacheColumn %q", tc.name, got)
		}
	}
}

func TestJumpPreviewHitRatio(t *testing.T) {
	causes := func(c ...string) *[]string { return &c }
	for _, tc := range []struct {
		name string
		pc   *ops.PromptCache
		want string
	}{
		{"all", &ops.PromptCache{HitRatio: ref(0.92), Misses: ref(int64(3)), LastMissCause: causes("ttl", "edit")}, "92% (3 misses; last: ttl, edit)"},
		{"hit_ratio null", &ops.PromptCache{Misses: ref(int64(3)), LastMissCause: causes("ttl", "edit")}, "— (3 misses; last: ttl, edit)"},
		{"misses null", &ops.PromptCache{HitRatio: ref(0.92), LastMissCause: causes("ttl", "edit")}, "92% (last: ttl, edit)"},
		{"last_miss_cause null", &ops.PromptCache{HitRatio: ref(0.92), Misses: ref(int64(3))}, "92% (3 misses)"},
		{"only misses", &ops.PromptCache{Misses: ref(int64(3))}, "— (3 misses)"},
		{"only cause", &ops.PromptCache{LastMissCause: causes("ttl")}, "— (last: ttl)"},
		{"only hit_ratio", &ops.PromptCache{HitRatio: ref(0.5)}, "50%"},
		{"zero", &ops.PromptCache{HitRatio: ref(0.0)}, "0%"},
		{"all null", &ops.PromptCache{State: "warm"}, "—"},
		{"empty causes", &ops.PromptCache{HitRatio: ref(0.92), Misses: ref(int64(3)), LastMissCause: causes()}, "92% (3 misses)"},
		{"empty causes only", &ops.PromptCache{LastMissCause: causes()}, "—"},
		{"one miss", &ops.PromptCache{HitRatio: ref(0.92), Misses: ref(int64(1))}, "92% (1 miss)"},
		{"zero misses", &ops.PromptCache{Misses: ref(int64(0))}, "— (0 misses)"},
		{"two causes", &ops.PromptCache{Misses: ref(int64(2)), LastMissCause: causes("a", "b")}, "— (2 misses; last: a, b)"},
		{"no prompt_cache", nil, "—"},
	} {
		v := pv()
		v.PromptCache = tc.pc
		if _, vals := previewRows(jp(v)); vals["hit ratio"] != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, vals["hit ratio"], tc.want)
		}
	}
}

func TestJumpPreviewContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    *ops.Metrics
		want string
	}{
		{"all", &ops.Metrics{ContextPercent: ref(56.4), ContextTokens: ref(int64(112000)), ContextWindow: ref(int64(200000))}, "56% 112k/200k"},
		{"percent null", &ops.Metrics{ContextTokens: ref(int64(112000)), ContextWindow: ref(int64(200000))}, "— 112k/200k"},
		{"tokens null", &ops.Metrics{ContextPercent: ref(56.0), ContextWindow: ref(int64(200000))}, "56%"},
		{"window null", &ops.Metrics{ContextPercent: ref(56.0), ContextTokens: ref(int64(112000))}, "56%"},
		{"percent only", &ops.Metrics{ContextPercent: ref(56.0)}, "56%"},
		{"counts only, percent null", &ops.Metrics{ContextTokens: ref(int64(5)), ContextWindow: ref(int64(1000000))}, "— 5/1M"},
		{"tokens null, window set, percent null", &ops.Metrics{ContextWindow: ref(int64(200000))}, "—"},
		{"all null", &ops.Metrics{}, "—"},
		{"metrics null", nil, "—"},
	} {
		v := pv()
		v.Metrics = tc.m
		if _, vals := previewRows(jp(v)); vals["context"] != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, vals["context"], tc.want)
		}
	}
}

func TestJumpPreviewCost(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    *ops.Metrics
		want string
	}{
		{"both", &ops.Metrics{CostUSD: ref(4.12), BurnUSDPerHour: ref(1.8)}, "$4.12 ($1.80/h)"},
		{"cost only", &ops.Metrics{CostUSD: ref(4.12)}, "$4.12"},
		{"burn only", &ops.Metrics{BurnUSDPerHour: ref(1.8)}, "— ($1.80/h)"},
		{"zero cost", &ops.Metrics{CostUSD: ref(0.0), BurnUSDPerHour: ref(0.0)}, "$0.00 ($0.00/h)"},
		{"both null", &ops.Metrics{}, "—"},
		{"metrics null", nil, "—"},
	} {
		v := pv()
		v.Metrics = tc.m
		if _, vals := previewRows(jp(v)); vals["cost"] != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, vals["cost"], tc.want)
		}
	}
}

func TestJumpPreviewWhere(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   *int64
		sess string
		want string
	}{
		{"titled", ref(int64(3)), "docs", "#3 docs"},
		{"untitled", ref(int64(3)), "#3", "#3"},
		{"another number", ref(int64(3)), "#4", "#3 #4"},
		{"no ID", nil, "docs", "docs"},
		{"no ID, hash name", nil, "#3", "#3"},
		{"hostile", ref(int64(3)), "a\nb\t" + uuid(9), "#3 a b " + uuid(9)},
	} {
		v := pv()
		v.ID, v.Name = tc.id, tc.sess
		labels, vals := previewRows(jp(v))
		if vals["session"] != tc.want {
			t.Errorf("%s: session %q, want %q", tc.name, vals["session"], tc.want)
		}
		if len(strings.Split(jp(v), "\n")) != len(labels)+1 { // one row each (the extra's included), the final newline
			t.Errorf("%s: a value broke a row:\n%s", tc.name, jp(v))
		}
	}

	// Unknown values are the none mark; known ones are scrubbed.
	v := pv()
	_, vals := previewRows(jp(v))
	for _, k := range []string{"job", "cwd", "git_branch", "model", "permission_mode", "tab title"} {
		if vals[k] != none {
			t.Errorf("%s: %q, want %q", k, vals[k], none)
		}
	}
	v.Job, v.Cwd, v.GitBranch = ref("a\tb"), ref("/home/p"), ref("fe/\x1b[31mx")
	v.Model, v.PermissionMode = ref("m\u2028n"), ref("p\x7fq")
	_, vals = previewRows(jp(v))
	for k, w := range map[string]string{"job": "a b", "cwd": "~", "git_branch": "fe/ [31mx", "model": "m n", "permission_mode": "p q"} {
		if vals[k] != w {
			t.Errorf("%s: %q, want %q", k, vals[k], w)
		}
	}

	// The tab title: no placement, none stored, a newline in it.
	v = pv()
	v.Placement = obj(t, `{"terminal":"kitty","socket":"unix:/old","window_id":4}`)
	if _, vals := previewRows(jp(v)); vals["tab title"] != none {
		t.Errorf("tab title without a title: %q", vals["tab title"])
	}
	v.Placement = obj(t, `{"terminal":"kitty","socket":"unix:/old","window_id":4,"tab_title":"one\ntwo\u0085"}`)
	text := jp(v)
	if _, vals := previewRows(text); vals["tab title"] != "one two " {
		t.Errorf("tab title %q", vals["tab title"])
	}
	if strings.Count(text, "\n") != strings.Count(jp(pv()), "\n") {
		t.Errorf("a title with a newline made another row:\n%s", text)
	}
}

func TestJumpPreviewExtra(t *testing.T) {
	for _, extra := range []*string{nil, ref("{}")} {
		v := pv()
		if extra != nil {
			v.Extra = obj(t, *extra)
		}
		text := jp(v)
		if !strings.HasSuffix(text, "\n"+fmt.Sprintf("%-16s %s\n", "extra:", none)) {
			t.Errorf("extra %v:\n%s", extra, text)
		}
	}
	v := pv()
	v.Extra = obj(t, `{"k":"a\u009bb\u007f","n":{"x":[1,2]}}`)
	text := jp(v)
	for _, bad := range []string{"\u009b", "\u007f"} {
		if strings.Contains(text, bad) {
			t.Errorf("raw %U in:\n%s", []rune(bad)[0], text)
		}
	}
	if !strings.Contains(text, `"k": "a\u009bb\u007f"`) || !strings.Contains(text, "\nextra:\n{\n") {
		t.Errorf("extra block:\n%s", text)
	}
	// The block is restart's: the same text for the same extra.
	_, restartTail, _ := strings.Cut(preview(v, at, titleOf), "extra:\n")
	_, jumpTail, _ := strings.Cut(text, "extra:\n")
	if restartTail != jumpTail {
		t.Errorf("restart's block:\n%s\njump's:\n%s", restartTail, jumpTail)
	}
}

// ---- jump against the fake fzf ----

var columnGap = regexp.MustCompile(`  +`)

// The snapshot: each file's quiet and cache read as its line's Quiet and
// Cache columns, from the one now; one file per candidate, named by its key.
func TestJumpPreviewSnapshot(t *testing.T) {
	f := newJumpFixture(t)
	m, h := time.Minute, time.Hour
	f.warm(f.live(1, "blocked", 4*m, title("fix auth")), 56*m)
	f.cold(f.live(2, "stalled", 3*h), 80000)
	f.live(3, "your_turn", 90*time.Second)
	f.live(4, "working", 50*h)
	f.selects(1)
	f.jump(JumpInput{})

	lines := f.stdin()
	if len(lines) != 4 {
		t.Fatalf("lines %q", lines)
	}
	previews := filepath.Join(f.fzf, "previews")
	if ents, _ := os.ReadDir(previews); len(ents) != 4 {
		t.Errorf("%d files", len(ents))
	}
	for _, line := range lines {
		key, rest, _ := strings.Cut(line, "\t")
		cols := columnGap.Split(rest, -1)
		_, vals := previewRows(f.recorded("previews/" + key))
		if vals["quiet"] != cols[5] || vals["cache"] != cols[4] {
			t.Errorf("%s: file quiet %q, cache %q; line %q", key, vals["quiet"], vals["cache"], cols)
		}
		if vals["attention"] == "" || !strings.HasSuffix(vals["attention"], cols[3]) {
			t.Errorf("%s: attention %q, line %q", key, vals["attention"], cols[3])
		}
	}
}

// The directory: a private sesshin-jump- directory under XDG_RUNTIME_DIR, the
// preview command quoted, the first line's file printed.
func TestJumpPreviewDir(t *testing.T) {
	f := newJumpFixture(t)
	f.live(1, "blocked", time.Minute, title("fix auth"))
	f.live(2, "idle", time.Minute)
	f.selects(1)
	f.jump(JumpInput{})

	if m := strings.TrimSpace(f.recorded("mode")); m != "drwx------" {
		t.Errorf("directory mode %s", m)
	}
	argv := f.argv()
	i := slices.Index(argv, "--preview")
	if i < 0 || !strings.HasPrefix(argv[i+1], "cat -- '") || !strings.HasSuffix(argv[i+1], "'/{1}") ||
		!strings.Contains(argv[i+1], `dir'\''s/sesshin-jump-`) {
		t.Errorf("preview command %q", argv)
	}
	first, _, _ := strings.Cut(f.stdin()[0], "\t")
	if out, want := f.recorded("preview-out"), f.recorded("previews/"+first); out == "" || out != want || !strings.HasPrefix(out, "attention:") {
		t.Errorf("preview command printed:\n%s\nwant:\n%s", out, want)
	}
	if left, _ := filepath.Glob(filepath.Join(f.run, "sesshin-*")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

// The directory is gone as soon as fzf returns, before focus runs, and
// after every outcome, a failed focus included.
func TestJumpPreviewRemoved(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		keys     []string
		focusErr error
		focused  bool
	}{
		{"enter", 0, []string{uuid(1)}, nil, true},
		{"esc", 130, nil, nil, false},
		{"enter, nothing matching", 1, nil, nil, false},
		{"other status", 2, nil, nil, false},
		{"failed focus", 0, []string{uuid(1)}, errors.New("kitten @ focus-window: exit status 1"), true},
	} {
		f := newJumpFixture(t)
		f.live(1, "blocked", time.Minute)
		f.selects(tc.status, tc.keys...)
		f.focusErr = tc.focusErr
		f.jump(JumpInput{})
		if left, _ := filepath.Glob(filepath.Join(f.run, "sesshin-*")); len(left) != 0 {
			t.Errorf("%s: left behind: %v", tc.name, left)
		}
		if !f.ran() {
			t.Errorf("%s: fzf did not run", tc.name)
		}
		if tc.focused != (len(f.dirsAtFocus) > 0) {
			t.Errorf("%s: focus ran %d times", tc.name, len(f.dirsAtFocus))
		}
		for _, seen := range f.dirsAtFocus {
			if len(seen) != 0 {
				t.Errorf("%s: at focus, the directory was there: %v", tc.name, seen)
			}
		}
	}
}

// A failure writing a file is io, with no directory left and no fzf.
func TestJumpPreviewIOFailure(t *testing.T) {
	f := newJumpFixture(t)
	f.live(1, "blocked", time.Minute)
	f.live(2, "idle", time.Minute)
	f.selects(0, uuid(1))
	writes := 0
	f.hook = func(op fsys.Op) error {
		if op.Name == fsys.OpCreateTemp && strings.Contains(op.Root, "sesshin-jump-") {
			if writes++; writes == 2 { // the second file
				return syscall.ENOSPC
			}
		}
		return nil
	}
	env := Jump(JumpInput{}, f.env())
	if env.OK || env.Error.Kind != ops.KindIO {
		t.Fatalf("%+v", env)
	}
	checkEnvelopeAs(t, env, "jump-output")
	if f.ran() || f.catches != 0 {
		t.Error("fzf started")
	}
	if left, _ := filepath.Glob(filepath.Join(f.run, "sesshin-*")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	if len(f.focuses) != 0 {
		t.Errorf("focused %v", f.focuses)
	}
	if writes != 2 {
		t.Errorf("%d files written", writes)
	}
}

// The directory is never made with no candidates or no terminal.
func TestJumpPreviewNeverMade(t *testing.T) {
	made := func(f *jumpFixture) bool {
		got := false
		f.hook = func(op fsys.Op) error {
			if op.Name == fsys.OpMkdir && strings.Contains(op.Root, f.run) {
				got = true
			}
			return nil
		}
		f.jump(JumpInput{})
		return got
	}
	f := newJumpFixture(t)
	f.add(1, time.Hour) // ended: no candidate
	f.selects(0, uuid(1))
	if made(f) || f.ran() {
		t.Error("made for no candidates")
	}
	f = newJumpFixture(t)
	f.live(1, "blocked", time.Minute)
	f.noTTY = true
	f.selects(0, uuid(1))
	if made(f) || f.ran() {
		t.Error("made for no terminal")
	}
}
