package kitty

import (
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func obj(t *testing.T, s string) *jsonio.Object {
	t.Helper()
	o, _, err := jsonio.ParseObject([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func enc(t *testing.T, o *jsonio.Object) string {
	t.Helper()
	if o == nil {
		return "null"
	}
	b, err := jsonio.MarshalLine(o)
	if err != nil {
		t.Fatal(err)
	}
	return string(b[:len(b)-1])
}

func TestRecognize(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		"in kitty": {map[string]string{"KITTY_LISTEN_ON": "unix:/tmp/kitty-554338", "KITTY_WINDOW_ID": "7"},
			`{"terminal":"kitty","socket":"unix:/tmp/kitty-554338","window_id":7}`},
		"placeholder socket kept": {map[string]string{"KITTY_LISTEN_ON": "unix:/tmp/kitty-{kitty.pid}-4099", "KITTY_WINDOW_ID": "12"},
			`{"terminal":"kitty","socket":"unix:/tmp/kitty-{kitty.pid}-4099","window_id":12}`},
		"remote control off": {map[string]string{"KITTY_WINDOW_ID": "7"}, "null"},
		"socket, no window":  {map[string]string{"KITTY_LISTEN_ON": "unix:/x"}, "null"},
		"not kitty":          {map[string]string{}, "null"},
		"window zero":        {map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "0"}, "null"},
		"window negative":    {map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "-3"}, "null"},
		"window text":        {map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "abc"}, "null"},
		"window fraction":    {map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "7.0"}, "null"},
		"window padded":      {map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "07"}, "null"},
		"window plus":        {map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "+7"}, "null"},
		"window too big":     {map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "99999999999999999999"}, "null"},
		"window empty":       {map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": ""}, "null"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := Recognize(env(tc.env))
			if s := enc(t, got); s != tc.want {
				t.Errorf("got %s, want %s", s, tc.want)
			}
			parsed, ok := RecognizeParsed(env(tc.env))
			if ok != (got != nil) {
				t.Fatalf("RecognizeParsed %v, Recognize %s", ok, enc(t, got))
			}
			if got != nil {
				if p, ok := Parse(got); !ok || p != parsed {
					t.Errorf("a recognized placement parses as %+v, %v; RecognizeParsed says %+v", p, ok, parsed)
				}
			}
		})
	}
}

const (
	synced = `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"api","user_vars":{"p":"a"}}`
	bare   = `{"terminal":"kitty","socket":"unix:/x","window_id":7}`
)

func TestReplace(t *testing.T) {
	tests := map[string]struct {
		next, old string
		resumed   bool
		want      string
	}{
		"same window keeps sync keys":           {bare, synced, false, synced},
		"same window, none to keep":             {bare, bare, false, bare},
		"only the title":                        {bare, `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"t"}`, false, `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"t"}`},
		"different window drops them":           {`{"terminal":"kitty","socket":"unix:/x","window_id":8}`, synced, false, `{"terminal":"kitty","socket":"unix:/x","window_id":8}`},
		"different socket drops them":           {`{"terminal":"kitty","socket":"unix:/y","window_id":7}`, synced, false, `{"terminal":"kitty","socket":"unix:/y","window_id":7}`},
		"no old placement":                      {bare, "", false, bare},
		"old not kitty":                         {bare, `{"terminal":"tmux","socket":"unix:/x","window_id":7,"tab_title":"t"}`, false, bare},
		"old invalid keeps nothing":             {bare, `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"t","user_vars":{"p":1}}`, false, bare},
		"old unknown keys not carried":          {bare, `{"terminal":"kitty","socket":"unix:/x","window_id":7,"extra":1,"tab_title":"t"}`, false, `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"t"}`},
		"nothing recognized is null":            {"", synced, false, "null"},
		"nothing recognized, no old":            {"", "", false, "null"},
		"new socket key order is next's":        {bare, `{"user_vars":{},"tab_title":"t","window_id":7,"socket":"unix:/x","terminal":"kitty"}`, false, `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":"t","user_vars":{}}`},
		"resumed keeps them for another window": {`{"terminal":"kitty","socket":"unix:/y","window_id":8}`, synced, true, `{"terminal":"kitty","socket":"unix:/y","window_id":8,"tab_title":"api","user_vars":{"p":"a"}}`},
		"resumed, old invalid keeps nothing":    {`{"terminal":"kitty","socket":"unix:/y","window_id":8}`, `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":1}`, true, `{"terminal":"kitty","socket":"unix:/y","window_id":8}`},
		"resumed, old not kitty keeps nothing":  {bare, `{"terminal":"tmux","socket":"unix:/x","window_id":7,"tab_title":"t"}`, true, bare},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var next, old *jsonio.Object
			if tc.next != "" {
				next = obj(t, tc.next)
			}
			if tc.old != "" {
				old = obj(t, tc.old)
			}
			nextBefore, oldBefore := enc(t, next), enc(t, old)
			if got := enc(t, Replace(next, old, tc.resumed)); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
			if enc(t, next) != nextBefore || enc(t, old) != oldBefore {
				t.Error("Replace changed an argument")
			}
		})
	}
}

func TestPlacementFunc(t *testing.T) {
	placementOf := func(getenv func(string) string) func(old *jsonio.Object, resumed bool) *jsonio.Object {
		return func(old *jsonio.Object, resumed bool) *jsonio.Object {
			return Backend{}.Replace(Backend{}.Recognize(getenv), old, resumed)
		}
	}
	f := placementOf(env(map[string]string{"KITTY_LISTEN_ON": "unix:/x", "KITTY_WINDOW_ID": "7"}))
	if got := enc(t, f(obj(t, synced), false)); got != synced {
		t.Errorf("same window: %s", got)
	}
	if got := enc(t, f(nil, false)); got != bare {
		t.Errorf("no old: %s", got)
	}
	other := `{"terminal":"kitty","socket":"unix:/x","window_id":9,"tab_title":"api","user_vars":{"p":"a"}}`
	if got := enc(t, f(obj(t, other), false)); got != bare {
		t.Errorf("another window: %s", got)
	}
	if got := enc(t, f(obj(t, other), true)); got != synced {
		t.Errorf("another window, resumed: %s", got)
	}
	if got := placementOf(env(nil))(obj(t, synced), true); got != nil {
		t.Errorf("outside kitty: %s", enc(t, got))
	}
}

func TestParse(t *testing.T) {
	valid := map[string]string{
		"bare":            bare,
		"synced":          synced,
		"empty user vars": `{"terminal":"kitty","socket":"unix:/x","window_id":7,"user_vars":{}}`,
		"placeholder":     `{"terminal":"kitty","socket":"unix:/tmp/kitty-{kitty.pid}-4099","window_id":1}`,
		"unknown key":     `{"terminal":"kitty","socket":"unix:/x","window_id":7,"future":[1]}`,
	}
	for name, s := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if _, ok := Parse(obj(t, s)); !ok {
				t.Errorf("%s rejected", s)
			}
		})
	}
	invalid := map[string]string{
		"no terminal":           `{"socket":"unix:/x","window_id":7}`,
		"other terminal":        `{"terminal":"tmux","socket":"unix:/x","window_id":7}`,
		"terminal not string":   `{"terminal":1,"socket":"unix:/x","window_id":7}`,
		"no socket":             `{"terminal":"kitty","window_id":7}`,
		"empty socket":          `{"terminal":"kitty","socket":"","window_id":7}`,
		"socket not string":     `{"terminal":"kitty","socket":4,"window_id":7}`,
		"socket null":           `{"terminal":"kitty","socket":null,"window_id":7}`,
		"no window":             `{"terminal":"kitty","socket":"unix:/x"}`,
		"window string":         `{"terminal":"kitty","socket":"unix:/x","window_id":"7"}`,
		"window fraction":       `{"terminal":"kitty","socket":"unix:/x","window_id":7.5}`,
		"window exponent":       `{"terminal":"kitty","socket":"unix:/x","window_id":7e0}`,
		"window zero":           `{"terminal":"kitty","socket":"unix:/x","window_id":0}`,
		"window negative":       `{"terminal":"kitty","socket":"unix:/x","window_id":-1}`,
		"window null":           `{"terminal":"kitty","socket":"unix:/x","window_id":null}`,
		"title not string":      `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":3}`,
		"title null":            `{"terminal":"kitty","socket":"unix:/x","window_id":7,"tab_title":null}`,
		"vars not object":       `{"terminal":"kitty","socket":"unix:/x","window_id":7,"user_vars":"a"}`,
		"vars null":             `{"terminal":"kitty","socket":"unix:/x","window_id":7,"user_vars":null}`,
		"vars array":            `{"terminal":"kitty","socket":"unix:/x","window_id":7,"user_vars":[]}`,
		"vars value not string": `{"terminal":"kitty","socket":"unix:/x","window_id":7,"user_vars":{"a":"b","c":2}}`,
		"vars nested":           `{"terminal":"kitty","socket":"unix:/x","window_id":7,"user_vars":{"a":{}}}`,
	}
	for name, s := range invalid {
		t.Run("invalid/"+name, func(t *testing.T) {
			if _, ok := Parse(obj(t, s)); ok {
				t.Errorf("%s accepted", s)
			}
		})
	}
	if _, ok := Parse(nil); ok {
		t.Error("nil accepted")
	}
	got, _ := Parse(obj(t, synced))
	if got != (Parsed{Socket: "unix:/x", WindowID: 7}) {
		t.Errorf("parsed %+v", got)
	}
}
