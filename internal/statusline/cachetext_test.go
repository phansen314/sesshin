package statusline

import (
	"testing"
	"time"
)

// CacheText is shared with jump's Cache column.
func TestCacheText(t *testing.T) {
	zone := time.FixedZone("UTC+2", 2*3600)
	exp := time.Date(2026, 10, 4, 13, 5, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		state   State
		expires time.Time
		tokens  float64
		have    bool
		loc     *time.Location
		want    string
	}{
		{"warm in a zone", Warm, exp, 0, false, zone, "♨️ until 3:05PM"},
		{"warm, nil loc is UTC", Warm, exp, 0, false, nil, "♨️ until 1:05PM"},
		{"warm without an expiry", Warm, time.Time{}, 0, false, zone, ""},
		{"cold with a cost", Cold, time.Time{}, 45000, true, zone, "🧊 ~45k"},
		{"cold without", Cold, time.Time{}, 45000, false, zone, "🧊 cold"},
		{"unknown", Unknown, exp, 45000, true, zone, ""},
	} {
		if got := CacheText(tc.state, tc.expires, tc.tokens, tc.have, tc.loc); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
