package pick

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"

	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/ops"
)

// none is a column with nothing to show.
const none = "—"

// transcriptGone follows the End word when the transcript is not there.
const transcriptGone = "transcript-gone"

// renderLines renders views, in order, as fzf's lines (picker-spec.md,
// Lines), without newlines: the key, a tab, then the columns, each but the
// last padded to the widest in views, in terminal cells. home is the home
// directory, shown as ~.
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
	for i, v := range views {
		var cols []string
		for c, s := range rows[i] {
			cols = append(cols, runewidth.FillRight(s, widths[c]))
		}
		cols = append(cols, scrub(v.Name))
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
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", max(d, 0)/time.Second)
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", d/time.Minute)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", d/time.Hour)
	}
	return fmt.Sprintf("%dd ago", d/(24*time.Hour))
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
