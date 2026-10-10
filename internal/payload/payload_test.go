package payload

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/text"
)

const uuid = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

func decode(s string) Payload { return Decode(strings.NewReader(s)) }

func TestLifecyclePayload(t *testing.T) {
	got := decode(`{
		"session_id": "3FA85F64-5717-4562-B3FC-2C963F66AFA6",
		"transcript_path": "/home/me/.claude/projects/x/` + uuid + `.jsonl",
		"cwd": "/home/me/code/sesshin",
		"hook_event_name": "StopFailure",
		"prompt_id": "p-1",
		"permission_mode": "acceptEdits",
		"source": "startup",
		"reason": "prompt_input_exit",
		"trigger": "auto",
		"error": "rate_limit",
		"notification_type": "permission_prompt",
		"new_cwd": "/tmp",
		"session_title": "api refactor",
		"model": "claude-opus-5-5",
		"background_tasks": [{"id": 1}, [2], "3", null],
		"session_crons": [],
		"tool_response": {"content": [1, {"a": "b"}]}
	}`)
	want := Payload{
		SessionID:        uuid,
		TranscriptPath:   "/home/me/.claude/projects/x/" + uuid + ".jsonl",
		Cwd:              "/home/me/code/sesshin",
		HookEventName:    "StopFailure",
		PromptID:         "p-1",
		PermissionMode:   "acceptEdits",
		Source:           "startup",
		Reason:           "prompt_input_exit",
		Trigger:          "auto",
		Error:            "rate_limit",
		NotificationType: "permission_prompt",
		NewCwd:           "/tmp",
		SessionTitle:     "api refactor",
		Model:            "claude-opus-5-5",
		BackgroundTasks:  4,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// The statusline's payload, as captured (internal/model/testdata).
func TestStatuslinePayload(t *testing.T) {
	got := Decode(bytes.NewReader(statuslinePayload(t)))
	want := Payload{
		SessionID:      uuid,
		HookEventName:  "Status",
		SessionName:    "api review",
		TranscriptPath: "/home/me/.claude/projects/-home-me-code-sesshin/" + uuid + ".jsonl",
		Cwd:            "/home/me/code/sesshin",
		Model:          "claude-opus-5-5",
	}
	// The fixture's numbers, read through the reference decoder: this test
	// is about the shape, TestReference about the values.
	ref, _ := reference(statuslinePayload(t))
	want.Cost, want.ContextWindow, want.RateLimits, want.PromptCache = ref.Cost, ref.ContextWindow, ref.RateLimits, ref.PromptCache
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	for name, v := range map[string]Num{
		"total_cost_usd": got.Cost.TotalCostUSD, "context_window_size": got.ContextWindow.ContextWindowSize,
		"five_hour.resets_at": got.RateLimits.FiveHour.ResetsAt, "expires_at": got.PromptCache.ExpiresAt,
	} {
		if !v.OK {
			t.Errorf("%s not decoded", name)
		}
	}
	if !got.PromptCache.Present || !got.PromptCache.Warm.OK || !got.PromptCache.CachingObserved.OK {
		t.Errorf("prompt_cache not decoded: %+v", got.PromptCache)
	}
}

// A field that is absent, null, or of the wrong type is empty, and the
// members after it still decode (hooks-spec.md, H6).
func TestWrongTypes(t *testing.T) {
	for _, v := range []string{`null`, `true`, `5`, `"x"`, `[]`, `[1,{"a":[]}]`, `{}`, `{"id":5,"a":{"b":[1]}}`} {
		in := `{"cwd":` + v + `,"model":` + v + `,"source":` + v + `,"background_tasks":` + v +
			`,"cost":` + v + `,"rate_limits":` + v + `,"prompt_cache":` + v + `,"session_id":"` + uuid + `"}`
		got := decode(in)
		if got.Err != nil || got.SessionID != uuid {
			t.Errorf("%s: later members lost: %+v", v, got)
		}
		want := Payload{SessionID: uuid}
		switch v {
		case `"x"`:
			want.Cwd, want.Model, want.Source = "x", "x", "x"
		case `[]`:
			// an array of anything counts
		case `[1,{"a":[]}]`:
			want.BackgroundTasks = 2
		case `{}`, `{"id":5,"a":{"b":[1]}}`:
			want.PromptCache.Present = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\ngot  %+v\nwant %+v", v, got, want)
		}
	}
}

func TestSessionID(t *testing.T) {
	for in, want := range map[string]string{
		`"` + uuid + `"`:                              uuid,
		`"` + strings.ToUpper(uuid) + `"`:             uuid,
		`"` + uuid + ` "`:                             "",
		`"` + uuid[:35] + `"`:                         "",
		`"3fa85f64-5717-4562-b3fc-2c963f66afa\u212a"`: "", // Kelvin sign: ToLower would make it k
		`"../../etc"`:                                 "",
		`5`:                                           "",
	} {
		if got := decode(`{"session_id":` + in + `}`).SessionID; got != want {
			t.Errorf("session_id %s: got %q, want %q", in, got, want)
		}
	}
}

// Copied enums pass the open-set shape guard; one that fails is absent
// (design-spec.md, Open sets).
func TestGuards(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Payload
	}{
		{`{"source":"startup","reason":"prompt_input_exit","trigger":"auto","error":"server_error"}`,
			Payload{Source: "startup", Reason: "prompt_input_exit", Trigger: "auto", Error: "server_error"}},
		{`{"source":"Startup","reason":"a b","trigger":"","error":"1x"}`, Payload{}},
		{`{"source":"a-b","reason":"a\nb","error":"` + strings.Repeat("a", 65) + `"}`, Payload{}},
		{`{"permission_mode":"acceptEdits"}`, Payload{PermissionMode: "acceptEdits"}},
		{`{"permission_mode":"bypass-permissions"}`, Payload{}},
	} {
		if got := decode(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\ngot  %+v\nwant %+v", tc.in, got, tc.want)
		}
	}
}

// Every string kept is scrubbed (hooks-spec.md, Reading the payload).
func TestScrubbed(t *testing.T) {
	got := decode(`{"cwd":"/a\nb","new_cwd":"/a\u2028b","session_title":"x\u0085y\ty","model":{"id":"m\u0000"},
		"prompt_id":"p\r","hook_event_name":"Stop\u007f","transcript_path":"/t\u2029","session_name":"n\u001b[31m",
		"notification_type":"x\n"}`)
	want := Payload{Cwd: "/a b", NewCwd: "/a b", SessionTitle: "x y y", Model: "m ", PromptID: "p ",
		HookEventName: "Stop ", TranscriptPath: "/t ", SessionName: "n [31m", NotificationType: "x "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// Members read before a syntax error are kept, and what follows it is not
// (implementation-spec.md, JSON reading).
func TestPartial(t *testing.T) {
	for _, in := range []string{
		`{"cwd":"/a","session_id":"` + uuid + `","model":"m",`,
		`{"cwd":"/a","session_id":"` + uuid + `","model":"m" "source":"startup"}`,
		`{"cwd":"/a","session_id":"` + uuid + `","model":"m","x":[1,}`,
		`{"cwd":"/a","session_id":"` + uuid + `","model":"m","x":` + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + `,"source":"startup"}`,
	} {
		got := decode(in)
		if got.Err == nil || got.Err == io.EOF {
			t.Errorf("%.80s: error %v", in, got.Err)
		}
		got.Err = nil
		if want := (Payload{Cwd: "/a", SessionID: uuid, Model: "m"}); !reflect.DeepEqual(got, want) {
			t.Errorf("%.80s:\ngot  %+v\nwant %+v", in, got, want)
		}
	}
}

func TestNotAPayload(t *testing.T) {
	for _, tc := range []struct {
		in           string
		empty, error bool
	}{
		{"", true, false},
		{" \n\t", true, false},
		{"[1]", false, true},
		{`"x"`, false, true},
		{"null", false, true},
		{"x", false, true},
		{"\xEF\xBB\xBF{}", false, true},
		{"{}", false, false},
		{`{} trailing`, false, true}, // data after the closing brace is reported
		{`{} {}`, false, true},
		{`{}, `, false, true},
		{"{}\n \t\r\n", false, false}, // whitespace is not
	} {
		got := decode(tc.in)
		if got.Empty != tc.empty || (got.Err != nil) != tc.error {
			t.Errorf("%q: Empty %v, Err %v; want %v, error %v", tc.in, got.Empty, got.Err, tc.empty, tc.error)
		}
	}
}

func TestNumbers(t *testing.T) {
	got := decode(`{"cost":{"total_cost_usd":1.5e400,"total_api_duration_ms":-0},
		"context_window":{"used_percentage":"35","total_input_tokens":351000,"context_window_size":1e6},
		"rate_limits":{"five_hour":{"used_percentage":22.4,"resets_at":1791051112},"seven_day":null}}`)
	want := Payload{
		Cost:          Cost{TotalAPIDurationMS: Num{0, true}},
		ContextWindow: ContextWindow{TotalInputTokens: Num{351000, true}, ContextWindowSize: Num{1e6, true}},
		RateLimits:    RateLimits{FiveHour: RateLimit{Num{22.4, true}, Num{1791051112, true}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// Keys match exactly, unlike json.Unmarshal's into a struct, and a repeated
// key takes its last value, even one of the wrong type.
func TestKeys(t *testing.T) {
	got := decode(`{"Session_ID":"` + uuid + `","CWD":"/a","cwd":"/b","cwd":"/c","source":"startup","source":5}`)
	if want := (Payload{Cwd: "/c"}); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// The decoder replaces invalid UTF-8 with U+FFFD, so rendering still works
// (implementation-spec.md, JSON reading).
func TestInvalidUTF8(t *testing.T) {
	if got := decode("{\"cwd\":\"/a\xffb\"}"); got.Cwd != "/a\uFFFDb" || got.Err != nil {
		t.Errorf("got %+v", got)
	}
}

// Decode agrees with a reference built on json.Unmarshal into a map, for any
// input both read whole, with nothing but whitespace after it; and whatever it returns holds only text, guarded
// enums, and a UUID or nothing.
func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"session_id":"` + uuid + `","cwd":"/a","model":{"id":"m"},"background_tasks":[1]}`))
	f.Add([]byte(`{"cost":{"total_cost_usd":1.2},"prompt_cache":{"warm":true,"expires_at":1e9}}`))
	f.Add([]byte(`{"rate_limits":{"five_hour":{"used_percentage":1,"resets_at":2}},"source":"x","permission_mode":"aB"}`))
	f.Add([]byte(`{"cwd":"a\u2028\n\u0085","x":[{"y":null}],"session_crons":{}}`))
	f.Add(statuslinePayload(f))
	f.Fuzz(func(t *testing.T, data []byte) {
		got := Decode(bytes.NewReader(data))
		for _, s := range []string{got.HookEventName, got.PromptID, got.NotificationType, got.Cwd, got.NewCwd,
			got.TranscriptPath, got.SessionTitle, got.SessionName, got.Model} {
			if !text.Is(s) {
				t.Errorf("%q is not text", s)
			}
		}
		for _, s := range []string{got.Source, got.Reason, got.Trigger, got.Error} {
			if s != "" && !model.IsEnum(s) {
				t.Errorf("%q fails the enum guard", s)
			}
		}
		if got.PermissionMode != "" && !model.IsPermissionMode(got.PermissionMode) {
			t.Errorf("permission_mode %q fails its guard", got.PermissionMode)
		}
		if got.SessionID != "" && !model.IsUUID(got.SessionID) {
			t.Errorf("session_id %q is not a UUID", got.SessionID)
		}
		want, ok := reference(data)
		if !ok {
			return
		}
		if got.Err != nil {
			t.Fatalf("%q: Err %v, but it reads whole", data, got.Err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q:\ngot  %+v\nwant %+v", data, got, want)
		}
	})
}

// reference decodes data as Decode should, from a map: ok is false when
// data's first value isn't an object json.Unmarshal reads whole.
func reference(data []byte) (Payload, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return Payload{}, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return Payload{}, false // data after the object
	}
	var p Payload
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	guard := func(k string, is func(string) bool) string {
		if s := str(m, k); is(s) {
			return s
		}
		return ""
	}
	obj := func(m map[string]any, k string) map[string]any { o, _ := m[k].(map[string]any); return o }
	num := func(m map[string]any, k string) Num {
		if n, ok := m[k].(json.Number); ok {
			if f, err := n.Float64(); err == nil {
				return Num{f, true}
			}
		}
		return Num{}
	}
	boolean := func(m map[string]any, k string) Bool { b, ok := m[k].(bool); return Bool{b, ok} }
	count := func(k string) int64 { a, _ := m[k].([]any); return int64(len(a)) }

	if s := lowerASCII(str(m, "session_id")); model.IsUUID(s) {
		p.SessionID = s
	}
	p.HookEventName = text.Scrub(str(m, "hook_event_name"))
	p.PromptID = text.Scrub(str(m, "prompt_id"))
	p.NotificationType = text.Scrub(str(m, "notification_type"))
	p.Cwd = text.Scrub(str(m, "cwd"))
	p.NewCwd = text.Scrub(str(m, "new_cwd"))
	p.TranscriptPath = text.Scrub(str(m, "transcript_path"))
	p.SessionTitle = text.Scrub(str(m, "session_title"))
	p.SessionName = text.Scrub(str(m, "session_name"))
	p.Model = text.Scrub(str(m, "model"))
	if o := obj(m, "model"); o != nil {
		p.Model = text.Scrub(str(o, "id"))
	}
	p.Source = guard("source", model.IsEnum)
	p.Reason = guard("reason", model.IsEnum)
	p.Trigger = guard("trigger", model.IsEnum)
	p.Error = guard("error", model.IsEnum)
	p.PermissionMode = guard("permission_mode", model.IsPermissionMode)
	p.BackgroundTasks = count("background_tasks")
	p.SessionCrons = count("session_crons")
	c := obj(m, "cost")
	p.Cost = Cost{num(c, "total_cost_usd"), num(c, "total_api_duration_ms")}
	w := obj(m, "context_window")
	p.ContextWindow = ContextWindow{num(w, "total_input_tokens"), num(w, "context_window_size"), num(w, "used_percentage")}
	r := obj(m, "rate_limits")
	limit := func(k string) RateLimit {
		l := obj(r, k)
		return RateLimit{num(l, "used_percentage"), num(l, "resets_at")}
	}
	p.RateLimits = RateLimits{limit("five_hour"), limit("seven_day")}
	if pc := obj(m, "prompt_cache"); pc != nil {
		p.PromptCache = PromptCache{true, boolean(pc, "warm"), boolean(pc, "caching_observed"),
			num(pc, "expires_at"), num(pc, "recache_tokens_if_cold")}
	}
	return p, true
}

// statuslinePayload is the payload of internal/model's statusline.json
// fixture, a captured tick.
func statuslinePayload(tb testing.TB) []byte {
	tb.Helper()
	data, err := os.ReadFile("../model/testdata/statusline.json")
	if err != nil {
		tb.Fatal(err)
	}
	var f struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		tb.Fatal(err)
	}
	return f.Payload
}
