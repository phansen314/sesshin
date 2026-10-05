package fsys

import "path"

// The modes of everything sesshin creates: its files hold paths and costs
// (hooks-spec.md, Recording an event step 1). CreateTemp sets FileMode
// whatever the umask; directories get DirMode less the umask.
const (
	FileMode = 0o600
	DirMode  = 0o700
)

// Publish writes data to rel, a path within r, through a temp file in rel's
// directory (implementation-spec.md, Writing files): created exclusively,
// written in full, then renamed over rel. Nothing is flushed: only state.json
// is (PublishSynced). Any failure returns the OS error, with nothing
// published and no temp file left.
func Publish(r Root, rel string, data []byte) error {
	return publish(r, rel, data, false)
}

// PublishSynced is Publish for state.json, the one file sesshin flushes
// (design-spec.md, Files): the temp file is flushed (fsync) before it is
// published, and the directory after, so the file survives a system crash
// whole. A failed file flush fails the write, with nothing published; a
// failed directory flush does not, since the file is already published.
func PublishSynced(r Root, rel string, data []byte) error {
	return publish(r, rel, data, true)
}

func publish(r Root, rel string, data []byte, sync bool) error {
	p, err := prepare(r, rel, data, sync)
	if err != nil {
		return err
	}
	if err := p.Commit(); err != nil {
		return err
	}
	if sync {
		r.SyncDir(path.Dir(rel))
	}
	return nil
}

// Prepared is a temp file written in full and closed, not yet published: the
// first step of the statusline's two-step write, which decides between the
// steps whether to publish (hooks-spec.md, statusline step 6). Exactly one
// of Commit and Abort must follow.
type Prepared struct {
	r        Root
	tmp, rel string
}

// Prepare writes data to a temp file in rel's directory, unflushed. A
// failure returns the OS error, with no temp file left.
func Prepare(r Root, rel string, data []byte) (*Prepared, error) {
	return prepare(r, rel, data, false)
}

func prepare(r Root, rel string, data []byte, sync bool) (*Prepared, error) {
	f, tmp, err := r.CreateTemp(path.Dir(rel))
	if err != nil {
		return nil, err
	}
	_, err = f.Write(data)
	if err == nil && sync {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		r.Remove(tmp)
		return nil, err
	}
	return &Prepared{r: r, tmp: tmp, rel: rel}, nil
}

// Commit renames the temp file over rel. A failure returns the OS error,
// with the temp file removed and rel as it was.
func (p *Prepared) Commit() error {
	if err := p.r.Rename(p.tmp, p.rel); err != nil {
		p.r.Remove(p.tmp)
		return err
	}
	return nil
}

// Abort removes the temp file. A failure leaves it behind, a leftover reads
// ignore (design-spec.md, Files), and is not reported.
func (p *Prepared) Abort() {
	p.r.Remove(p.tmp)
}

// RemoveAside removes the directory name, within r, in one step as far as
// readers are concerned: it is renamed to a temp name beside it, then removed
// with everything under it (implementation-spec.md, Writing files: prune).
// Only the rename can fail, with the OS error and nothing changed. Once
// renamed, the directory is gone; a removal that fails or is interrupted
// leaves a hidden leftover, which reads ignore.
func RemoveAside(r Root, name string) error {
	tmp := TempName(path.Dir(name))
	if err := r.Rename(name, tmp); err != nil {
		return err
	}
	r.RemoveAll(tmp)
	return nil
}
