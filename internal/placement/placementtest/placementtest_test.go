package placementtest

import (
	"os/exec"
	"strings"
	"testing"
)

// No binary links this package: it is a kitty that runs nothing, for tests
// only.
func TestNotLinked(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/phansen314/sesshin/cmd/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if p == "github.com/phansen314/sesshin/internal/placement/placementtest" {
			t.Fatal("a binary links placementtest")
		}
	}
}
