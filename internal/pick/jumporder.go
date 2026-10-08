package pick

import (
	"cmp"
	"slices"
	"time"

	"github.com/phansen314/sesshin/internal/ops"
)

// sortJump sorts views into jump order (picker-spec.md, Jump order), once.
// views come in session order, which breaks ties: the sort is stable.
func sortJump(views []ops.SessionView) {
	slices.SortStableFunc(views, compareJump)
}

// compareJump orders two sessions by the jump order's keys 1 to 6, 0 when
// they tie (key 7, session order, is the stable sort's).
func compareJump(a, b ops.SessionView) int {
	ta, tb := tier(attentionOf(a)), tier(attentionOf(b))
	if c := cmp.Compare(ta, tb); c != 0 {
		return c
	}
	switch ta {
	case tierWants:
		return compareWants(a, b)
	case tierIdle:
		return b.LastEventAt.Time().Compare(a.LastEventAt.Time()) // the latest first
	}
	return a.LastEventAt.Time().Compare(b.LastEventAt.Time()) // the earliest first
}

// The tiers, in order (key 1).
const (
	tierWants = iota // blocked, stalled, your_turn
	tierIdle
	tierSelfWaking
	tierWorking
	tierUnknown
)

func attentionOf(v ops.SessionView) string {
	if v.Attention == nil {
		return ops.AttentionUnknown
	}
	return *v.Attention
}

func tier(attention string) int {
	switch attention {
	case ops.AttentionBlocked, ops.AttentionStalled, ops.AttentionYourTurn:
		return tierWants
	case ops.AttentionIdle:
		return tierIdle
	case ops.AttentionSelfWaking:
		return tierSelfWaking
	case ops.AttentionWorking:
		return tierWorking
	}
	return tierUnknown
}

// The prompt cache states, in order (key 2).
const (
	cacheWarm = iota
	cacheCold
	cacheUnknown
)

func cacheRank(v ops.SessionView) int {
	if v.PromptCache != nil {
		switch v.PromptCache.State {
		case "warm":
			return cacheWarm
		case "cold":
			return cacheCold
		}
	}
	return cacheUnknown
}

// wantsRank is blocked, stalled, then your_turn (key 4).
func wantsRank(attention string) int {
	switch attention {
	case ops.AttentionBlocked:
		return 0
	case ops.AttentionStalled:
		return 1
	}
	return 2
}

// compareWants orders two sessions of the first tier (keys 2 to 4).
func compareWants(a, b ops.SessionView) int {
	ca, cb := cacheRank(a), cacheRank(b)
	if c := cmp.Compare(ca, cb); c != 0 {
		return c
	}
	if ca == cacheWarm {
		// Whatever the session wants, the earliest expiry first; a warm cache
		// with no expires_at (a time no timestamp holds) after the rest.
		ea, oka := expiry(a)
		eb, okb := expiry(b)
		switch {
		case oka && okb:
			return ea.Compare(eb)
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	}
	aa, ab := attentionOf(a), attentionOf(b)
	if c := cmp.Compare(wantsRank(aa), wantsRank(ab)); c != 0 {
		return c
	}
	if aa == ops.AttentionYourTurn {
		return b.LastEventAt.Time().Compare(a.LastEventAt.Time()) // the latest first
	}
	return a.LastEventAt.Time().Compare(b.LastEventAt.Time()) // the longest waiting first
}

// expiry is the warm cache's expires_at.
func expiry(v ops.SessionView) (time.Time, bool) {
	if v.PromptCache == nil || v.PromptCache.ExpiresAt == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, *v.PromptCache.ExpiresAt)
	return t, err == nil
}
