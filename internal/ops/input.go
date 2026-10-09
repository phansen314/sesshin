package ops

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
)

// MaxProblems is how many problems an invalid-input error lists at most
// (operations.md, Error kinds).
const MaxProblems = 20

// InvalidInput reports every problem found, sorted by field, then by reason,
// both compared byte by byte, so the same input always yields the same list;
// then cut to the first MaxProblems, with problems_truncated true, though the
// message counts them all. No problems is a bug, reported as internal.
func InvalidInput(problems []model.Problem) *Error {
	if len(problems) == 0 {
		return &Error{Kind: KindInternal, Message: "invalid-input with no problems"}
	}
	ps := slices.Clone(problems)
	slices.SortFunc(ps, func(a, b model.Problem) int {
		return cmp.Or(strings.Compare(a.Field, b.Field), strings.Compare(a.Reason, b.Reason))
	})
	msg := fmt.Sprintf("%d invalid inputs", len(ps))
	if len(ps) == 1 {
		msg = fmt.Sprintf("invalid input at %q: %s", ps[0].Field, ps[0].Reason)
	}
	truncated := len(ps) > MaxProblems
	if truncated {
		ps = ps[:MaxProblems]
	}
	details := map[string]any{"problems": ps, "problems_truncated": truncated}
	return &Error{Kind: KindInvalidInput, Message: msg, Details: details}
}

// DecodeInput reads an operation's --input strictly, as sesshin reads its own
// files (cli-spec.md, Input; implementation-spec.md, JSON reading): exactly
// one JSON object in UTF-8, else a problem at ""; no repeated key; then the
// operation's own checks, decode, which asks for each member it allows and
// checks it. Every member it doesn't ask for is unknown, matched by exact
// key, so DRY_RUN is never dry_run. The input is meaningful only when the
// error is nil.
func DecodeInput[T any](data []byte, decode func(f *model.Fields, p *model.Problems) T) (T, *Error) {
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		var zero T
		return zero, InvalidInput([]model.Problem{{Field: "", Reason: err.Error()}})
	}
	var p model.Problems
	for _, ptr := range repeated {
		p.AddAdditional(ptr, "repeated key") // beyond the input schema, which can't see repeats
	}
	return CheckInput(obj, &p, decode)
}

// CheckInput runs decode over input, as DecodeInput does once it has read
// the input: for input built from the command line's flags and arguments,
// which has no repeated keys. p holds any problems found before.
func CheckInput[T any](input *jsonio.Object, p *model.Problems, decode func(f *model.Fields, p *model.Problems) T) (T, *Error) {
	f, _ := p.Object(input, "")
	in := decode(f, p)
	f.Done()
	if !p.OK() {
		var zero T
		return zero, InvalidInput(p.List())
	}
	return in, nil
}
