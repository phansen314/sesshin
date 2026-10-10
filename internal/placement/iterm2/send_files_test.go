package iterm2

import (
	"errors"
	"os"
	"testing"
)

// The paste file is gone however the call ended: a timeout, a refusal, an
// answer that is not ok, and a success.
func TestSendRemovesItsFile(t *testing.T) {
	for name, r := range map[string]func(call) ([]byte, error){
		"timeout":    failing(timeout),
		"permission": failing(permission),
		"failure":    failing(errors.New("exit status 1")),
		"not found":  reply("not-found\n"),
		"ok":         reply("ok\n"),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			b := newBackend(&fakeRunner{reply: r})
			b.TempDir = dir
			b.Send(placed(t, uuidUpper), "text", false)
			if left, _ := os.ReadDir(dir); len(left) != 0 {
				t.Errorf("left %v", left)
			}
		})
	}
	t.Run("directory missing", func(t *testing.T) {
		b := newBackend(&fakeRunner{reply: reply("ok")})
		b.TempDir = "/no/such/dir"
		if err := b.Send(placed(t, uuidUpper), "text", false); err == nil {
			t.Error("sent without a file")
		}
	})
}
