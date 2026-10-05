package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The performance gate's numbers (implementation-spec.md, Performance gate).
const (
	perfN      = 500
	perfWarmup = 50
	// startBudget bounds sesshin-hook's start-up (empty stdin: it exits after
	// reading it) over do-nothing. A stray linked library shows here: cobra
	// cost about 0.19ms, a JSON Schema library 2.6ms (implementation-spec.md,
	// Toolchain).
	startBudget = 300 * time.Microsecond
	// eventBudget bounds a whole post-tool-use over do-nothing: start-up and
	// the event's work.
	eventBudget = 750 * time.Microsecond
)

// TestHookCostGate is two gates on medians over a do-nothing Go binary:
// sesshin-hook's start-up within startBudget, and a whole post-tool-use within
// eventBudget. The gate's second bullet, 8 writers × 100 events leaving
// event_seq exact, is TestConcurrentWriters.
func TestHookCostGate(t *testing.T) {
	if testing.Short() || os.Getenv("SESSHIN_PERF") != "1" {
		t.Skip("times 1,500 process starts; set SESSHIN_PERF=1 to run")
	}
	h := New(t)
	nothing := filepath.Join(t.TempDir(), "donothing")
	cmd := exec.Command("go", "build", "-o", nothing, "./testdata/donothing")
	cmd.Dir = ".." // the module root: tests run in e2e/
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build donothing: %v\n%s", err, out)
	}

	// A real session first, so every timed run locks, reads, writes, and
	// renames; none adopts.
	payload := perfPayload()
	quiet(t, h.Hook("post-tool-use", payload))
	before := readLifecycle(t, h).EventSeq

	// A gate that fails on a loaded machine is noise, so one retry: a real
	// regression fails both attempts.
	var start, event time.Duration
	for attempt := 1; attempt <= 2; attempt++ {
		hook, empty, base := perfMeasure(t, h, nothing, payload)
		start, event = median(empty)-median(base), median(hook)-median(base)
		t.Logf("attempt %d, n=%d: do-nothing median %v p99 %v; start-up median %v p99 %v, +%v (budget %v); post-tool-use median %v p99 %v, +%v (budget %v)",
			attempt, perfN, median(base), p99(base), median(empty), p99(empty), start, startBudget,
			median(hook), p99(hook), event, eventBudget)
		if start <= startBudget && event <= eventBudget {
			break
		}
	}

	if got := readLifecycle(t, h).EventSeq; got < before+perfN {
		t.Errorf("event_seq %d after %d timed runs from %d: the hook did not write", got, perfN, before)
	}
	if start > startBudget {
		t.Errorf("start-up median exceeds do-nothing by %v, over %v", start, startBudget)
	}
	if event > eventBudget {
		t.Errorf("post-tool-use median exceeds do-nothing by %v, over %v", event, eventBudget)
	}
}

// perfPayload is a PostToolUse payload of about 1KB.
func perfPayload() string {
	out := strings.Repeat("x", 600)
	return event("PostToolUse",
		`"prompt_id":"p1"`,
		`"transcript_path":"/home/user/.claude/projects/-home-user-code-sesshin/`+sid+`.jsonl"`,
		`"cwd":"/home/user/code/sesshin"`,
		`"permission_mode":"default"`,
		`"tool_name":"Bash"`,
		`"tool_input":{"command":"go test ./...","description":"Run the tests"}`,
		`"tool_response":{"stdout":"`+out+`","stderr":"","interrupted":false}`,
		`"tool_use_id":"toolu_01ABCDEFGHJKLMNPQRSTUVWX"`)
}

// perfMeasure starts sesshin-hook post-tool-use with payload, sesshin-hook
// post-tool-use with empty stdin (start-up alone), and the do-nothing binary
// with payload, in turn, each directly: the fake claude and sh would add
// noise the gate doesn't measure. The warm-up runs aren't timed.
func perfMeasure(t *testing.T, h *Harness, nothing, payload string) (hook, empty, base []time.Duration) {
	t.Helper()
	run := func(stdin, path string, args ...string) time.Duration {
		cmd := exec.Command(path, args...)
		cmd.Env = h.Environ()
		cmd.Stdin = strings.NewReader(stdin)
		start := time.Now()
		err := cmd.Run()
		d := time.Since(start)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		return d
	}
	for range perfWarmup {
		run(payload, h.HookPath, "post-tool-use")
		run("", h.HookPath, "post-tool-use")
		run(payload, nothing)
	}
	for range perfN {
		hook = append(hook, run(payload, h.HookPath, "post-tool-use"))
		empty = append(empty, run("", h.HookPath, "post-tool-use"))
		base = append(base, run(payload, nothing))
	}
	return hook, empty, base
}

func median(ds []time.Duration) time.Duration { return percentile(ds, 50) }
func p99(ds []time.Duration) time.Duration    { return percentile(ds, 99) }

// percentile is the nearest-rank percentile of ds.
func percentile(ds []time.Duration, p int) time.Duration {
	s := slices.Sorted(slices.Values(ds))
	return s[(len(s)*p+99)/100-1]
}
