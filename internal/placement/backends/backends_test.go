package backends

import (
	"os/exec"
	"strings"
	"testing"
)

// No backend links model: a placement is the backend's, and the files are
// not (implementation-spec.md, Import direction). What they share is below
// both, in text and jsonio.
func TestNoBackendLinksModel(t *testing.T) {
	const internal = "github.com/phansen314/sesshin/internal/"
	out, err := exec.Command("go", "list", "-deps", internal+"placement", internal+"placement/backends").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if p == internal+"model" {
			t.Fatal("a backend links model")
		}
	}
}
