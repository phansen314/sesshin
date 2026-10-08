package pick

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/statusline"
)

// marks are the attention's marks (picker-spec.md, Jump lines).
var marks = map[string]string{
	ops.AttentionBlocked:    "🔐",
	ops.AttentionStalled:    "⛔",
	ops.AttentionYourTurn:   "🙋",
	ops.AttentionIdle:       "💤",
	ops.AttentionSelfWaking: "⏳",
	ops.AttentionWorking:    "🏃",
	ops.AttentionUnknown:    "❓",
}

// jumpColumns is the number of columns before the name: mark, ID, job,
// attention, cache, quiet, cwd.
const jumpColumns = 7

// renderJumpLines renders views, in order, as fzf's lines (picker-spec.md,
// Jump lines), without newlines: the key, a tab, then the columns, each but
// the last padded to the widest in views, in display width with every emoji
// two columns. now gives Quiet and, in its location, the cache's time of day;
// home is the home directory, shown as ~.
func renderJumpLines(views []ops.SessionView, now time.Time, home string) []string {
	rows := make([][jumpColumns]string, len(views))
	var widths [jumpColumns]int
	for i, v := range views {
		id, job := none, none
		if v.ID != nil {
			id = fmt.Sprintf("#%d", *v.ID)
		}
		if v.Job != nil {
			job = *v.Job
		}
		att := attentionOf(v)
		mark, ok := marks[att]
		if !ok {
			mark = marks[ops.AttentionUnknown]
		}
		rows[i] = [jumpColumns]string{
			mark, id, scrub(job), scrub(att), cacheColumn(v, now),
			elapsed(now.Sub(v.LastEventAt.Time())), scrub(cwd(v, home)),
		}
		for c, s := range rows[i] {
			widths[c] = max(widths[c], cells(s))
		}
	}
	lines := make([]string, len(views))
	for i, v := range views {
		var cols []string
		for c, s := range rows[i] {
			cols = append(cols, s+strings.Repeat(" ", widths[c]-cells(s)))
		}
		cols = append(cols, scrub(v.Name))
		lines[i] = v.SessionID + lineDelimiter + strings.Join(cols, "  ")
	}
	return lines
}

// cacheColumn is the prompt cache as the statusline shows it, in now's
// location; the none mark when it is unknown or null.
func cacheColumn(v ops.SessionView, now time.Time) string {
	pc := v.PromptCache
	if pc == nil {
		return none
	}
	var state statusline.State
	switch pc.State {
	case "warm":
		state = statusline.Warm
	case "cold":
		state = statusline.Cold
	}
	var expires time.Time
	if e, ok := expiry(v); ok {
		expires = e
	}
	var tokens float64
	if pc.RecacheTokens != nil {
		tokens = float64(*pc.RecacheTokens)
	}
	if s := statusline.CacheText(state, expires, tokens, pc.RecacheTokens != nil, now.Location()); s != "" {
		return s
	}
	return none
}

// cells is the display width of s in terminal cells, counting every emoji
// as two: go-runewidth counts a text-style character made an emoji by U+FE0F
// (the ♨️ of a warm cache) as one.
func cells(s string) int {
	w := runewidth.StringWidth(s)
	var prev rune
	for _, r := range s {
		if r == '️' && prev != 0 && runewidth.RuneWidth(prev) == 1 {
			w++
		}
		prev = r
	}
	return w
}
