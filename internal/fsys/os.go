package fsys

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// OS is the real filesystem.
type OS struct{}

var _ FS = OS{}

// OpenRoot on a path that leads to a non-directory fails with ENOTDIR.
// os.OpenRoot opens the path without O_DIRECTORY and checks its type only
// afterwards, so a FIFO would block the open, and a regular file is reported
// by an error with no errno. It is therefore handed p + "/.": resolving that
// requires p to be a directory, so the kernel refuses a FIFO or regular file
// with ENOTDIR before opening anything, with no window for a swap. Error
// paths are restored to p. An empty path, which would become "/.", fails
// with ENOENT as open(2) fails on it.
func (OS) OpenRoot(p string) (Root, error) {
	if p == "" {
		return nil, &os.PathError{Op: "open", Path: p, Err: syscall.ENOENT}
	}
	r, err := os.OpenRoot(p + "/.")
	if err != nil {
		return nil, restorePath(err, p)
	}
	return &osRoot{r: r, name: p}, nil
}

func (OS) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
func (OS) Stat(p string) (fs.FileInfo, error)        { return os.Stat(p) }
func (OS) Lstat(p string) (fs.FileInfo, error)       { return os.Lstat(p) }
func (OS) Readlink(p string) (string, error)         { return os.Readlink(p) }

func (OS) ReadFile(p string) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readOpened(f)
}

func restorePath(err error, p string) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		pe.Path = p
	}
	return err
}

type osRoot struct {
	r    *os.Root
	name string
}

func (r *osRoot) Name() string                              { return r.name }
func (r *osRoot) Stat(name string) (fs.FileInfo, error)     { return r.r.Stat(name) }
func (r *osRoot) Lstat(name string) (fs.FileInfo, error)    { return r.r.Lstat(name) }
func (r *osRoot) Mkdir(name string, perm fs.FileMode) error { return r.r.Mkdir(name, perm) }
func (r *osRoot) Rename(oldname, newname string) error      { return r.r.Rename(oldname, newname) }
func (r *osRoot) Remove(name string) error                  { return r.r.Remove(name) }
func (r *osRoot) RemoveAll(name string) error               { return r.r.RemoveAll(name) }
func (r *osRoot) Close() error                              { return r.r.Close() }

// OpenRoot appends "/." to name for the reason OS.OpenRoot does: os.Root's
// OpenRoot opens the last component without O_DIRECTORY, so a FIFO would
// block it.
func (r *osRoot) OpenRoot(name string) (Root, error) {
	sub, err := r.r.OpenRoot(name + "/.")
	if err != nil {
		return nil, restorePath(err, name)
	}
	return &osRoot{r: sub, name: filepath.Join(r.name, name)}, nil
}

// ReadFile opens with O_NONBLOCK, so a FIFO can't block the open, and reads
// anything that is neither a regular file nor a directory as empty, without
// reading it: a FIFO with a writer would block the read, and a device may
// never end. Symlinks are followed (implementation-spec.md, Roots).
func (r *osRoot) ReadFile(name string) ([]byte, error) {
	f, err := r.r.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readOpened(f)
}

// MaxRead bounds every read: sesshin's largest file is a few kilobytes, and a
// hook must not read gigabytes left by something else (implementation-spec.md,
// Roots).
const MaxRead = 16 << 20

// readOpened reads f whole, sized by its fstat; a directory fails with
// EISDIR when read. A file past MaxRead, by its fstat or by what is read,
// fails with EFBIG.
func readOpened(f *os.File) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() && !fi.IsDir() {
		return []byte{}, nil
	}
	if fi.Size() > MaxRead {
		return nil, &os.PathError{Op: "read", Path: f.Name(), Err: syscall.EFBIG}
	}
	// One byte more than the size, so a file that grew is read to its end
	// without a second allocation.
	b := make([]byte, 0, fi.Size()+1)
	for {
		n, err := f.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
		if err == io.EOF {
			return b, nil
		}
		if err != nil {
			return nil, err
		}
		if len(b) > MaxRead {
			return nil, &os.PathError{Op: "read", Path: f.Name(), Err: syscall.EFBIG}
		}
		if len(b) == cap(b) {
			b = append(b, 0)[:len(b)]
		}
	}
}

// ReadDir opens with O_DIRECTORY and O_NONBLOCK: a FIFO in the directory's
// place fails with ENOTDIR rather than blocking the open.
func (r *osRoot) ReadDir(name string) ([]fs.DirEntry, error) {
	f, err := r.r.OpenFile(name, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

func (r *osRoot) SyncDir(name string) error {
	d, err := r.r.Open(name)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// TempPrefix begins the name of every temp file sesshin creates, and of every
// directory it renames aside.
const TempPrefix = ".sesshin-tmp-"

// TempName is a fresh temp name in dir: hidden, recognizably sesshin's, and
// random, so two writes never collide. For a temp that is not created by
// CreateTemp: a directory renamed aside.
//
// The 96 random bits come from math/rand/v2, seeded by the runtime: the name
// must be unique, not secret, and O_EXCL catches a collision. Linking
// crypto/rand slowed sesshin-hook's start by about 13µs (implementation-spec.md,
// Writing files).
func TempName(dir string) string {
	var b [12]byte
	binary.LittleEndian.PutUint64(b[:], rand.Uint64())
	binary.LittleEndian.PutUint32(b[8:], rand.Uint32())
	return path.Join(dir, TempPrefix+hex.EncodeToString(b[:]))
}

// OpenAppend opens without O_CREATE first, the common case. Only when that
// finds nothing does it create, exclusively, so it knows the file is its own
// to fchmod; losing that race to another creator opens theirs. The race can
// repeat while the file is rotated away and recreated, so after
// appendTries rounds the last error is returned.
func (r *osRoot) OpenAppend(name string) (AppendFile, error) {
	const flags = os.O_WRONLY | os.O_APPEND | syscall.O_NONBLOCK
	var err error
	for range appendTries {
		var f *os.File
		f, err = r.r.OpenFile(name, flags, 0)
		if err == nil {
			return &osAppend{f: f}, nil
		}
		if !errnoIs(err, syscall.ENOENT) {
			return nil, err
		}
		f, err = r.r.OpenFile(name, flags|os.O_CREATE|os.O_EXCL, FileMode)
		if errnoIs(err, syscall.EEXIST) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := f.Chmod(FileMode); err != nil {
			f.Close()
			return nil, err
		}
		return &osAppend{f: f}, nil
	}
	return nil, err
}

// appendTries bounds OpenAppend's open-or-create rounds.
const appendTries = 3

type osAppend struct {
	f *os.File
}

// Write makes one write(2), retrying only EINTR. os.File's Write would
// wait in the poller on EAGAIN, so a FIFO whose reader never reads would
// block it.
func (a *osAppend) Write(p []byte) (int, error) {
	c, err := a.f.SyscallConn()
	if err != nil {
		return 0, err
	}
	var n int
	var werr error
	if err := c.Write(func(fd uintptr) bool {
		for {
			n, werr = syscall.Write(int(fd), p)
			if werr != syscall.EINTR {
				return true
			}
		}
	}); err != nil {
		return 0, err
	}
	if werr != nil {
		return 0, &os.PathError{Op: "write", Path: a.f.Name(), Err: werr}
	}
	if n < len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}
func (a *osAppend) Stat() (fs.FileInfo, error) { return a.f.Stat() }
func (a *osAppend) TryLock() error             { return tryLock(a.f) }
func (a *osAppend) Close() error               { return a.f.Close() }

// CreateTemp sets the mode with fchmod after creating the file, since the
// umask can take bits from the mode given to open but not from fchmod's.
func (r *osRoot) CreateTemp(dir string) (File, string, error) {
	name := TempName(dir)
	f, err := r.r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, FileMode)
	if err != nil {
		return nil, "", err
	}
	if err := f.Chmod(FileMode); err != nil {
		f.Close()
		r.r.Remove(name)
		return nil, "", err
	}
	return f, name, nil
}
