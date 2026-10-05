package ops

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/model"
)

// dryRunInput is a stand-in operation input: one optional boolean, as
// install's and uninstall's dry_run.
type dryRunInput struct{ DryRun bool }

func decodeDryRun(f *model.Fields, p *model.Problems) dryRunInput {
	var in dryRunInput
	if v, ok := f.Optional("dry_run"); ok {
		in.DryRun, _ = p.Bool(v, f.Ptr("dry_run"))
	}
	return in
}

// --input is read as strictly as sesshin's own files: a repeated key and a key
// of another case are invalid-input, never the last value or a match.
func TestDecodeInput(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []model.Problem // nil: accepted
	}{
		{`{}`, nil},
		{` {"dry_run": true} `, nil},
		{`{"DRY_RUN": true}`, []model.Problem{{Field: "/DRY_RUN", Reason: "unknown field"}}},
		{`{"dry_run": false, "dry_run": true}`, []model.Problem{{Field: "/dry_run", Reason: "repeated key"}}},
		{`{"dry_run": 1}`, []model.Problem{{Field: "/dry_run", Reason: "expected a boolean"}}},
		{`{"b": 1, "a": 2}`, []model.Problem{{Field: "/a", Reason: "unknown field"}, {Field: "/b", Reason: "unknown field"}}},
		{``, []model.Problem{{Field: "", Reason: "empty"}}},
		{`[]`, []model.Problem{{Field: "", Reason: "expected a JSON object"}}},
		{`{} {}`, []model.Problem{{Field: "", Reason: "unexpected data after the JSON value"}}},
		{"\xef\xbb\xbf{}", []model.Problem{{Field: "", Reason: "starts with a byte-order mark"}}},
		{"{\"x\": \"\xff\"}", []model.Problem{{Field: "", Reason: "not valid UTF-8"}}},
	} {
		in, e := DecodeInput([]byte(tc.in), decodeDryRun)
		if tc.want == nil {
			if e != nil {
				t.Errorf("%q: %+v", tc.in, e)
			} else if want := strings.Contains(tc.in, "true"); in.DryRun != want {
				t.Errorf("%q: dry_run %v", tc.in, in.DryRun)
			}
			continue
		}
		if e == nil {
			t.Errorf("%q: accepted", tc.in)
			continue
		}
		checkEnvelope(t, Failed(e), "")
		if got, _ := e.Details["problems"].([]model.Problem); e.Kind != KindInvalidInput || !slices.Equal(got, tc.want) {
			t.Errorf("%q: %+v, want invalid-input %v", tc.in, e, tc.want)
		}
	}
}

// At most 20 problems, sorted, with problems_truncated; the message counts
// them all.
func TestInvalidInputTruncated(t *testing.T) {
	var members []string
	for i := range 25 {
		members = append(members, fmt.Sprintf(`"k%02d": 1`, 24-i))
	}
	_, e := DecodeInput([]byte("{"+strings.Join(members, ",")+"}"), decodeDryRun)
	checkEnvelope(t, Failed(e), "")
	b, err := json.Marshal(e.Details)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Problems  []model.Problem `json:"problems"`
		Truncated bool            `json:"problems_truncated"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Problems) != 20 || !d.Truncated || d.Problems[0].Field != "/k00" || d.Problems[19].Field != "/k19" || e.Message != "25 invalid inputs" {
		t.Errorf("%s: %s", e.Message, b)
	}
}
