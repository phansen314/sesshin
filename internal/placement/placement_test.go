package placement

import "testing"

func TestMultiplexed(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want bool
	}{
		"neither":        {map[string]string{}, false},
		"tmux":           {map[string]string{"TMUX": "/tmp/tmux-1000/default,1,0"}, true},
		"screen":         {map[string]string{"STY": "123.pts-0.host"}, true},
		"both":           {map[string]string{"TMUX": "x", "STY": "y"}, true},
		"empty is unset": {map[string]string{"TMUX": "", "STY": ""}, false},
		"kitty alone":    {map[string]string{"KITTY_WINDOW_ID": "7"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Multiplexed(func(k string) string { return tc.env[k] }); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
