package payload

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/phansen314/sesshin/internal/model"
)

// Payload is what sesshin reads of a hook's payload: the fields the hooks spec's
// Reads parts name. Every string passes model.IsText: scrubbed, or guarded
// by a pattern that refuses what Scrub replaces. "" means absent, failed
// its guard, or was of the wrong type.
type Payload struct {
	// Empty is stdin with no JSON at all: not a payload. The hook exits at
	// once; the statusline prints its fallback line.
	Empty bool
	// Err is why decoding stopped before the payload's end, or why the input
	// isn't just the payload: a syntax error, input that isn't an object,
	// nesting past the decoder's limit, or data after the closing brace. The
	// members read before it are kept, and recorded.
	Err error

	// SessionID is session_id lowercased, or "" when that isn't a UUID: it
	// becomes a path (design-spec.md, Session UUIDs).
	SessionID string

	HookEventName    string
	PromptID         string
	NotificationType string
	Cwd              string
	NewCwd           string
	TranscriptPath   string
	SessionTitle     string
	SessionName      string
	// Model is .model, a string on lifecycle payloads, or .model.id on the
	// statusline's.
	Model string

	// Copied enums, which pass model.IsEnum, or permission_mode's guard.
	Source         string
	Reason         string
	Trigger        string
	Error          string
	PermissionMode string

	// BackgroundTasks and SessionCrons are the arrays' lengths: 0 when
	// absent or not an array (hooks-spec.md, stop).
	BackgroundTasks int64
	SessionCrons    int64

	// The statusline's.
	Cost          Cost
	ContextWindow ContextWindow
	RateLimits    RateLimits
	PromptCache   PromptCache
}

// Num is a payload number: OK when present and a number a float64 holds.
// Absent is not zero (hooks-spec.md, Rendering).
type Num struct {
	V  float64
	OK bool
}

// Bool is a payload boolean: OK when present and a boolean.
type Bool struct {
	V  bool
	OK bool
}

// Cost is .cost.
type Cost struct {
	TotalCostUSD       Num
	TotalAPIDurationMS Num
}

// ContextWindow is .context_window.
type ContextWindow struct {
	TotalInputTokens  Num
	ContextWindowSize Num
	UsedPercentage    Num
}

// RateLimits is .rate_limits.
type RateLimits struct {
	FiveHour RateLimit
	SevenDay RateLimit
}

// RateLimit is one of .rate_limits' windows. ResetsAt is a Unix time in
// seconds.
type RateLimit struct {
	UsedPercentage Num
	ResetsAt       Num
}

// PromptCache is .prompt_cache (design-spec.md, Prompt cache). Present is
// whether it was an object: absent until the session's first API response.
// ExpiresAt is a Unix time in seconds.
type PromptCache struct {
	Present             bool
	Warm                Bool
	CachingObserved     Bool
	ExpiresAt           Num
	RecacheTokensIfCold Num
}

// Decode reads one payload from r, walking the standard library's
// token stream, into the struct: no tree is built, and values sesshin doesn't
// read are skipped. After the closing brace it looks for anything but
// whitespace, which it reports in Err.
func Decode(r io.Reader) Payload {
	var p Payload
	d := decoder{json.NewDecoder(r)}
	d.UseNumber()
	tok, err := d.Token()
	switch {
	case err == io.EOF:
		p.Empty = true
	case err != nil:
		p.Err = err
	case tok != json.Delim('{'):
		p.Err = errors.New("payload is not a JSON object")
	default:
		// Past the opening brace, the input ending is the payload cut short.
		switch p.Err = d.members(p.member(&d)); {
		case p.Err == io.EOF:
			p.Err = io.ErrUnexpectedEOF
		case p.Err == nil:
			// Anything but whitespace after the closing brace is not the
			// payload Claude Code sends.
			if _, err := d.Token(); err != io.EOF {
				p.Err = errors.New("trailing data after the payload")
			}
		}
	}
	return p
}

// member returns the handler for the payload's top-level members.
func (p *Payload) member(d *decoder) func(string) error {
	return func(key string) error {
		switch key {
		case "session_id":
			p.SessionID = ""
			return d.str(func(s string) {
				if s = lowerASCII(s); model.IsUUID(s) {
					p.SessionID = s
				}
			})
		case "hook_event_name":
			return d.text(&p.HookEventName)
		case "prompt_id":
			return d.text(&p.PromptID)
		case "notification_type":
			return d.text(&p.NotificationType)
		case "cwd":
			return d.text(&p.Cwd)
		case "new_cwd":
			return d.text(&p.NewCwd)
		case "transcript_path":
			return d.text(&p.TranscriptPath)
		case "session_title":
			return d.text(&p.SessionTitle)
		case "session_name":
			return d.text(&p.SessionName)
		case "model":
			return d.model(&p.Model)
		case "source":
			return d.guarded(&p.Source, model.IsEnum)
		case "reason":
			return d.guarded(&p.Reason, model.IsEnum)
		case "trigger":
			return d.guarded(&p.Trigger, model.IsEnum)
		case "error":
			return d.guarded(&p.Error, model.IsEnum)
		case "permission_mode":
			return d.guarded(&p.PermissionMode, model.IsPermissionMode)
		case "background_tasks":
			return d.count(&p.BackgroundTasks)
		case "session_crons":
			return d.count(&p.SessionCrons)
		case "cost":
			c := &p.Cost
			*c = Cost{}
			return d.object(func(key string) error {
				switch key {
				case "total_cost_usd":
					return d.num(&c.TotalCostUSD)
				case "total_api_duration_ms":
					return d.num(&c.TotalAPIDurationMS)
				}
				return d.skip()
			})
		case "context_window":
			c := &p.ContextWindow
			*c = ContextWindow{}
			return d.object(func(key string) error {
				switch key {
				case "total_input_tokens":
					return d.num(&c.TotalInputTokens)
				case "context_window_size":
					return d.num(&c.ContextWindowSize)
				case "used_percentage":
					return d.num(&c.UsedPercentage)
				}
				return d.skip()
			})
		case "rate_limits":
			p.RateLimits = RateLimits{}
			return d.object(func(key string) error {
				switch key {
				case "five_hour":
					return d.rateLimit(&p.RateLimits.FiveHour)
				case "seven_day":
					return d.rateLimit(&p.RateLimits.SevenDay)
				}
				return d.skip()
			})
		case "prompt_cache":
			c := &p.PromptCache
			*c = PromptCache{}
			return d.objectThen(func() { c.Present = true }, func(key string) error {
				switch key {
				case "warm":
					return d.boolean(&c.Warm)
				case "caching_observed":
					return d.boolean(&c.CachingObserved)
				case "expires_at":
					return d.num(&c.ExpiresAt)
				case "recache_tokens_if_cold":
					return d.num(&c.RecacheTokensIfCold)
				}
				return d.skip()
			})
		}
		return d.skip()
	}
}

// decoder reads values from the token stream. Each reader consumes exactly
// one value, whatever its type, and sets its target only when the value has
// the type it wants. Its caller clears the target first, so a repeated key
// takes its last value, as with json.Unmarshal, even when that value is
// absent for being the wrong type.
type decoder struct {
	*json.Decoder
}

// members reads an object's members, after its opening brace, through its
// closing one, calling member with each key: member must consume the value.
func (d decoder) members(member func(key string) error) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return nil // the closing brace: Token checks the syntax
		}
		if err := member(key); err != nil {
			return err
		}
	}
}

// object reads an object's members as members does, or skips a value of
// another type.
func (d decoder) object(member func(key string) error) error {
	return d.objectThen(nil, member)
}

// objectThen is object, calling opened first when the value is an object.
func (d decoder) objectThen(opened func(), member func(key string) error) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if tok != json.Delim('{') {
		return d.rest(tok)
	}
	if opened != nil {
		opened()
	}
	return d.members(member)
}

// str calls set with a string value.
func (d decoder) str(set func(string)) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if s, ok := tok.(string); ok {
		set(s)
		return nil
	}
	return d.rest(tok)
}

// text reads a string, scrubbed.
func (d decoder) text(dst *string) error {
	*dst = ""
	return d.str(func(s string) { *dst = model.Scrub(s) })
}

// guarded reads a string that passes is; one that fails is absent.
func (d decoder) guarded(dst *string, is func(string) bool) error {
	*dst = ""
	return d.str(func(s string) {
		if is(s) {
			*dst = s
		}
	})
}

// model reads .model: a string, or an object's id.
func (d decoder) model(dst *string) error {
	*dst = ""
	tok, err := d.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case string:
		*dst = model.Scrub(v)
		return nil
	case json.Delim:
		if v == '{' {
			return d.members(func(key string) error {
				if key == "id" {
					return d.text(dst)
				}
				return d.skip()
			})
		}
	}
	return d.rest(tok)
}

// num reads a number a float64 holds: Float64 fails past its range.
func (d decoder) num(dst *Num) error {
	*dst = Num{}
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if n, ok := tok.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			*dst = Num{f, true}
		}
		return nil
	}
	return d.rest(tok)
}

// boolean reads a boolean.
func (d decoder) boolean(dst *Bool) error {
	*dst = Bool{}
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if b, ok := tok.(bool); ok {
		*dst = Bool{b, true}
		return nil
	}
	return d.rest(tok)
}

// rateLimit reads one of .rate_limits' windows.
func (d decoder) rateLimit(dst *RateLimit) error {
	*dst = RateLimit{}
	return d.object(func(key string) error {
		switch key {
		case "used_percentage":
			return d.num(&dst.UsedPercentage)
		case "resets_at":
			return d.num(&dst.ResetsAt)
		}
		return d.skip()
	})
}

// count reads an array's length, skipping its elements.
func (d decoder) count(dst *int64) error {
	*dst = 0
	tok, err := d.Token()
	if err != nil {
		return err
	}
	if tok != json.Delim('[') {
		return d.rest(tok)
	}
	var n int64
	for d.More() {
		if err := d.skip(); err != nil {
			return err
		}
		n++
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	*dst = n
	return nil
}

// skip consumes one value without building it.
func (d decoder) skip() error {
	return d.Decode(&skipped{})
}

// rest consumes the rest of a value whose first token, tok, was read: the
// rest of an object or array, nothing for a scalar.
func (d decoder) rest(tok json.Token) error {
	switch tok {
	case json.Delim('{'):
		return d.members(func(string) error { return d.skip() })
	case json.Delim('['):
		for d.More() {
			if err := d.skip(); err != nil {
				return err
			}
		}
		_, err := d.Token()
		return err
	}
	return nil
}

// skipped is a value decoded and thrown away. The decoder still checks its
// syntax, but builds nothing and copies nothing.
type skipped struct{}

func (*skipped) UnmarshalJSON([]byte) error { return nil }

// lowerASCII lowercases A–Z only: a UUID is ASCII, and strings.ToLower
// would map some non-ASCII letters (U+212A, the Kelvin sign) to ASCII ones.
func lowerASCII(s string) string {
	for i := range len(s) {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; c >= 'A' && c <= 'Z' {
					b[j] = c + 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
