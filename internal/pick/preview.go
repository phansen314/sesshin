package pick

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/fsys"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/placement/kitty"
)

// preview is the text of a session's preview (picker-spec.md, Preview), one
// detail per line. Every value is scrubbed: a title can't pass for another
// line.
func preview(v ops.SessionView, now time.Time) string {
	str := func(s *string) string {
		if s == nil {
			return none
		}
		return scrub(*s)
	}
	id := none
	if v.ID != nil {
		id = fmt.Sprintf("#%d", *v.ID)
	}
	ended := none
	if v.EndedAt != nil {
		ended = string(*v.EndedAt)
	}
	cost := none
	if v.Metrics != nil && v.Metrics.CostUSD != nil {
		cost = fmt.Sprintf("$%.2f", *v.Metrics.CostUSD)
	}
	transcript := str(v.TranscriptPath)
	switch {
	case v.TranscriptExists == nil:
	case *v.TranscriptExists:
		transcript += " (exists)"
	default:
		transcript += " (" + transcriptGone + ")"
	}
	tab := none
	if title, _, ok := kitty.Stored(v.Placement); ok && title != "" {
		tab = scrub(title)
	}
	rows := [][2]string{
		{"sesshin ID", id},
		{"name", scrub(v.Name)},
		{"job", str(v.Job)},
		{"cwd", str(v.Cwd)},
		{"git_branch", str(v.GitBranch)},
		{"model", str(v.Model)},
		{"permission_mode", str(v.PermissionMode)},
		{"started_at", string(v.StartedAt)},
		{"last seen", fmt.Sprintf("%s (%s)", v.LastSeen, seen(v, now))},
		{"ended_at", ended},
		{"end_reason", str(v.EndReason)},
		{"compactions", fmt.Sprint(v.Compactions)},
		{"cost", cost},
		{"transcript", transcript},
		{"tab title", tab},
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%-16s %s\n", r[0]+":", r[1])
	}
	return b.String()
}

// previewDir is a private directory holding one preview file per candidate,
// named by its key. It is removed when the picker is done.
type previewDir struct {
	Path string
	base fsys.Root
	name string
}

// tempBase is where the preview directory is made: $XDG_RUNTIME_DIR if set,
// else the system temp directory ($TMPDIR, else /tmp, as os.TempDir has it
// on Unix); absolute, since fzf runs the preview from anywhere.
func tempBase(getenv func(string) string) (string, error) {
	base := getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = getenv("TMPDIR")
	}
	if base == "" {
		base = "/tmp"
	}
	return filepath.Abs(base)
}

// newPreviewDir makes the directory, mode 0700, and writes each view's
// preview into it, by session ID.
func newPreviewDir(fsy fsys.FS, base string, views []ops.SessionView, now time.Time) (*previewDir, *ops.Error) {
	b, err := fsy.OpenRoot(base)
	if err != nil {
		return nil, ops.IOError(base, err)
	}
	var name string
	for range 10 { // a name taken is a collision, or someone guessing
		var rnd [8]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			panic(err) // crypto/rand does not fail on the platforms sesshin supports
		}
		name = "sesshin-restart-" + hex.EncodeToString(rnd[:])
		if err = b.Mkdir(name, fsys.DirMode); !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		b.Close()
		return nil, ops.IOError(filepath.Join(base, name), err)
	}
	d := &previewDir{Path: filepath.Join(base, name), base: b, name: name}
	root, err := fsy.OpenRoot(d.Path)
	if err != nil {
		d.Remove()
		return nil, ops.IOError(d.Path, err)
	}
	defer root.Close()
	for _, v := range views {
		if err := fsys.Publish(root, v.SessionID, []byte(preview(v, now))); err != nil {
			d.Remove()
			return nil, ops.IOError(filepath.Join(d.Path, v.SessionID), err)
		}
	}
	return d, nil
}

// Remove removes the directory and its files. A failure is ignored: it is
// under the runtime or temp directory, which the system clears.
func (d *previewDir) Remove() {
	_ = d.base.RemoveAll(d.name)
	_ = d.base.Close()
}
