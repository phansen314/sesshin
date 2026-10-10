package fsys

import (
	"errors"
	"io"
	"io/fs"
	"time"
)

// FS opens roots, and makes the few calls that happen outside one: creating
// a session directory and its missing parents, reading hooks.properties and
// config.toml, reading the process table, the statusline's walk up to the
// repository, and ops' stat of a transcript.
type FS interface {
	// OpenRoot opens the directory at path, following symlinks. Every later
	// call through the Root is relative to this one resolution.
	OpenRoot(path string) (Root, error)
	// MkdirAll creates a directory and any missing parents.
	MkdirAll(path string, perm fs.FileMode) error
	// ReadFile reads a file outside any root, following symlinks, as
	// Root.ReadFile does.
	ReadFile(path string) ([]byte, error)
	// Stat follows symlinks.
	Stat(path string) (fs.FileInfo, error)
	// Lstat is Stat that does not follow a symlink in the last component.
	Lstat(path string) (fs.FileInfo, error)
	// Readlink returns a symlink's target: /proc/<pid>/exe, for the
	// process lookup (design-spec.md, Liveness).
	Readlink(path string) (string, error)
}

// Root is a directory opened once, through which every call is made. Names
// are slash-separated and relative to the root; "." is the root itself.
//
// Symlinks inside the root are followed, in every component: the state
// directory is sesshin's own (implementation-spec.md, Roots). A name that
// escapes the root — through "..", or a symlink that leads outside — fails
// with os.Root's error, which holds no errno.
type Root interface {
	// Name is the path the root was opened with.
	Name() string
	// OpenRoot opens the directory name, inside this root, as a root of its
	// own, named Name()/name. A non-directory fails with ENOTDIR.
	OpenRoot(name string) (Root, error)
	Stat(name string) (fs.FileInfo, error)
	// Lstat is Stat that does not follow a symlink in the last component.
	Lstat(name string) (fs.FileInfo, error)
	// ReadFile reads a whole file. One past MaxRead fails with EFBIG,
	// without being read past it. A directory fails with EISDIR; a FIFO,
	// socket, or device reads as empty, without blocking.
	ReadFile(name string) ([]byte, error)
	// ReadDir lists a directory in the order the OS gives; callers sort.
	ReadDir(name string) ([]fs.DirEntry, error)
	Mkdir(name string, perm fs.FileMode) error
	// CreateTemp creates a new, empty temp file in dir, exclusively, with
	// mode FileMode whatever the umask. Its name, .sesshin-tmp-<random>, is
	// hidden from reads and recognizably sesshin's. The returned name includes
	// dir.
	CreateTemp(dir string) (f File, name string, err error)
	// Rename publishes a file over an existing one, or moves a directory.
	Rename(oldname, newname string) error
	// OpenAppend opens name for appending, creating it with mode FileMode,
	// whatever the umask, when it is missing. A FIFO in its place fails with
	// ENXIO rather than blocking the open (no reader) or a write (one that
	// never reads).
	OpenAppend(name string) (AppendFile, error)
	// SyncDir flushes a directory's entries to disk (fsync).
	SyncDir(name string) error
	Remove(name string) error
	// RemoveAll removes a directory and everything under it, never following
	// a symlink; a name that is already gone is not an error.
	RemoveAll(name string) error
	// Lock takes the lock on the root directory itself: flock(LOCK_EX), never
	// blocking. It tries at once, then polls with a backoff until wait has
	// passed on the monotonic clock; wait 0 is a single try. A lock still
	// held at the end fails with EAGAIN. EINTR is retried.
	Lock(wait time.Duration) (Lock, error)
	// Moved reports whether Name no longer leads to the directory this root
	// opened: it was renamed aside or removed, and maybe replaced. Compared by
	// device and inode, after locking (implementation-spec.md, Roots).
	Moved() (bool, error)
	Close() error
}

// File is a temp file being written.
type File interface {
	io.Writer
	// Sync flushes what was written to disk (fsync).
	Sync() error
	Close() error
}

// AppendFile is a file opened with O_APPEND: each Write lands whole at the
// file's end, however many processes append at once.
type AppendFile interface {
	io.Writer
	// Stat is fstat: the file this descriptor opened, wherever it is now.
	Stat() (fs.FileInfo, error)
	// TryLock makes one flock(LOCK_EX|LOCK_NB) on this descriptor, retrying
	// EINTR; held elsewhere, it fails with EAGAIN. Close releases it.
	TryLock() error
	Close() error
}

// Lock is a held lock. It keeps the locked descriptor referenced until
// Unlock, which closes it and so releases the lock.
type Lock interface {
	Unlock() error
}

// OpenRootCreate opens the directory at dir, creating it, mode DirMode, and
// its missing parents when it does not exist.
func OpenRootCreate(fsy FS, dir string) (Root, error) {
	root, err := fsy.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err = fsy.MkdirAll(dir, DirMode); err == nil {
			root, err = fsy.OpenRoot(dir)
		}
	}
	return root, err
}
