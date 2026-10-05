package model

import (
	"encoding/json"
	"math"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// StatuslineName is statusline.json's name in a session directory.
const StatuslineName = "statusline.json"

// StatuslineFile is a session's statusline.json (design-spec.md,
// statusline.json), its fields in the schema's order. A pointer field is
// nullable: nil is null.
type StatuslineFile struct {
	Schema         StatuslineVersion `json:"schema"`
	ReceivedAt     Timestamp         `json:"received_at"`
	ReceivedNS     int64             `json:"received_ns"`
	Payload        Payload           `json:"payload"`
	GitBranch      *string           `json:"git_branch"`
	CostSample     *CostSample       `json:"cost_sample"`
	BurnUSDPerHour *float64          `json:"burn_usd_per_hour"`
	PID            *int64            `json:"pid"`
	PIDStartedAt   *string           `json:"pid_started_at"`
}

// CostSample is the burn rate's base.
type CostSample struct {
	At  Timestamp `json:"at"`
	USD float64   `json:"usd"`
}

// Payload is the stored statusline payload. A tick stores Raw, as
// jsonio.Payload returns it, written back with its key order, number text,
// and escapes as received. A read gives Tree, the stored payload as parsed:
// written back, it keeps key order and number text, but its strings are
// re-escaped (`\u0041` becomes `A`). No writer writes back a payload it read.
type Payload struct {
	Raw  json.RawMessage
	Tree *jsonio.Object
}

// MarshalJSON writes Raw if set, else Tree, else {}.
func (p Payload) MarshalJSON() ([]byte, error) {
	switch {
	case p.Raw != nil:
		return p.Raw, nil
	case p.Tree != nil:
		return p.Tree.MarshalJSON()
	}
	return []byte("{}"), nil
}

// ReadStatusline reads a session's statusline.json. Its payload is checked
// only for being an object. Beyond the schema, pid_started_at must be null
// exactly when pid is, and the two numbers must be within a float64's range.
// Its content is meaningful only when the result is usable.
func ReadStatusline(data []byte) (StatuslineFile, FileResult) {
	return readFile(data, StatuslineSchema, func(s *StatuslineFile, f *Fields, p *Problems) {
		s.ReceivedAt = timestamp(f, p, "received_at")
		if v, ok := f.Required("received_ns"); ok {
			s.ReceivedNS, _ = p.Int(v, f.Ptr("received_ns"), 0, math.MaxInt64)
		}
		if v, ok := f.Required("payload"); ok {
			if _, ok := p.Object(v, f.Ptr("payload")); ok {
				s.Payload.Tree = v.(*jsonio.Object)
			}
		}
		s.GitBranch = nullableText(f, p, "git_branch")
		if v, ok := f.Required("cost_sample"); ok {
			s.CostSample, _ = nullable(p, v, f.Ptr("cost_sample"), costSample)
		}
		if v, ok := f.Required("burn_usd_per_hour"); ok {
			s.BurnUSDPerHour, _ = nullable(p, v, f.Ptr("burn_usd_per_hour"), (*Problems).NonNegative)
		}
		pid, pidOK := nullablePositive(f, p, "pid")
		started, startedOK := nullableStartedAt(f, p, "pid_started_at")
		s.PID, s.PIDStartedAt = pid, started
		pidPair(p, f, pid, pidOK, started, startedOK)
	})
}

// costSample checks v, at ptr, as a non-null cost_sample.
func costSample(p *Problems, v any, ptr string) (CostSample, bool) {
	var c CostSample
	f, ok := p.Object(v, ptr)
	if !ok {
		return c, false
	}
	before := len(p.List())
	c.At = timestamp(f, p, "at")
	if v, ok := f.Required("usd"); ok {
		c.USD, _ = p.NonNegative(v, f.Ptr("usd"))
	}
	f.Done()
	return c, len(p.List()) == before
}
