package pick

import (
	"bytes"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/mattn/go-runewidth"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/ops"
)

// none is a column with nothing to show.
const none = "—"

// transcriptGone follows the End word when the transcript is not there.
const transcriptGone = "transcript-gone"

// nameCap is the most columns Name is padded to, and extraCap the most the
// Extra column shows (picker-spec.md, Lines and Extra column).
const (
	nameCap  = 32
	extraCap = 200
)

// renderLines renders views, in order, as fzf's lines (picker-spec.md,
// Lines), without newlines: the key, a tab, then the columns, each but the
// last two padded to the widest in views, in terminal cells; Name is padded
// to the widest name, to at most nameCap, and Extra, last, is not padded and
// is left out, with its separator, when empty. home is the home directory,
// shown as ~.
func renderLines(views []ops.SessionView, now time.Time, home string) []string {
	rows := make([][5]string, len(views))
	var widths [5]int
	for i, v := range views {
		id, job := none, none
		if v.ID != nil {
			id = fmt.Sprintf("#%d", *v.ID)
		}
		if v.Job != nil {
			job = *v.Job
		}
		end := endWord(v)
		if v.TranscriptExists != nil && !*v.TranscriptExists {
			end += " " + transcriptGone
		}
		rows[i] = [5]string{id, scrub(job), end, seen(v, now), scrub(cwd(v, home))}
		for c, s := range rows[i] {
			widths[c] = max(widths[c], runewidth.StringWidth(s))
		}
	}
	lines := make([]string, len(views))
	nameWidth := 0
	for _, v := range views {
		nameWidth = max(nameWidth, runewidth.StringWidth(scrub(v.Name)))
	}
	nameWidth = min(nameWidth, nameCap)
	for i, v := range views {
		var cols []string
		for c, s := range rows[i] {
			cols = append(cols, runewidth.FillRight(s, widths[c]))
		}
		if extra := renderExtra(v.Extra); extra != "" {
			cols = append(cols, runewidth.FillRight(scrub(v.Name), nameWidth), extra)
		} else {
			cols = append(cols, scrub(v.Name))
		}
		lines[i] = v.SessionID + lineDelimiter + strings.Join(cols, "  ")
	}
	return lines
}

// endWord is how the session ended, as a word fzf can match (picker-spec.md,
// Lines): killed when no SessionEnd was recorded or its reason is other, and
// otherwise the reason's own word.
func endWord(v ops.SessionView) string {
	reason := ""
	if v.EndReason != nil {
		reason = *v.EndReason
	}
	switch {
	case v.EndedAt == nil && reason != live.Superseded, reason == "other":
		return "killed"
	case reason == "prompt_input_exit":
		return "exited"
	case reason == "clear":
		return "cleared"
	case reason == "resume":
		return "resumed"
	case reason == "":
		return "ended" // ended with no reason stored: no word of sesshin's own
	}
	return scrub(reason) // superseded, logout, and any other, as stored
}

// seen is the session's last seen, relative to now.
func seen(v ops.SessionView, now time.Time) string {
	return ago(now.Sub(v.LastSeen.Time()))
}

// ago is d as <n>s, <n>m, <n>h, or <n>d ago; a negative d (a clock set
// back) is 0s.
func ago(d time.Duration) string { return elapsed(d) + " ago" }

// elapsed is d as <n>s, <n>m, <n>h, or <n>d; a negative d is 0s.
func elapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(d, 0)/time.Second)
	case d < time.Hour:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dd", d/(24*time.Hour))
}

// cwd is the session's cwd with the home directory as ~, or the none mark.
func cwd(v ops.SessionView, home string) string {
	if v.Cwd == nil {
		return none
	}
	return tilde(*v.Cwd, home)
}

func tilde(path, home string) string {
	home = strings.TrimSuffix(home, "/")
	switch {
	case home == "":
		return path
	case path == home:
		return "~"
	case strings.HasPrefix(path, home+"/"):
		return "~" + path[len(home):]
	}
	return path
}

// scrub replaces every control character, and the line separators that some
// terminals and tools treat as one, with a space, so that no field can end
// the line or start another, or forge the key.
func scrub(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
}

// renderExtra is the Extra column for o (picker-spec.md, Extra column): one
// key=value pair per top-level key, in stored order, joined by single spaces,
// scrubbed, and cut to extraCap columns: scrubbed first, since a control
// character JSON leaves raw (DEL, C1) is no columns wide until it is a space.
// nil and {} give "".
func renderExtra(o *jsonio.Object) string {
	if o == nil || o.Len() == 0 {
		return ""
	}
	pairs := make([]string, len(o.Members))
	for i, m := range o.Members {
		key := m.Key
		if !bareKey(key) {
			key = quoteJSON(key)
		}
		var val string
		if s, ok := m.Value.(string); ok {
			val = s
			if !bareValue(s) {
				val = quoteJSON(s)
			}
		} else {
			val = quoteJSON(m.Value)
		}
		pairs[i] = key + "=" + val
	}
	return capCells(scrub(strings.Join(pairs, " ")), extraCap)
}

// quoteJSON is v as compact JSON, escaped as the File format escapes strings
// (jsonio writes no HTML escapes); numbers keep their text.
func quoteJSON(v any) string {
	b, err := jsonio.MarshalLine(v)
	if err != nil {
		return fmt.Sprintf("%q", fmt.Sprint(v)) // unreachable for a parsed tree
	}
	return string(bytes.TrimSuffix(b, []byte("\n")))
}

// bareKey is whether key shows as stored: non-empty, only ASCII letters,
// digits, _, ., and -.
func bareKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

// bareValue is whether a string value shows as stored: non-empty, with no =,
// no ", no control character, and no space of Unicode's.
func bareValue(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == '=' || r == '"' || unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// capCells is s if it is at most limit columns wide (as cells counts), else
// the longest prefix of whole grapheme clusters that, with a "…", is.
func capCells(s string, limit int) string {
	if cells(s) <= limit {
		return s
	}
	const ellipsis = "…"
	budget := limit - cells(ellipsis)
	var b strings.Builder
	used := 0
	it := graphemes.FromString(s)
	for it.Next() {
		g := it.Value()
		w := cells(g)
		if used+w > budget {
			break
		}
		used += w
		b.WriteString(g)
	}
	return b.String() + ellipsis
}
