package statusline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/payload"
)

// zone is a fixed zone, so no test depends on the machine's.
var zone = time.FixedZone("MST", -7*3600)

func num(v float64) payload.Num { return payload.Num{V: v, OK: true} }
func ptr[T any](v T) *T         { return &v }

func render(v View) string {
	if v.Loc == nil {
		v.Loc = zone
	}
	return string(Render(v))
}

// at is a Unix time that reads h:m on the given month and day of 2026 in zone.
func at(month time.Month, day, h, m int) float64 {
	return float64(time.Date(2026, month, day, h, m, 0, 0, zone).Unix())
}

func TestRenderSegments(t *testing.T) {
	full := payload.Payload{
		SessionName: "named",
		Cwd:         "/home/me/code/sesshin",
		Model:       "claude-opus-5-5",
		Cost:        payload.Cost{TotalCostUSD: num(1.2), TotalAPIDurationMS: num(12 * 60000)},
		ContextWindow: payload.ContextWindow{
			UsedPercentage: num(35), TotalInputTokens: num(351000), ContextWindowSize: num(1000000)},
		RateLimits: payload.RateLimits{
			FiveHour: payload.RateLimit{UsedPercentage: num(22), ResetsAt: num(at(10, 3, 15, 0))},
			SevenDay: payload.RateLimit{UsedPercentage: num(41), ResetsAt: num(at(10, 6, 9, 0))},
		},
	}
	found := Found{GitBranch: ptr("main"), BurnUSDPerHour: ptr(3.4)}
	without := func(f func(*View)) View {
		v := View{SesshinID: ptr(int64(12)), Title: "api refactor", Payload: full, Found: found}
		f(&v)
		return v
	}
	const line1 = "#12 | ⬢ api refactor | 🧠 35% 351k/1M | 📁 sesshin | 🌿 main | 🤖 Opus 5.5 | 💰 $1.20 | 🔥 $3.40/h | ⌛ 12m API"
	const line2 = "⏱️ 5h 22% resets 3:00PM | 7d 41% resets 10/6 9:00AM"
	tests := []struct {
		name string
		v    View
		want string
	}{
		// The spec's example, without the prompt cache segment (see cache_test.go).
		{"everything shown", without(func(*View) {}), line1 + "\n" + line2},
		{"no sesshin ID", without(func(v *View) { v.SesshinID = nil }), strings.TrimPrefix(line1, "#12 | ") + "\n" + line2},
		{"title wins over session_name", without(func(v *View) { v.Payload.SessionName = "x" }), line1 + "\n" + line2},
		{"session_name when no title", without(func(v *View) { v.Title = "" }), strings.Replace(line1, "api refactor", "named", 1) + "\n" + line2},
		{"no name", without(func(v *View) { v.Title, v.Payload.SessionName = "", "" }), strings.Replace(line1, "⬢ api refactor | ", "", 1) + "\n" + line2},
		{"no tokens without the window size", without(func(v *View) { v.Payload.ContextWindow.ContextWindowSize = payload.Num{} }),
			strings.Replace(line1, " 351k/1M", "", 1) + "\n" + line2},
		{"no tokens without the count", without(func(v *View) { v.Payload.ContextWindow.TotalInputTokens = payload.Num{} }),
			strings.Replace(line1, " 351k/1M", "", 1) + "\n" + line2},
		{"no percentage still renders the brain", without(func(v *View) { v.Payload.ContextWindow.UsedPercentage = payload.Num{} }),
			strings.Replace(line1, "35%", "0%", 1) + "\n" + line2},
		{"no cwd", without(func(v *View) { v.Payload.Cwd = "" }), strings.Replace(line1, "📁 sesshin | ", "", 1) + "\n" + line2},
		{"no branch", without(func(v *View) { v.Found.GitBranch = nil }), strings.Replace(line1, "🌿 main | ", "", 1) + "\n" + line2},
		{"empty branch", without(func(v *View) { v.Found.GitBranch = ptr("") }), strings.Replace(line1, "🌿 main | ", "", 1) + "\n" + line2},
		{"no model", without(func(v *View) { v.Payload.Model = "" }), strings.Replace(line1, "🤖 Opus 5.5 | ", "", 1) + "\n" + line2},
		{"model with no family", without(func(v *View) { v.Payload.Model = "5-5" }), strings.Replace(line1, "🤖 Opus 5.5 | ", "", 1) + "\n" + line2},
		{"no cost", without(func(v *View) { v.Payload.Cost.TotalCostUSD = payload.Num{} }), strings.Replace(line1, "💰 $1.20 | ", "", 1) + "\n" + line2},
		{"zero cost is shown", without(func(v *View) { v.Payload.Cost.TotalCostUSD = num(0) }), strings.Replace(line1, "$1.20", "$0.00", 1) + "\n" + line2},
		{"no burn", without(func(v *View) { v.Found.BurnUSDPerHour = nil }), strings.Replace(line1, "🔥 $3.40/h | ", "", 1) + "\n" + line2},
		{"zero burn is shown", without(func(v *View) { v.Found.BurnUSDPerHour = ptr(0.0) }), strings.Replace(line1, "$3.40/h", "$0.00/h", 1) + "\n" + line2},
		{"no duration", without(func(v *View) { v.Payload.Cost.TotalAPIDurationMS = payload.Num{} }), strings.TrimSuffix(line1, " | ⌛ 12m API") + "\n" + line2},
		{"zero duration", without(func(v *View) { v.Payload.Cost.TotalAPIDurationMS = num(0) }), strings.TrimSuffix(line1, " | ⌛ 12m API") + "\n" + line2},
		{"no rate limits: one line", without(func(v *View) { v.Payload.RateLimits = payload.RateLimits{} }), line1},
		{"5h hidden: the clock moves to 7d", without(func(v *View) { v.Payload.RateLimits.FiveHour = payload.RateLimit{} }),
			line1 + "\n⏱️ 7d 41% resets 10/6 9:00AM"},
		{"7d hidden", without(func(v *View) { v.Payload.RateLimits.SevenDay = payload.RateLimit{} }),
			line1 + "\n⏱️ 5h 22% resets 3:00PM"},
		{"5h without a reset", without(func(v *View) { v.Payload.RateLimits.FiveHour.ResetsAt = payload.Num{} }),
			line1 + "\n⏱️ 5h 22% | 7d 41% resets 10/6 9:00AM"},
		{"7d without a reset", without(func(v *View) { v.Payload.RateLimits.SevenDay.ResetsAt = payload.Num{} }),
			line1 + "\n⏱️ 5h 22% resets 3:00PM | 7d 41%"},
		{"a reset with no percentage is hidden", without(func(v *View) { v.Payload.RateLimits.FiveHour.UsedPercentage = payload.Num{} }),
			line1 + "\n⏱️ 7d 41% resets 10/6 9:00AM"},
		{"only the brain", View{}, "🧠 0%"},
		{"no trailing newline with line 2 hidden", View{Payload: payload.Payload{Cwd: "/x"}}, "🧠 0% | 📁 x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := render(tc.v); got != tc.want {
				t.Errorf("got\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// The exact bytes of hooks-spec.md's example, less its prompt-cache segment
// (see cache_test.go).
func TestRenderSpecExample(t *testing.T) {
	v := View{
		SesshinID: ptr(int64(12)), Title: "api refactor",
		Payload: payload.Payload{
			Cwd: "/work/sesshin", Model: "claude-opus-5-5",
			Cost:          payload.Cost{TotalCostUSD: num(1.2), TotalAPIDurationMS: num(12*60000 + 500)},
			ContextWindow: payload.ContextWindow{UsedPercentage: num(34.6), TotalInputTokens: num(351000), ContextWindowSize: num(1e6)},
			RateLimits: payload.RateLimits{
				FiveHour: payload.RateLimit{UsedPercentage: num(22), ResetsAt: num(at(10, 3, 15, 0))},
				SevenDay: payload.RateLimit{UsedPercentage: num(41), ResetsAt: num(at(10, 6, 9, 0))}},
		},
		Found: Found{GitBranch: ptr("main"), BurnUSDPerHour: ptr(3.4)},
	}
	want := "#12 | ⬢ api refactor | 🧠 35% 351k/1M | 📁 sesshin | 🌿 main | 🤖 Opus 5.5 | 💰 $1.20 | 🔥 $3.40/h | ⌛ 12m API\n" +
		"⏱️ 5h 22% resets 3:00PM | 7d 41% resets 10/6 9:00AM"
	if got := render(v); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestModelName(t *testing.T) {
	tests := []struct{ id, want string }{
		{"claude-opus-5-5", "Opus 5.5"},
		{"us.anthropic.claude-opus-4-20250514-v1:0", "Opus 4"},
		{"claude-sonnet-4-6[1m]", "Sonnet 4.6"},
		{"claude-3-5-sonnet-20241022", "Sonnet 3.5"},
		{"claude-opus", "Opus"},
		{"opus", "Opus"},
		{"claude-fable-5-1", "Fable 5.1"},
		{"claude-haiku-4-5-20251001", "Haiku 4.5"},
		{"claude-opus-4-1-20250805", "Opus 4.1"},
		{"claude-opus-4-v1:0", "Opus 4"},
		{"claude-opus--4", "Opus 4"},
		{"claude-opus-5-5[1m]-x", "Opus 5.5"},
		{"claude-claude-opus-5", "Opus 5"},
		{"claude-OPUS-5", "Opus 5"},
		{"claude-éclair-2", "Éclair 2"},
		{"claude-opus-4-2025051", "Opus 4.2025051"}, // seven digits is no date
		{"claude-opus-1234567890", "Opus"},          // a date is eight digits and what follows
		{"gpt-4", "Gpt 4"},
		{"claude-", ""},
		{"", ""},
		{"5-5", ""},
		{"[1m]", ""},
		{"claude-5-5", ""},
	}
	for _, tc := range tests {
		if got := modelName(tc.id); got != tc.want {
			t.Errorf("modelName(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestTokens(t *testing.T) {
	tests := []struct {
		n    float64
		want string
	}{
		{0, "0"}, {1, "1"}, {999, "999"}, {999.4, "999"}, {999.5, "1k"},
		{1000, "1k"}, {1499, "1k"}, {1500, "2k"}, {351000, "351k"}, {45000, "45k"},
		{999499, "999k"}, {999500, "1M"}, {999999, "1M"},
		{1000000, "1M"}, {1049999, "1M"}, {1050000, "1.1M"}, {1250000, "1.3M"},
		{1500000, "1.5M"}, {2000000, "2M"}, {12340000, "12.3M"},
	}
	for _, tc := range tests {
		if got := tokens(tc.n); got != tc.want {
			t.Errorf("tokens(%v) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestPercent(t *testing.T) {
	tests := []struct {
		v    float64
		want string
	}{
		{0, "0%"}, {0.4, "0%"}, {-0.4, "0%"}, {0.5, "1%"}, {1.5, "2%"}, {2.5, "3%"}, {-2.5, "-3%"},
		{34.6, "35%"}, {99.5, "100%"}, {100, "100%"}, {150, "150%"},
	}
	for _, tc := range tests {
		if got := percent(tc.v); got != tc.want {
			t.Errorf("percent(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestDuration(t *testing.T) {
	const min = 60000
	tests := []struct {
		ms   float64
		want string
	}{
		{-5, ""}, {0, ""}, {1, "<1m"}, {59999, "<1m"}, {min, "1m"}, {min + 59999, "1m"},
		{12 * min, "12m"}, {59 * min, "59m"}, {60 * min, "1h0m"}, {65 * min, "1h5m"},
		{65*min + 59999, "1h5m"}, {25*60*min + 7*min, "25h7m"},
	}
	for _, tc := range tests {
		if got := duration(tc.ms); got != tc.want {
			t.Errorf("duration(%v) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestTimes(t *testing.T) {
	rl := func(at float64) View {
		return View{Payload: payload.Payload{RateLimits: payload.RateLimits{
			FiveHour: payload.RateLimit{UsedPercentage: num(1), ResetsAt: num(at)},
			SevenDay: payload.RateLimit{UsedPercentage: num(2), ResetsAt: num(at)}}}}
	}
	tests := []struct {
		name string
		at   float64
		want string
	}{
		{"noon", at(10, 3, 12, 0), "⏱️ 5h 1% resets 12:00PM | 7d 2% resets 10/3 12:00PM"},
		{"midnight", at(10, 3, 0, 0), "⏱️ 5h 1% resets 12:00AM | 7d 2% resets 10/3 12:00AM"},
		{"12:05 after midnight", at(10, 3, 0, 5), "⏱️ 5h 1% resets 12:05AM | 7d 2% resets 10/3 12:05AM"},
		{"9:00AM has no leading zero", at(1, 9, 9, 0), "⏱️ 5h 1% resets 9:00AM | 7d 2% resets 1/9 9:00AM"},
		{"11:59PM", at(12, 31, 23, 59), "⏱️ 5h 1% resets 11:59PM | 7d 2% resets 12/31 11:59PM"},
		{"1:07PM", at(11, 15, 13, 7), "⏱️ 5h 1% resets 1:07PM | 7d 2% resets 11/15 1:07PM"},
		{"fractional seconds truncate", at(10, 3, 15, 0) + 0.9, "⏱️ 5h 1% resets 3:00PM | 7d 2% resets 10/3 3:00PM"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := render(rl(tc.at))
			if _, line2, _ := strings.Cut(got, "\n"); line2 != tc.want {
				t.Errorf("line 2 %q, want %q", line2, tc.want)
			}
		})
	}
	t.Run("the zone is the parameter's", func(t *testing.T) {
		v := rl(at(10, 3, 15, 0))
		v.Loc = time.UTC
		_, line2, _ := strings.Cut(string(Render(v)), "\n")
		if want := "⏱️ 5h 1% resets 10:00PM | 7d 2% resets 10/3 10:00PM"; line2 != want {
			t.Errorf("line 2 %q, want %q", line2, want)
		}
	})
	t.Run("a nil zone is UTC", func(t *testing.T) {
		v := rl(at(10, 3, 15, 0))
		_, line2, _ := strings.Cut(string(Render(v)), "\n")
		if want := "⏱️ 5h 1% resets 10:00PM | 7d 2% resets 10/3 10:00PM"; line2 != want {
			t.Errorf("line 2 %q, want %q", line2, want)
		}
	})
}

func TestBaseName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""}, {"/", "/"}, {"///", "/"}, {"/a", "a"}, {"/a/b", "b"}, {"/a/b/", "b"}, {"rel", "rel"}, {"a/b", "b"},
	}
	for _, tc := range tests {
		if got := baseName(tc.in); got != tc.want {
			t.Errorf("baseName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFallback(t *testing.T) {
	if got := string(Fallback(nil)); got != "🧠 0%" {
		t.Errorf("without an ID: %q", got)
	}
	if got := string(Fallback(ptr(int64(7)))); got != "#7 | 🧠 0%" {
		t.Errorf("with an ID: %q", got)
	}
}

// RenderTick reads the sesshin ID and the title from the session's files, and
// each is hidden when its file can't be used.
func TestRenderTickReads(t *testing.T) {
	lifecycle := func(r *rig) string { // the model's fixture has the title "api review"
		data, err := os.ReadFile(filepath.Join(r.dir, model.LifecycleName))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	tests := []struct {
		name  string
		setup func(r *rig)
		want  string
	}{
		{"both", func(r *rig) {
			put(t, filepath.Join(r.dir, "sesshin.json"), `{"schema": 2,"id":41,"job":null,"source":"hook","placement":null,"extra":{}}`)
		},
			"#41 | ⬢ api review | 🧠 0%"},
		{"no sesshin.json", func(*rig) {}, "⬢ api review | 🧠 0%"},
		{"id null", func(r *rig) {
			put(t, filepath.Join(r.dir, "sesshin.json"), `{"schema": 2,"id":null,"job":null,"source":"hook","placement":null,"extra":{}}`)
		},
			"⬢ api review | 🧠 0%"},
		{"unusable sesshin.json", func(r *rig) { put(t, filepath.Join(r.dir, "sesshin.json"), `{"schema":1,`) }, "⬢ api review | 🧠 0%"},
		{"other format sesshin.json", func(r *rig) {
			put(t, filepath.Join(r.dir, "sesshin.json"), `{"schema":99,"id":41,"job":null,"source":"hook","placement":null,"extra":{}}`)
		},
			"⬢ api review | 🧠 0%"},
		{"no lifecycle.json", func(r *rig) {
			os.Remove(filepath.Join(r.dir, model.LifecycleName))
			put(t, filepath.Join(r.dir, "sesshin.json"), `{"schema": 2,"id":41,"job":null,"source":"hook","placement":null,"extra":{}}`)
		}, "#41 | ⬢ named | 🧠 0%"},
		{"unusable lifecycle.json", func(r *rig) { put(t, filepath.Join(r.dir, model.LifecycleName), `{`) }, "⬢ named | 🧠 0%"},
		{"lifecycle.json of another session", func(r *rig) {
			put(t, filepath.Join(r.dir, model.LifecycleName), strings.Replace(lifecycle(r), sessionID, "00000000-0000-4000-8000-000000000000", 1))
		}, "⬢ named | 🧠 0%"},
		{"no title falls to session_name", func(r *rig) {
			put(t, filepath.Join(r.dir, model.LifecycleName), strings.Replace(lifecycle(r), `"session_title": "api review"`, `"session_title": null`, 1))
		}, "⬢ named | 🧠 0%"},
		{"no session directory", func(r *rig) { r.tick.SessionDir = filepath.Join(r.dir, "nope") }, "⬢ named | 🧠 0%"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, `{}`)
			tc.setup(r)
			p := payload.Payload{SessionName: "named"}
			if got := string(r.render(Found{}, p, Render)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if len(r.logs) != 0 {
				t.Errorf("logged %q", r.logs)
			}
		})
	}
}

// A panic in rendering discards the buffer: the fallback line, with the ID
// when it was read, and the panic logged.
func TestRenderTickPanic(t *testing.T) {
	r := newRig(t, `{}`)
	put(t, filepath.Join(r.dir, "sesshin.json"), `{"schema": 2,"id":41,"job":null,"source":"hook","placement":null,"extra":{}}`)
	got := r.render(Found{}, payload.Payload{}, func(View) []byte { panic("boom") })
	if string(got) != "#41 | 🧠 0%" {
		t.Errorf("got %q", got)
	}
	if len(r.logs) != 1 || r.logs[0] != "statusline step 5 (render): panic: boom" {
		t.Errorf("logs %q", r.logs)
	}
	r = newRig(t, `{}`)
	got = r.render(Found{}, payload.Payload{}, func(View) []byte { panic("boom") })
	if string(got) != "🧠 0%" {
		t.Errorf("without an ID: %q", got)
	}
}

// Rendering reads sesshin.json and records nothing from it: statusline.json is
// the same with and without.
func TestRenderWritesNothing(t *testing.T) {
	r := newRig(t, `{"cwd":"/"}`)
	put(t, filepath.Join(r.dir, "sesshin.json"), `{"schema": 2,"id":41,"job":null,"source":"hook","placement":null,"extra":{}}`)
	_, f := r.collect()
	if got := string(r.render(f, payload.Payload{}, Render)); got != "#41 | ⬢ api review | 🧠 0%" {
		t.Errorf("got %q", got)
	}
	r.noFile()
}
