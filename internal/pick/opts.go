package pick

import (
	"fmt"
	"strings"

	"github.com/junegunn/go-shellwords"

	"github.com/phansen314/sesshin/internal/ops"
)

// OptsVar holds the person's options for restart's fzf, which come after
// the picker's own and so win (picker-spec.md, fzf options).
const OptsVar = "SESSHIN_PICK_OPTS"

// undone are the options passed after FZF_DEFAULT_OPTS to undo any there
// that would accept or abort without the person, change what fzf prints
// (the selection) or reads, or move it into a popup.
var undone = []string{
	"--no-select-1", "--no-exit-0", "--no-expect", "--no-tmux",
	"--no-read0", "--no-header-lines", "--no-print0", "--no-print-query", "--accept-nth", "..",
}

// A line is tab-delimited (picker-spec.md, Lines): the hidden key, then the
// columns fzf shows and searches.
const (
	lineDelimiter = "\t"
	lineWithNth   = "2.."
)

// userOpts splits SESSHIN_PICK_OPTS as fzf splits FZF_DEFAULT_OPTS, with its
// own parser, comments included. One that doesn't split is reported as fzf
// reports a bad FZF_DEFAULT_OPTS: fzf-failed, here without a status, since
// fzf never ran.
func userOpts(environ []string) ([]string, *ops.Error) {
	p := shellwords.NewParser()
	p.ParseComment = true
	opts, err := p.Parse(lookupEnv(environ, OptsVar))
	if err != nil {
		return nil, unavailableErr(fmt.Sprintf("$%s: %v", OptsVar, err), fzfFailed)
	}
	return opts, nil
}

// lookupEnv is key's value in environ (KEY=value entries), the last if it is
// repeated, as getenv has it; "" if unset.
func lookupEnv(environ []string, key string) string {
	v := ""
	for _, kv := range environ {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// args are fzf's arguments, in order: the options undone, the picker's own
// (picker-spec.md, fzf options), then SESSHIN_PICK_OPTS. FZF_DEFAULT_OPTS,
// which fzf reads itself, comes before them all. dir is the directory of the
// preview files.
func args(dir, query string, userOpts []string) []string {
	a := append([]string{}, undone...)
	a = append(a,
		"--with-shell", "sh -c",
		"--multi",
		"--delimiter", lineDelimiter,
		"--with-nth", lineWithNth,
		// Equal matches stay in session order, most recently seen first.
		"--tiebreak", "index",
		"--preview", "cat -- "+shQuote(dir)+"/{1}",
		"--bind", "ctrl-a:select-all",
		"--query", query,
	)
	return append(a, userOpts...)
}

// jumpArgs are fzf's arguments for jump, in order: the options undone, jump's
// own (picker-spec.md, fzf options), then SESSHIN_PICK_OPTS. Single-select,
// in the order the lines are given, with no preview.
func jumpArgs(query string, userOpts []string) []string {
	a := append([]string{}, undone...)
	a = append(a,
		"--no-multi",
		"--no-sort",
		"--delimiter", lineDelimiter,
		"--with-nth", lineWithNth,
		"--query", query,
	)
	return append(a, userOpts...)
}

// shQuote quotes s as one sh word.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
