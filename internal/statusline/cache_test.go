package statusline

import (
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/payload"
)

func boolean(v bool) payload.Bool { return payload.Bool{V: v, OK: true} }

// now is 3:00PM on 10/3 in zone.
var now = time.Unix(int64(at(10, 3, 15, 0)), 0)

func cache(warm, observed payload.Bool, expires payload.Num) payload.PromptCache {
	return payload.PromptCache{Present: true, Warm: warm, CachingObserved: observed, ExpiresAt: expires}
}

func TestCacheState(t *testing.T) {
	exp := now.Unix()
	tests := []struct {
		name string
		pc   payload.PromptCache
		want State
	}{
		{"no prompt_cache", payload.PromptCache{}, Unknown},
		{"caching not observed", cache(boolean(true), boolean(false), num(float64(exp+60))), Unknown},
		{"not observed and cold", cache(boolean(false), boolean(false), num(float64(exp+60))), Unknown},
		{"warm absent", cache(payload.Bool{}, boolean(true), num(float64(exp+60))), Unknown},
		{"warm true without expires_at", cache(boolean(true), boolean(true), payload.Num{}), Unknown},
		{"caching_observed absent is not false", cache(boolean(true), payload.Bool{}, num(float64(exp+60))), Warm},
		{"warm and before expires_at", cache(boolean(true), boolean(true), num(float64(exp+1))), Warm},
		{"fractional expires_at still ahead", cache(boolean(true), boolean(true), num(float64(exp)+0.5)), Warm},
		{"now equal to expires_at is cold", cache(boolean(true), boolean(true), num(float64(exp))), Cold},
		{"expires_at passed", cache(boolean(true), boolean(true), num(float64(exp-1))), Cold},
		{"expiry tick: warm false, expires_at unchanged", cache(boolean(false), boolean(true), num(float64(exp-1))), Cold},
		{"warm false without expires_at", cache(boolean(false), boolean(true), payload.Num{}), Cold},
		{"warm false, expires_at ahead", cache(boolean(false), boolean(true), num(float64(exp+60))), Cold},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CacheState(tc.pc, now); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPromptCacheSegment(t *testing.T) {
	exp := now.Unix()
	warm := cache(boolean(true), boolean(true), num(float64(exp+4*60)))
	withTokens := func(pc payload.PromptCache, n payload.Num) payload.PromptCache {
		pc.RecacheTokensIfCold = n
		return pc
	}
	tests := []struct {
		name string
		pc   payload.PromptCache
		want string
	}{
		{"warm", warm, "♨️ until 3:04PM"},
		{"warm ignores the token count", withTokens(warm, num(45000)), "♨️ until 3:04PM"},
		{"warm fractional expiry truncates", cache(boolean(true), boolean(true), num(float64(exp+4*60)+0.9)), "♨️ until 3:04PM"},
		{"warm across midnight shows no date", cache(boolean(true), boolean(true), num(at(10, 4, 0, 5))), "♨️ until 12:05AM"},
		{"cold with tokens", withTokens(cache(boolean(false), boolean(true), num(float64(exp-1))), num(45000)), "🧊 ~45k"},
		{"cold small count", withTokens(cache(boolean(false), boolean(true), num(float64(exp-1))), num(812)), "🧊 ~812"},
		{"cold zero tokens", withTokens(cache(boolean(false), boolean(true), num(float64(exp-1))), num(0)), "🧊 ~0"},
		{"cold millions", withTokens(cache(boolean(false), boolean(true), num(float64(exp-1))), num(1.5e6)), "🧊 ~1.5M"},
		{"cold without tokens", cache(boolean(false), boolean(true), num(float64(exp-1))), "🧊 cold"},
		{"cold with negative tokens", withTokens(cache(boolean(false), boolean(true), num(float64(exp-1))), num(-5)), "🧊 cold"},
		{"expiry tick", withTokens(cache(boolean(false), boolean(true), num(float64(exp-1))), num(45000)), "🧊 ~45k"},
		{"stale warm snapshot", withTokens(cache(boolean(true), boolean(true), num(float64(exp-600))), num(45000)), "🧊 ~45k"},
		{"stale warm snapshot without tokens", cache(boolean(true), boolean(true), num(float64(exp-600))), "🧊 cold"},
		{"no prompt_cache", payload.PromptCache{}, ""},
		{"caching not observed", cache(boolean(true), boolean(false), num(float64(exp+60))), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := View{Payload: payload.Payload{PromptCache: tc.pc}, Now: now, Loc: zone}
			if got := promptCacheSegment(v); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("the zone is the view's", func(t *testing.T) {
		v := View{Payload: payload.Payload{PromptCache: warm}, Now: now, Loc: time.UTC}
		if got, want := promptCacheSegment(v), "♨️ until 10:04PM"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// The segment follows the context segment and comes before the cwd.
func TestRenderPromptCacheAfterContext(t *testing.T) {
	pc := cache(boolean(true), boolean(true), num(at(10, 3, 15, 4)))
	v := View{
		SesshinID: ptr(int64(12)), Now: now,
		Payload: payload.Payload{
			Cwd:         "/home/u/sesshin",
			Cost:        payload.Cost{TotalAPIDurationMS: num(12 * 60000)},
			PromptCache: pc,
			RateLimits:  payload.RateLimits{FiveHour: payload.RateLimit{UsedPercentage: num(22)}},
		},
	}
	want := "#12 | 🧠 0% | ♨️ until 3:04PM | 📁 sesshin | ⌛ 12m API\n⏱️ 5h 22%"
	if got := render(v); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	v.Payload.PromptCache = payload.PromptCache{}
	if got := render(v); strings.Contains(got, "♨️") || strings.Contains(got, "🧊") {
		t.Errorf("segment shown without a prompt_cache: %q", got)
	}
}
