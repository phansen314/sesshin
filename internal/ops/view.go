package ops

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/payload"
	"github.com/phansen314/sesshin/internal/statusline"
)

// SessionView is one session as every read reports it (operations.md,
// Session view): what is stored, and what is derived from it at read time.
// Its fields are in the schema's order, which is the order they are written
// in. A pointer, or an empty-able slice behind one, is null when nil.
type SessionView struct {
	ID               *int64           `json:"id"`
	SessionID        string           `json:"session_id"`
	Name             string           `json:"name"`
	Job              *string          `json:"job"`
	Source           *string          `json:"source"`
	Extra            *jsonio.Object   `json:"extra"`
	Headless         bool             `json:"headless"`
	Liveness         string           `json:"liveness"`
	Status           string           `json:"status"`
	StallReason      *string          `json:"stall_reason"`
	Pending          *Pending         `json:"pending"`
	Attention        *string          `json:"attention"`
	Cwd              *string          `json:"cwd"`
	GitBranch        *string          `json:"git_branch"`
	Model            *string          `json:"model"`
	PermissionMode   *string          `json:"permission_mode"`
	Entrypoint       *string          `json:"entrypoint"`
	Nested           *bool            `json:"nested"`
	PID              *int64           `json:"pid"`
	PIDStartedAt     *string          `json:"pid_started_at"`
	StartedAt        model.Timestamp  `json:"started_at"`
	LastStartAt      model.Timestamp  `json:"last_start_at"`
	LastEventAt      model.Timestamp  `json:"last_event_at"`
	LastEventType    string           `json:"last_event_type"`
	EventSeq         int64            `json:"event_seq"`
	LastSeen         model.Timestamp  `json:"last_seen"`
	EndedAt          *model.Timestamp `json:"ended_at"`
	EndReason        *string          `json:"end_reason"`
	Compactions      int64            `json:"compactions"`
	Metrics          *Metrics         `json:"metrics"`
	PromptCache      *PromptCache     `json:"prompt_cache"`
	Placement        *jsonio.Object   `json:"placement"`
	TranscriptPath   *string          `json:"transcript_path"`
	TranscriptExists *bool            `json:"transcript_exists"`
}

// Pending is the session's pending work.
type Pending struct {
	BackgroundTasks int64 `json:"background_tasks"`
	SessionCrons    int64 `json:"session_crons"`
}

// Metrics is what the last statusline tick reported.
type Metrics struct {
	ReceivedAt     model.Timestamp `json:"received_at"`
	CostUSD        *float64        `json:"cost_usd"`
	BurnUSDPerHour *float64        `json:"burn_usd_per_hour"`
	APIDurationMS  *int64          `json:"api_duration_ms"`
	ContextTokens  *int64          `json:"context_tokens"`
	ContextWindow  *int64          `json:"context_window"`
	ContextPercent *float64        `json:"context_percent"`
	RateLimits     *jsonio.Object  `json:"rate_limits"`
}

// PromptCache is the session's prompt cache, derived at read time.
type PromptCache struct {
	State         string    `json:"state"`
	ExpiresAt     *string   `json:"expires_at"`
	RecacheTokens *int64    `json:"recache_tokens"`
	HitRatio      *float64  `json:"hit_ratio"`
	Misses        *int64    `json:"misses"`
	LastMissCause *[]string `json:"last_miss_cause"`
}

// viewFields are the session view's property names, in order: what `fields`
// may name.
var viewFields = func() []string {
	t := reflect.TypeFor[SessionView]()
	names := make([]string, t.NumField())
	for i := range names {
		names[i], _, _ = strings.Cut(t.Field(i).Tag.Get("json"), ",")
	}
	return names
}()

// project is the view with only the fields named in keep, in the view's
// order.
func (v SessionView) project(keep map[string]bool) *jsonio.Object {
	rv := reflect.ValueOf(v)
	t := rv.Type()
	o := &jsonio.Object{}
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if keep[name] {
			o.Members = append(o.Members, jsonio.Member{Key: name, Value: rv.Field(i).Interface()})
		}
	}
	return o
}

// ref is the session as an error or warning names it (operations.md,
// Session ref).
func (v SessionView) ref() SessionRef {
	return SessionRef{ID: v.ID, SessionID: v.SessionID, Name: v.Name}
}

// SessionRef is the smallest way to name a session.
type SessionRef struct {
	ID        *int64 `json:"id"`
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

// viewer builds views from the sessions read.
type viewer struct {
	fs  fsys.FS
	now time.Time
}

// view derives one session's view (operations.md, Session view).
func (vw viewer) view(r *sessionRec) SessionView {
	l := r.Lifecycle
	st := r.Statusline
	v := SessionView{
		ID:             r.sesshinID(),
		SessionID:      r.ID,
		Headless:       r.isHeadless(),
		Liveness:       r.res.State.String(),
		Status:         l.Status,
		StallReason:    l.StallReason,
		Cwd:            l.Cwd,
		Model:          l.Model,
		PermissionMode: l.PermissionMode,
		Entrypoint:     l.Entrypoint,
		Nested:         l.Nested,
		PID:            l.PID,
		PIDStartedAt:   l.PIDStartedAt,
		StartedAt:      l.StartedAt,
		LastStartAt:    l.LastStartAt,
		LastEventAt:    l.LastEventAt,
		LastEventType:  l.LastEventType,
		EventSeq:       l.EventSeq,
		LastSeen:       r.res.LastSeen,
		EndedAt:        l.EndedAt,
		Compactions:    l.Compactions,
		TranscriptPath: l.TranscriptPath,
	}
	v.Job = r.job
	if r.Sesshin != nil {
		v.Source = &r.Sesshin.Source
		v.Placement = r.Sesshin.Placement
		v.Extra = r.Sesshin.Extra
	}
	if r.res.EndReason != "" {
		v.EndReason = &r.res.EndReason
	}
	if l.BackgroundTasks != nil {
		p := Pending{BackgroundTasks: *l.BackgroundTasks}
		if l.SessionCrons != nil {
			p.SessionCrons = *l.SessionCrons
		}
		v.Pending = &p
	}
	v.Attention = attention(v.Liveness, l.Status, l.StallReason, v.Pending)
	if v.PID == nil && st != nil {
		v.PID, v.PIDStartedAt = st.PID, st.PIDStartedAt
	}
	v.TranscriptExists = vw.transcriptExists(l.TranscriptPath)

	var pl payload.Payload
	if st != nil {
		v.GitBranch = st.GitBranch
		if b, err := json.Marshal(st.Payload); err == nil {
			pl = payload.Decode(bytes.NewReader(b))
		}
		v.Metrics = metrics(st, pl)
		v.PromptCache = vw.promptCache(st, pl)
		if st.ReceivedAt >= l.LastStartAt && pl.Model != "" {
			m := pl.Model
			v.Model = &m
		}
	}
	v.Name = viewName(l, pl, v.ID, r.ID)
	return v
}

// The attention values (design-spec.md, Attention).
const (
	AttentionBlocked    = "blocked"
	AttentionStalled    = "stalled"
	AttentionSelfWaking = "self_waking"
	AttentionYourTurn   = "your_turn"
	AttentionIdle       = "idle"
	AttentionWorking    = "working"
	AttentionUnknown    = "unknown"
)

// attention is what the session wants from you, the first that applies
// (design-spec.md, Attention); nil for an ended session.
func attention(liveness, status string, stallReason *string, pending *Pending) *string {
	if liveness == live.Ended.String() {
		return nil
	}
	a := AttentionUnknown
	switch status {
	case model.StatusNeedsApproval:
		a = AttentionBlocked
	case model.StatusWaiting:
		switch {
		case stallReason != nil:
			a = AttentionStalled
		case pending != nil && (pending.BackgroundTasks > 0 || pending.SessionCrons > 0):
			a = AttentionSelfWaking
		default:
			a = AttentionYourTurn
		}
	case model.StatusIdle:
		a = AttentionIdle
	case model.StatusWorking:
		a = AttentionWorking
	}
	return &a
}

// viewName is the name (design-spec.md, Terms): the /rename title, else the
// statusline's session name, else #id, else the UUID's first 8 characters.
func viewName(l *model.LifecycleFile, pl payload.Payload, id *int64, uuid string) string {
	switch {
	case l.SessionTitle != nil && *l.SessionTitle != "":
		return *l.SessionTitle
	case pl.SessionName != "":
		return pl.SessionName
	case id != nil:
		return "#" + strconv.FormatInt(*id, 10)
	}
	return uuid[:8]
}

// transcriptExists stats the transcript: nil when there is no path, a
// relative one, which would be read against sesshin's own directory, or a stat
// that failed other than with ENOENT.
func (vw viewer) transcriptExists(path *string) *bool {
	if path == nil || !filepath.IsAbs(*path) {
		return nil
	}
	_, err := vw.fs.Stat(*path)
	switch {
	case err == nil:
		return ptrTo(true)
	case errors.Is(err, fs.ErrNotExist):
		return ptrTo(false)
	}
	return nil
}

func metrics(st *model.StatuslineFile, pl payload.Payload) *Metrics {
	m := &Metrics{
		ReceivedAt:     st.ReceivedAt,
		CostUSD:        floatOf(pl.Cost.TotalCostUSD),
		BurnUSDPerHour: st.BurnUSDPerHour,
		APIDurationMS:  intOf(pl.Cost.TotalAPIDurationMS),
		ContextTokens:  intOf(pl.ContextWindow.TotalInputTokens),
		ContextWindow:  intOf(pl.ContextWindow.ContextWindowSize),
		ContextPercent: floatOf(pl.ContextWindow.UsedPercentage),
	}
	if o, ok := member(st.Payload.Tree, "rate_limits").(*jsonio.Object); ok {
		m.RateLimits = o
	}
	return m
}

func (vw viewer) promptCache(st *model.StatuslineFile, pl payload.Payload) *PromptCache {
	pc := &PromptCache{State: statusline.CacheState(pl.PromptCache, vw.now).String()}
	if pc.State == "warm" {
		pc.ExpiresAt = expiresAt(pl.PromptCache.ExpiresAt.V)
	}
	if n := pl.PromptCache.RecacheTokensIfCold; n.OK && n.V >= 0 {
		pc.RecacheTokens = intOf(n)
	}
	cache, _ := member(st.Payload.Tree, "prompt_cache").(*jsonio.Object)
	pc.HitRatio = floatOfValue(member(cache, "hit_ratio"))
	pc.Misses = intOfValue(member(cache, "misses"))
	if lm, ok := member(cache, "last_miss_cause").(*jsonio.Object); ok {
		if arr, ok := member(lm, "causes").([]any); ok {
			causes := make([]string, 0, len(arr))
			for _, item := range arr {
				s, ok := item.(string)
				if !ok {
					causes = nil
					break
				}
				causes = append(causes, s)
			}
			if causes != nil {
				pc.LastMissCause = &causes
			}
		}
	}
	return pc
}

// expiresAt is the Unix time in seconds as a timestamp, its fraction
// dropped; nil for one no timestamp can hold.
func expiresAt(unix float64) *string {
	const maxUnix = 253402300799 // 9999-12-31T23:59:59Z
	sec := math.Floor(unix)
	if !(sec >= 0 && sec <= maxUnix) {
		return nil
	}
	return ptrTo(string(model.FormatTimestamp(time.Unix(int64(sec), 0))))
}

// member is the value of key in o, nil when o is nil or has no such member.
func member(o *jsonio.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func floatOf(n payload.Num) *float64 {
	if !n.OK {
		return nil
	}
	return &n.V
}

// intOf is a payload number that is a whole number in the range every JSON
// reader holds exactly; any other reads as null.
func intOf(n payload.Num) *int64 {
	if !n.OK || n.V != math.Floor(n.V) || math.Abs(n.V) > model.MaxSafe {
		return nil
	}
	return ptrTo(int64(n.V))
}

func floatOfValue(v any) *float64 {
	n, ok := v.(json.Number)
	if !ok {
		return nil
	}
	f, err := n.Float64()
	if err != nil {
		return nil
	}
	return &f
}

func intOfValue(v any) *int64 {
	f := floatOfValue(v)
	if f == nil {
		return nil
	}
	return intOf(payload.Num{V: *f, OK: true})
}
