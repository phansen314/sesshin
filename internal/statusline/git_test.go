package statusline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/payload"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// git_branch (design-spec.md, statusline.json; hooks-spec.md, statusline
// step 3), from a walk up from cwd, with no git process.
func TestGitBranch(t *testing.T) {
	tests := []struct {
		name  string
		setup func(root string) (cwd string)
		want  string // "" means null
	}{
		{"repository root", func(r string) string {
			put(t, r+"/repo/.git/HEAD", "ref: refs/heads/main\n")
			return r + "/repo"
		}, "main"},
		{"subdirectory", func(r string) string {
			put(t, r+"/repo/.git/HEAD", "ref: refs/heads/feature/x\n")
			os.MkdirAll(r+"/repo/a/b", 0o755)
			return r + "/repo/a/b"
		}, "feature/x"},
		{"cwd that doesn't exist", func(r string) string {
			put(t, r+"/repo/.git/HEAD", "ref: refs/heads/main\n")
			return r + "/repo/gone/deeper"
		}, "main"},
		{"worktree, absolute gitdir", func(r string) string {
			put(t, r+"/main/.git/worktrees/wt/HEAD", "ref: refs/heads/wt-branch\n")
			put(t, r+"/wt/.git", "gitdir: "+r+"/main/.git/worktrees/wt\n")
			os.MkdirAll(r+"/wt/sub", 0o755)
			return r + "/wt/sub"
		}, "wt-branch"},
		{"worktree, relative gitdir", func(r string) string {
			put(t, r+"/main/.git/worktrees/wt/HEAD", "ref: refs/heads/rel\n")
			put(t, r+"/wt/.git", "gitdir: ../main/.git/worktrees/wt\n")
			return r + "/wt"
		}, "rel"},
		{"gitdir line without a newline, CRLF", func(r string) string {
			put(t, r+"/g/HEAD", "ref: refs/heads/crlf\r\n")
			put(t, r+"/wt/.git", "gitdir: ../g\r")
			return r + "/wt"
		}, "crlf"},
		{"detached HEAD", func(r string) string {
			put(t, r+"/repo/.git/HEAD", "0123456789abcdef0123456789abcdef01234567\n")
			return r + "/repo"
		}, ""},
		{"HEAD to a ref outside refs/heads", func(r string) string {
			put(t, r+"/repo/.git/HEAD", "ref: refs/remotes/origin/main\n")
			return r + "/repo"
		}, ""},
		{"empty branch name", func(r string) string {
			put(t, r+"/repo/.git/HEAD", "ref: refs/heads/\n")
			return r + "/repo"
		}, ""},
		{"branch name with a control character", func(r string) string {
			put(t, r+"/repo/.git/HEAD", "ref: refs/heads/a\tb\n")
			return r + "/repo"
		}, ""},
		{"no repository", func(r string) string {
			os.MkdirAll(r+"/plain/dir", 0o755)
			return r + "/plain/dir"
		}, ""},
		{"unreadable HEAD: a directory", func(r string) string {
			put(t, r+"/outer/.git/HEAD", "ref: refs/heads/outer\n")
			os.MkdirAll(r+"/outer/inner/.git/HEAD", 0o755)
			return r + "/outer/inner"
		}, ""},
		{"missing HEAD stops at the nearest .git", func(r string) string {
			put(t, r+"/outer/.git/HEAD", "ref: refs/heads/outer\n")
			os.MkdirAll(r+"/outer/inner/.git", 0o755)
			return r + "/outer/inner"
		}, ""},
		{".git file without a gitdir line", func(r string) string {
			put(t, r+"/outer/.git/HEAD", "ref: refs/heads/outer\n")
			put(t, r+"/outer/inner/.git", "not a gitdir\n")
			return r + "/outer/inner"
		}, ""},
		{"gitdir pointing nowhere", func(r string) string {
			put(t, r+"/wt/.git", "gitdir: /no/such/dir\n")
			return r + "/wt"
		}, ""},
		{"cwd a file", func(r string) string {
			put(t, r+"/repo/.git/HEAD", "ref: refs/heads/main\n")
			put(t, r+"/repo/file", "x")
			return r + "/repo/file/oops"
		}, "main"},
		{"relative cwd", func(r string) string { return "repo" }, ""},
		{"empty cwd", func(r string) string { return "" }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cwd := tc.setup(root)
			got := gitBranch(Tick{FS: fsys.OS{}, Payload: payload.Payload{Cwd: cwd}})
			switch {
			case tc.want == "" && got != nil:
				t.Errorf("branch %q, want null", *got)
			case tc.want != "" && got == nil:
				t.Errorf("branch null, want %q", tc.want)
			case got != nil && *got != tc.want:
				t.Errorf("branch %q, want %q", *got, tc.want)
			}
		})
	}
}

// Reading an unreadable HEAD (permissions) is null too.
func TestGitBranchPermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	r := t.TempDir()
	put(t, r+"/repo/.git/HEAD", "ref: refs/heads/main\n")
	if err := os.Chmod(r+"/repo/.git/HEAD", 0); err != nil {
		t.Fatal(err)
	}
	if got := gitBranch(Tick{FS: fsys.OS{}, Payload: payload.Payload{Cwd: r + "/repo"}}); got != nil {
		t.Errorf("branch %q, want null", *got)
	}
}
