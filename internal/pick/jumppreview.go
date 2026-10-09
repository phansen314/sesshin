package pick

import (
	"fmt"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/statusline"
)

// jumpPreview is the text of a candidate's preview in jump (picker-spec.md,
// Jump preview): what it wants, what going there costs, where it is, and its
// extra, as restart's rows are written. Every value is scrubbed: a title
// can't pass for another row.
func jumpPreview(v ops.SessionView, now time.Time, home string, tabTitle func(*jsonio.Object) string) string {
	att := attentionOf(v)
	mark, ok := marks[att]
	if !ok {
		mark = marks[ops.AttentionUnknown]
	}
	rows := [][2]string{
		{"attention", mark + " " + att},
		{"status", v.Status},
	}
	if v.Liveness == "unknown" {
		rows = append(rows, [2]string{"liveness", v.Liveness})
	}
	rows = append(rows, [2]string{"quiet", elapsed(now.Sub(v.LastEventAt.Time()))})
	if v.StallReason != nil {
		rows = append(rows, [2]string{"stall_reason", *v.StallReason})
	}
	if p := v.Pending; p != nil && (p.BackgroundTasks > 0 || p.SessionCrons > 0) {
		rows = append(rows, [2]string{"pending", fmt.Sprintf("%d background, %d cron", p.BackgroundTasks, p.SessionCrons)})
	}

	rows = append(rows,
		[2]string{"cache", cacheColumn(v, now)},
		[2]string{"hit ratio", hitRatio(v.PromptCache)},
		[2]string{"context", contextRow(v.Metrics)},
		[2]string{"cost", costRow(v.Metrics)},
		[2]string{"session", sessionRow(v)},
		[2]string{"job", orNone(v.Job)},
		[2]string{"cwd", cwd(v, home)},
		[2]string{"git_branch", orNone(v.GitBranch)},
		[2]string{"model", orNone(v.Model)},
		[2]string{"permission_mode", orNone(v.PermissionMode)},
	)
	tab := none
	if title := tabTitle(v.Placement); title != "" {
		tab = title
	}
	rows = append(rows, [2]string{"tab title", tab})
	for i := range rows {
		rows[i][1] = scrub(rows[i][1])
	}
	return previewText(rows, v.Extra)
}

// orNone is *s, or the none mark when it is nil.
func orNone(s *string) string {
	if s == nil {
		return none
	}
	return *s
}

// standAlone joins the parts of a row with a space (picker-spec.md, Jump
// preview): each part stands alone, so a part that is "" (null) is left out,
// or the none mark in its place when a later part is shown, and the row is
// the none mark when every part is "".
func standAlone(parts ...string) string {
	last := -1
	for i, p := range parts {
		if p != "" {
			last = i
		}
	}
	if last < 0 {
		return none
	}
	shown := make([]string, last+1)
	for i := range shown {
		shown[i] = parts[i]
		if shown[i] == "" {
			shown[i] = none
		}
	}
	return strings.Join(shown, " ")
}

// hitRatio is the hit ratio, then in parentheses the misses and the last
// miss's causes: 92% (3 misses; last: ttl, edit).
func hitRatio(pc *ops.PromptCache) string {
	if pc == nil {
		return none
	}
	var pct, detail string
	if pc.HitRatio != nil {
		pct = statusline.Percent(*pc.HitRatio * 100)
	}
	var in []string
	if pc.Misses != nil {
		in = append(in, fmt.Sprintf("%d misses", *pc.Misses))
		if *pc.Misses == 1 {
			in[0] = "1 miss"
		}
	}
	if pc.LastMissCause != nil && len(*pc.LastMissCause) > 0 {
		in = append(in, "last: "+strings.Join(*pc.LastMissCause, ", "))
	}
	if len(in) > 0 {
		detail = "(" + strings.Join(in, "; ") + ")"
	}
	return standAlone(pct, detail)
}

// contextRow is the statusline's context text: 56% 112k/200k.
func contextRow(m *ops.Metrics) string {
	if m == nil {
		return none
	}
	num := func(v *float64) payload.Num {
		if v == nil {
			return payload.Num{}
		}
		return payload.Num{V: *v, OK: true}
	}
	count := func(v *int64) payload.Num {
		if v == nil {
			return payload.Num{}
		}
		return payload.Num{V: float64(*v), OK: true}
	}
	pct, counts := statusline.ContextText(num(m.ContextPercent), count(m.ContextTokens), count(m.ContextWindow))
	return standAlone(pct, counts)
}

// costRow is the cost, then the burn rate in parentheses: $4.12 ($1.80/h).
func costRow(m *ops.Metrics) string {
	if m == nil {
		return none
	}
	var cost, burn string
	if m.CostUSD != nil {
		cost = fmt.Sprintf("$%.2f", *m.CostUSD)
	}
	if m.BurnUSDPerHour != nil {
		burn = fmt.Sprintf("($%.2f/h)", *m.BurnUSDPerHour)
	}
	return standAlone(cost, burn)
}

// sessionRow is #<id> <name>; the name alone when it is #<id> (an untitled
// session's) or the session has no ID.
func sessionRow(v ops.SessionView) string {
	if v.ID == nil {
		return v.Name
	}
	id := fmt.Sprintf("#%d", *v.ID)
	if v.Name == id {
		return v.Name
	}
	return id + " " + v.Name
}
