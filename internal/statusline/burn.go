package statusline

import (
	"math"
	"time"

	"github.com/phansen314/sesshin/internal/model"
)

// The burn rate's windows (design-spec.md, statusline.json).
const (
	// floor is how old the sample must be to compute a rate from; before
	// it, the previous tick's rate is carried forward.
	floor = 60 * time.Second
	// replaceAfter is when the sample is replaced by this tick's.
	replaceAfter = 300 * time.Second
	// maxAge is the oldest a sample may be and still give a rate: an idle
	// gap never averages into it.
	maxAge = 600 * time.Second
)

// computeBurn is step 4: the cost_sample to store and burn_usd_per_hour,
// from the previous tick's sample and this tick's total_cost_usd, with the
// ages measured between the two received_at timestamps (whole seconds), as
// a reader could recompute them from the file.
//
// What the design spec leaves open is decided here:
//   - a tick with no usable cost (absent, not a number, negative) has no
//     sample to offer: the base is kept, and past the floor the rate is
//     null;
//   - the first tick, with no previous sample, has a rate of null, and
//     starts a sample if it has a cost;
//   - a cost below the base's is a new run's (a sample from another
//     session's total, say): the rate is null and the sample is replaced
//     at once, past the floor, not after 300 seconds;
//   - a base dated after this tick (the clock stepped back) is replaced,
//     with a rate of null.
func computeBurn(t Tick, prev *model.StatuslineFile) burnResult {
	now := model.FormatTimestamp(t.Now)
	var cur *model.CostSample
	if c := t.Payload.Cost.TotalCostUSD; c.OK && c.V >= 0 && !math.IsInf(c.V, 0) && !math.IsNaN(c.V) {
		cur = &model.CostSample{At: now, USD: c.V}
	}
	if prev == nil || prev.CostSample == nil {
		return burnResult{sample: cur}
	}
	base := prev.CostSample
	age := now.Time().Sub(base.At.Time())
	switch {
	case age < 0:
		return burnResult{sample: cur}
	case age < floor:
		return burnResult{sample: base, rate: prev.BurnUSDPerHour}
	case cur == nil:
		return burnResult{sample: base}
	}
	r := burnResult{sample: base}
	if age <= maxAge && cur.USD > base.USD {
		if v := (cur.USD - base.USD) / age.Seconds() * 3600; !math.IsInf(v, 0) {
			r.rate = &v
		}
	}
	if age > replaceAfter || cur.USD < base.USD {
		r.sample = cur
	}
	return r
}
