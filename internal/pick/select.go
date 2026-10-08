package pick

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/phansen314/sesshin/internal/ops"
)

// runSelection runs fzf on stdin and returns the keys it printed: each
// line's first field, in the order printed; nil when nothing matched (nothing
// picked). A cancel is the cancelled error with the message cancelled; any
// other failure is unavailable (picker-spec.md, Outcomes and Errors).
func runSelection(sys System, fzf string, fzfArgs []string, stdin []byte, cancelled string) ([]string, *ops.Error) {
	// In the picker, ctrl-c is a key: fzf cancels. Any SIGINT or SIGQUIT
	// that reaches the picker meanwhile is discarded.
	restore := sys.CatchInterrupts()
	stdout, status, err := sys.RunFzf(fzf, fzfArgs, sys.Environ(), stdin)
	restore()
	switch {
	case err != nil:
		return nil, unavailableErr(fmt.Sprintf("fzf failed: %v", err), fzfFailed)
	case status == 1:
		return nil, nil // nothing matched: nothing picked
	case status == 130:
		return nil, &ops.Error{Kind: KindCancelled, Message: cancelled, Details: map[string]any{}}
	case status != 0:
		return nil, unavailableErr(fmt.Sprintf("fzf exited with status %d", status), fzfFailed, "status", status)
	}
	keys := []string{}
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		key, _, _ := strings.Cut(string(line), lineDelimiter)
		keys = append(keys, key)
	}
	return keys, nil
}
