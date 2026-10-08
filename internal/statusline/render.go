package statusline

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/testhook"
)

// View is what step 5 renders from: the payload, what steps 1–4 found, and
// the two values read from files only for display.
type View struct {
	// SesshinID is the session's sesshin ID, nil when it wasn't read.
	SesshinID *int64
	// Title is lifecycle.json's session_title, "" when unset or unreadable.
	Title   string
	Payload payload.Payload
	Found   Found
	// Now is the tick's start. The prompt-cache segment tells warm from
	// cold by it.
	Now time.Time
	// Loc is the zone times of day are shown in. nil is UTC; the hook passes
	// the local zone, and a test pins its own.
	Loc *time.Location
}

// Fallback is the line printed when rendering panicked: the sesshin ID segment
// when the ID was read, then the context segment at zero.
func Fallback(id *int64) []byte {
	if id == nil {
		return []byte(FallbackLine)
	}
	return []byte("#" + strconv.FormatInt(*id, 10) + " | " + FallbackLine)
}

// renderTick is step 5: it reads the sesshin ID and the session title, which
// are read for display and never written (design-spec.md, Two tiers), and
// renders the line. A panic discards what was built and returns the fallback
// line, with the ID if it was read by then, after logging the panic.
func renderTick(t Tick, ses *session, f Found, loc *time.Location, render func(View) []byte) (out []byte) {
	var id *int64
	defer func() {
		if r := recover(); r != nil {
			out = Fallback(id)
			t.Log("statusline step 5 (render): panic: " + describe(r))
		}
	}()
	testhook.At("statusline:5")
	id = ses.sesshinID()
	v := View{SesshinID: id, Title: ses.title(), Payload: t.Payload, Found: f, Now: t.Now, Loc: loc}
	return render(v)
}

// sesshinID reads sesshin.json with no lock: nil when it is missing, unreadable,
// unusable, or has no ID yet. Nothing is logged: the line only hides the
// segment.
func (ses *session) sesshinID() *int64 {
	if ses.root == nil {
		return nil
	}
	h, st, _ := readFile(ses.root, model.SesshinName, model.ReadSesshin)
	if st != model.FileUsable {
		return nil
	}
	return h.ID
}

// title is session_title from lifecycle.json: "" when the file couldn't be
// read, is unusable, or has none.
func (ses *session) title() string {
	if ses.lifeStatus != model.FileUsable || ses.life.SessionTitle == nil {
		return ""
	}
	return *ses.life.SessionTitle
}

// Render is hooks-spec.md, Rendering: line 1, then line 2 on its own line
// when it has a segment, with no trailing newline.
func Render(v View) []byte {
	loc := v.Loc
	if loc == nil {
		loc = time.UTC
	}
	p := v.Payload
	var a []string
	if v.SesshinID != nil {
		a = append(a, "#"+strconv.FormatInt(*v.SesshinID, 10))
	}
	name := v.Title
	if name == "" {
		name = p.SessionName
	}
	if name != "" {
		a = append(a, "⬢ "+name)
	}
	a = append(a, contextSegment(p.ContextWindow))
	if s := promptCacheSegment(v); s != "" {
		a = append(a, s)
	}
	if dir := baseName(p.Cwd); dir != "" {
		a = append(a, "📁 "+dir)
	}
	if b := v.Found.GitBranch; b != nil && *b != "" {
		a = append(a, "🌿 "+*b)
	}
	if m := modelName(p.Model); m != "" {
		a = append(a, "🤖 "+m)
	}
	if c := p.Cost.TotalCostUSD; c.OK {
		a = append(a, "💰 $"+strconv.FormatFloat(c.V, 'f', 2, 64))
	}
	if b := v.Found.BurnUSDPerHour; b != nil {
		a = append(a, "🔥 $"+strconv.FormatFloat(*b, 'f', 2, 64)+"/h")
	}
	if p.Cost.TotalAPIDurationMS.OK {
		if d := duration(p.Cost.TotalAPIDurationMS.V); d != "" {
			a = append(a, "⌛ "+d+" API")
		}
	}

	var b []string
	if s := rateLimit(p.RateLimits.FiveHour, "5h", "3:04PM", loc); s != "" {
		b = append(b, "⏱️ "+s)
	}
	if s := rateLimit(p.RateLimits.SevenDay, "7d", "1/2 3:04PM", loc); s != "" {
		if len(b) == 0 {
			s = "⏱️ " + s
		}
		b = append(b, s)
	}
	out := strings.Join(a, " | ")
	if len(b) > 0 {
		out += "\n" + strings.Join(b, " | ")
	}
	return []byte(out)
}

// contextSegment is 🧠: the one segment that always renders.
func contextSegment(c payload.ContextWindow) string {
	pct, counts := ContextText(c.UsedPercentage, c.TotalInputTokens, c.ContextWindowSize)
	s := FallbackLine
	if pct != "" {
		s = "🧠 " + pct
	}
	if counts != "" {
		s += " " + counts
	}
	return s
}

// ContextText is the context segment's text without the 🧠: the percentage,
// "" when missing, and the counts as used/size, "" unless both are present
// and neither is negative. contextSegment and jump's preview both use it.
func ContextText(pct, used, size payload.Num) (percentage, counts string) {
	if pct.OK {
		percentage = Percent(pct.V)
	}
	if used.OK && size.OK && used.V >= 0 && size.V >= 0 {
		counts = tokens(used.V) + "/" + tokens(size.V)
	}
	return percentage, counts
}

// Percent is v rounded half away from zero, with no decimals.
func Percent(v float64) string {
	r := math.Round(v)
	if r == 0 {
		r = 0 // not -0
	}
	return strconv.FormatFloat(r, 'f', 0, 64) + "%"
}

// tokens formats a token count: the integer below 1,000, then k, then M with
// a decimal, whose trailing .0 is dropped.
func tokens(v float64) string {
	n := math.Round(v)
	switch {
	case n < 1000:
		return strconv.FormatFloat(n, 'f', 0, 64)
	case n < 999500:
		return strconv.FormatFloat(math.Round(n/1000), 'f', 0, 64) + "k"
	}
	// Tenths of a million, rounded half away from zero.
	tenths := strconv.FormatFloat(math.Round(n/100000), 'f', 0, 64)
	whole, frac := tenths[:len(tenths)-1], tenths[len(tenths)-1:]
	if frac == "0" {
		return whole + "M"
	}
	return whole + "." + frac + "M"
}

// duration is ms floored to whole minutes: Mm, or HhMm from an hour; <1m
// above zero and under a minute; "" at zero or below.
func duration(ms float64) string {
	if ms <= 0 {
		return ""
	}
	mins := math.Floor(ms / 60000)
	switch {
	case mins < 1:
		return "<1m"
	case mins < 60:
		return strconv.FormatFloat(mins, 'f', 0, 64) + "m"
	}
	h := math.Floor(mins / 60)
	return strconv.FormatFloat(h, 'f', 0, 64) + "h" + strconv.FormatFloat(mins-h*60, 'f', 0, 64) + "m"
}

// rateLimit is one window's segment, without the ⏱️: "" when the percentage
// is missing. layout is the reset's time layout.
func rateLimit(r payload.RateLimit, label, layout string, loc *time.Location) string {
	if !r.UsedPercentage.OK {
		return ""
	}
	s := label + " " + Percent(r.UsedPercentage.V)
	if r.ResetsAt.OK {
		s += " resets " + time.Unix(int64(r.ResetsAt.V), 0).In(loc).Format(layout)
	}
	return s
}

// baseName is the last element of a path, "" for none. A path of only
// slashes is "/".
func baseName(p string) string {
	if p == "" {
		return ""
	}
	t := strings.TrimRight(p, "/")
	if t == "" {
		return "/"
	}
	return t[strings.LastIndexByte(t, '/')+1:]
}

// modelName is "Family Version" from a model id, by the rule in hooks-spec.md,
// Rendering, never from a list of known families.
func modelName(id string) string {
	if i := strings.LastIndex(id, "claude-"); i >= 0 {
		id = id[i+len("claude-"):]
	}
	if i := strings.IndexByte(id, '['); i >= 0 {
		id = id[:i]
	}
	if i := dateIndex(id); i >= 0 {
		id = id[:i]
	}
	family := ""
	var version []string
	for _, tok := range strings.Split(id, "-") {
		switch {
		case tok == "":
		case allDigits(tok):
			version = append(version, tok)
		case family == "":
			family = titleCase(tok)
		}
	}
	if family == "" {
		return ""
	}
	if len(version) == 0 {
		return family
	}
	return family + " " + strings.Join(version, ".")
}

// dateIndex is the index of the first '-' followed by eight digits, or -1.
func dateIndex(s string) int {
	for i := 0; i+9 <= len(s); i++ {
		if s[i] == '-' && allDigits(s[i+1:i+9]) {
			return i
		}
	}
	return -1
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// titleCase upper-cases the first rune and lower-cases the rest.
func titleCase(s string) string {
	_, n := utf8.DecodeRuneInString(s)
	return strings.ToUpper(s[:n]) + strings.ToLower(s[n:])
}
