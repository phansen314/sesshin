package statusline

import (
	"math"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/payload"
)

var tickStart = time.Date(2026, 10, 3, 18, 31, 51, 400_000_000, time.UTC)

func cost(v float64) payload.Num { return payload.Num{V: v, OK: true} }

func f64(v float64) *float64 { return &v }

// previous is a usable previous tick whose sample is age old and has usd,
// carrying burn.
func previous(age time.Duration, usd float64, burn *float64) *model.StatuslineFile {
	return &model.StatuslineFile{
		CostSample:     &model.CostSample{At: model.FormatTimestamp(tickStart.Add(-age)), USD: usd},
		BurnUSDPerHour: burn,
	}
}

// The burn rate and the sample's carrying and replacing, at every boundary
// (design-spec.md, statusline.json).
func TestBurn(t *testing.T) {
	s := time.Second
	tests := []struct {
		name     string
		prev     *model.StatuslineFile
		cost     payload.Num
		sampleAt time.Duration // the stored sample's age; -1 means this tick's
		sampleUS float64
		noSample bool
		rate     *float64
	}{
		// First tick, and ticks with nothing to compare.
		{name: "first tick with a cost", cost: cost(2), sampleAt: -1, sampleUS: 2},
		{name: "first tick with no cost", noSample: true},
		{name: "previous tick had no sample", prev: &model.StatuslineFile{}, cost: cost(2), sampleAt: -1, sampleUS: 2},
		{name: "previous tick had no sample or cost", prev: &model.StatuslineFile{}, noSample: true},
		{name: "negative cost is no cost", cost: cost(-1), noSample: true},
		// The 60-second floor: carried forward before it.
		{name: "59 s: previous rate carried", prev: previous(59*s, 1, f64(7)), cost: cost(2), sampleAt: 59 * s, sampleUS: 1, rate: f64(7)},
		{name: "59 s: previous null carried", prev: previous(59*s, 1, nil), cost: cost(2), sampleAt: 59 * s, sampleUS: 1},
		{name: "59 s: no cost, rate carried", prev: previous(59*s, 1, f64(7)), sampleAt: 59 * s, sampleUS: 1, rate: f64(7)},
		{name: "60 s: computed", prev: previous(60*s, 1, f64(7)), cost: cost(2), sampleAt: 60 * s, sampleUS: 1, rate: f64(60)},
		{name: "120 s", prev: previous(120*s, 1, nil), cost: cost(2), sampleAt: 120 * s, sampleUS: 1, rate: f64(30)},
		// Replaced after 300 seconds.
		{name: "300 s: kept", prev: previous(300*s, 1, nil), cost: cost(2), sampleAt: 300 * s, sampleUS: 1, rate: f64(12)},
		{name: "301 s: replaced", prev: previous(301*s, 1, nil), cost: cost(4), sampleAt: -1, sampleUS: 4, rate: f64(3 * 3600 / 301.0)},
		// Null past 600 seconds.
		{name: "600 s: computed", prev: previous(600*s, 1, nil), cost: cost(2), sampleAt: -1, sampleUS: 2, rate: f64(6)},
		{name: "601 s: null", prev: previous(601*s, 1, f64(7)), cost: cost(2), sampleAt: -1, sampleUS: 2},
		// Both differences must be positive.
		{name: "equal cost: null, sample kept", prev: previous(120*s, 1, f64(7)), cost: cost(1), sampleAt: 120 * s, sampleUS: 1},
		{name: "equal cost past 300 s: sample replaced", prev: previous(301*s, 1, nil), cost: cost(1), sampleAt: -1, sampleUS: 1},
		{name: "falling cost: null, sample replaced", prev: previous(120*s, 5, f64(7)), cost: cost(1), sampleAt: -1, sampleUS: 1},
		{name: "falling cost under the floor: carried", prev: previous(30*s, 5, f64(7)), cost: cost(1), sampleAt: 30 * s, sampleUS: 5, rate: f64(7)},
		// No cost past the floor: the base is kept, the rate is null.
		{name: "no cost at 120 s", prev: previous(120*s, 1, f64(7)), sampleAt: 120 * s, sampleUS: 1},
		{name: "no cost at 700 s keeps the base", prev: previous(700*s, 1, nil), sampleAt: 700 * s, sampleUS: 1},
		// A sample dated after the tick.
		{name: "sample from the future: replaced", prev: previous(-30*s, 1, f64(7)), cost: cost(3), sampleAt: -1, sampleUS: 3},
		// A zero cost is a cost.
		{name: "zero cost starts a sample", cost: cost(0), sampleAt: -1, sampleUS: 0},
		// An overflowing rate is null: it could not be written.
		{name: "overflow", prev: previous(60*s, 0, nil), cost: cost(math.MaxFloat64), sampleAt: 60 * s, sampleUS: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := computeBurn(Tick{Now: tickStart, Payload: payload.Payload{Cost: payload.Cost{TotalCostUSD: tc.cost}}}, tc.prev)
			switch {
			case tc.noSample:
				if r.sample != nil {
					t.Errorf("sample %+v, want none", *r.sample)
				}
			case r.sample == nil:
				t.Errorf("no sample, want one")
			default:
				wantAt := model.FormatTimestamp(tickStart.Add(-max(tc.sampleAt, 0)))
				if tc.sampleAt < 0 {
					wantAt = model.FormatTimestamp(tickStart)
				}
				if r.sample.At != wantAt || r.sample.USD != tc.sampleUS {
					t.Errorf("sample %+v, want {%s %v}", *r.sample, wantAt, tc.sampleUS)
				}
			}
			switch {
			case tc.rate == nil && r.rate != nil:
				t.Errorf("rate %v, want null", *r.rate)
			case tc.rate != nil && r.rate == nil:
				t.Errorf("rate null, want %v", *tc.rate)
			case tc.rate != nil && math.Abs(*r.rate-*tc.rate) > 1e-9:
				t.Errorf("rate %v, want %v", *r.rate, *tc.rate)
			}
		})
	}
}
