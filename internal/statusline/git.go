package statusline

import (
	"errors"
	"io/fs"
	"path"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/text"
)

// gitBranch is step 3: git_branch, from a walk up from the payload's cwd to
// the first .git, a directory or a file, and its HEAD. It starts no git
// process, and every read goes through fsys.
//
// What the spec leaves open is decided here:
//   - a cwd that is empty or relative has no walk: it would mean the hook's
//     own directory, not the session's;
//   - a detached HEAD, or a symbolic one outside refs/heads/, has no
//     branch: null, not a commit's short name;
//   - a relative gitdir: is relative to the directory holding the .git
//     file, as git reads it;
//   - the first .git found is the answer: one that can't be read (a HEAD
//     that is missing or unreadable, a .git file without a gitdir: line, a
//     stat that fails other than as not found) is null, never the
//     enclosing repository's branch;
//   - a name that isn't text (control characters, invalid UTF-8) is null.
//
// The walk stops before /, as herd's did.
func gitBranch(t Tick) *string {
	dir := t.Payload.Cwd
	if !path.IsAbs(dir) {
		return nil
	}
	for dir = path.Clean(dir); dir != "/"; dir = path.Dir(dir) {
		dotgit := dir + "/.git"
		info, err := t.FS.Stat(dotgit)
		if err != nil {
			if notFound(err) {
				continue
			}
			return nil
		}
		head := dotgit + "/HEAD"
		if !info.IsDir() {
			data, err := t.FS.ReadFile(dotgit)
			if err != nil {
				return nil
			}
			line, _, _ := strings.Cut(string(data), "\n")
			gitdir, ok := strings.CutPrefix(strings.TrimRight(line, " \t\r"), "gitdir: ")
			if !ok || gitdir == "" {
				return nil
			}
			if !path.IsAbs(gitdir) {
				gitdir = dir + "/" + gitdir
			}
			head = path.Clean(gitdir) + "/HEAD"
		}
		data, err := t.FS.ReadFile(head)
		if err != nil {
			return nil
		}
		line, _, _ := strings.Cut(string(data), "\n")
		name, ok := strings.CutPrefix(strings.TrimRight(line, " \t\r"), "ref: refs/heads/")
		if !ok || name == "" || !utf8.ValidString(name) || !text.Is(name) {
			return nil
		}
		return &name
	}
	return nil
}

// notFound reports whether err says there is nothing at the path, or that a
// parent isn't a directory, so the walk goes on up.
func notFound(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	e, ok := fsys.ErrnoOf(err)
	return ok && e == syscall.ENOTDIR
}
