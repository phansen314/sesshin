package placement

import (
	"errors"
	"testing"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// fake is a backend that recognizes its terminal by one variable.
type fake struct{ tag, env string }

func (f fake) Tag() string { return f.tag }
func (f fake) Recognize(getenv func(string) string) *jsonio.Object {
	if getenv(f.env) == "" {
		return nil
	}
	return &jsonio.Object{Members: []jsonio.Member{{Key: "terminal", Value: f.tag}}}
}
func (fake) Replace(next, _ *jsonio.Object, _ bool) *jsonio.Object { return next }
func (fake) Valid(*jsonio.Object) bool                             { return false }
func (fake) Address(*jsonio.Object) *jsonio.Object                 { return nil }
func (fake) Stored(*jsonio.Object) (string, []Var, bool)           { return "", nil, false }

func getenv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestDetect(t *testing.T) {
	a, b := fake{"a", "A_WINDOW"}, fake{"b", "B_WINDOW"}
	list := []Backend{a, b}
	for name, tc := range map[string]struct {
		env  map[string]string
		list []Backend
		want string // the tag; "" for none
	}{
		"none recognizes":   {map[string]string{}, list, ""},
		"first":             {map[string]string{"A_WINDOW": "1"}, list, "a"},
		"second":            {map[string]string{"B_WINDOW": "1"}, list, "b"},
		"order decides":     {map[string]string{"A_WINDOW": "1", "B_WINDOW": "1"}, list, "a"},
		"reversed order":    {map[string]string{"A_WINDOW": "1", "B_WINDOW": "1"}, []Backend{b, a}, "b"},
		"tmux rules it out": {map[string]string{"A_WINDOW": "1", "TMUX": "/tmp/tmux,1,0"}, list, ""},
		"screen rules out":  {map[string]string{"B_WINDOW": "1", "STY": "1.pts-0"}, list, ""},
		"no backends":       {map[string]string{"A_WINDOW": "1"}, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, p := Detect(tc.list, getenv(tc.env))
			switch {
			case tc.want == "" && (got != nil || p != nil):
				t.Errorf("got %v, %v; want none", got, p)
			case tc.want != "" && (got == nil || got.Tag() != tc.want || TagOf(p) != tc.want):
				t.Errorf("got %v, %v; want %s", got, p, tc.want)
			}
		})
	}
}

func TestFor(t *testing.T) {
	a, b := fake{"a", "A"}, fake{"b", "B"}
	list := []Backend{a, b}
	if got := For(list, "b"); got != b {
		t.Errorf("got %v", got)
	}
	for _, tag := range []string{"c", "", "A"} {
		if got := For(list, tag); got != nil {
			t.Errorf("For(%q) = %v, want nil", tag, got)
		}
	}
	if For(nil, "a") != nil {
		t.Error("no backends: not nil")
	}
}

func TestOf(t *testing.T) {
	list := []Backend{fake{"a", "A"}}
	obj := func(tag any) *jsonio.Object {
		return &jsonio.Object{Members: []jsonio.Member{{Key: "terminal", Value: tag}}}
	}
	if Of(list, obj("a")) == nil || TagOf(obj("a")) != "a" {
		t.Error("a's placement is not found")
	}
	for name, p := range map[string]*jsonio.Object{
		"unknown tag": obj("zellij"),
		"not a tag":   obj(7),
		"no tag":      {},
		"nil":         nil,
	} {
		if Of(list, p) != nil {
			t.Errorf("%s: found a backend", name)
		}
	}
}

type timedOut struct{ timeout bool }

func (e timedOut) Error() string  { return "x" }
func (e timedOut) TimedOut() bool { return e.timeout }

func TestIsTimeout(t *testing.T) {
	if !IsTimeout(timedOut{true}) || IsTimeout(timedOut{false}) || IsTimeout(errors.New("x")) || IsTimeout(nil) {
		t.Error("IsTimeout")
	}
}

func TestLaunchAndSendErrors(t *testing.T) {
	boom := errors.New("boom")
	if !IsUnknown(&LaunchError{Unknown: true, Err: boom}) || IsUnknown(&LaunchError{Err: boom}) || IsUnknown(boom) {
		t.Error("IsUnknown")
	}
	if !IsSubmit(&SendError{Submit: true, Err: boom}) || IsSubmit(&SendError{Err: boom}) || IsSubmit(boom) {
		t.Error("IsSubmit")
	}
}
