package statusline

import (
	"math"
	"time"

	"github.com/phansen314/sesshin/internal/payload"
)

// State is the derived state of a session's prompt cache.
type State int

const (
	Unknown State = iota
	Warm
	Cold
)

func (s State) String() string {
	switch s {
	case Warm:
		return "warm"
	case Cold:
		return "cold"
	}
	return "unknown"
}

// CacheState derives the prompt cache's state at now (design-spec.md, Prompt
// cache). warm is read through expires_at, never on its own: a stored warm of
// true goes stale the moment the cache expires.
//
// The edges the spec leaves open: an absent caching_observed is not false,
// so it doesn't make the state unknown; and a now exactly equal to
// expires_at is cold, since warm needs now strictly before it.
func CacheState(pc payload.PromptCache, now time.Time) State {
	if !pc.Present || (pc.CachingObserved.OK && !pc.CachingObserved.V) || !pc.Warm.OK {
		return Unknown
	}
	if !pc.Warm.V {
		return Cold
	}
	if !pc.ExpiresAt.OK {
		return Unknown
	}
	if now.Before(unixFloat(pc.ExpiresAt.V)) {
		return Warm
	}
	return Cold
}

// unixFloat is a Unix time in seconds with a fractional part.
func unixFloat(v float64) time.Time {
	sec := math.Floor(v)
	return time.Unix(int64(sec), int64((v-sec)*1e9))
}

// promptCacheSegment is the prompt-cache segment (♨️ until 3:04PM, 🧊 ~45k),
// which goes last on line 1. "" when the state is unknown.
func promptCacheSegment(v View) string {
	pc := v.Payload.PromptCache
	var expires time.Time
	if pc.ExpiresAt.OK {
		expires = time.Unix(int64(pc.ExpiresAt.V), 0)
	}
	n := pc.RecacheTokensIfCold
	return CacheText(CacheState(pc, v.Now), expires, n.V, n.OK && n.V >= 0, v.Loc)
}

// CacheText is how the prompt cache is shown, by the statusline and by
// jump's Cache column: warm as ♨️ until <time of day in loc> (a time of day,
// like the 5h reset, with no date), cold as 🧊 ~<tokens> when the recache cost
// is known, else 🧊 cold. It is "" for an unknown state, and for a warm one
// with no expiry (a zero expires). A nil loc is UTC.
func CacheText(state State, expires time.Time, recacheTokens float64, haveRecache bool, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	switch state {
	case Warm:
		if expires.IsZero() {
			return ""
		}
		return "♨️ until " + expires.In(loc).Format("3:04PM")
	case Cold:
		if haveRecache {
			return "🧊 ~" + tokens(recacheTokens)
		}
		return "🧊 cold"
	}
	return ""
}
