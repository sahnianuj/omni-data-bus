package directory

import (
	"io"
	"io/fs"
	"os"
)

// fsys is the part of the file system the writer uses; tests inject faults through it.
type fsys interface {
	MkdirAll(path string, perm fs.FileMode) error
	// Create opens a new file (O_CREATE|O_EXCL|O_WRONLY).
	Create(name string, perm fs.FileMode) (file, error)
	Rename(oldpath, newpath string) error
	Link(oldpath, newpath string) error
	Remove(name string) error
	Lstat(name string) (fs.FileInfo, error)
	SyncDir(dir string) error
}

type file interface {
	io.Writer
	Sync() error
	Close() error
}

type osFS struct{}

func (osFS) MkdirAll(p string, m fs.FileMode) error { return os.MkdirAll(p, m) }
func (osFS) Create(name string, m fs.FileMode) (file, error) {
	return os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, m) //nolint:gosec // inside the allowed target folder
}
func (osFS) Rename(o, n string) error            { return os.Rename(o, n) }
func (osFS) Link(o, n string) error              { return os.Link(o, n) }
func (osFS) Remove(n string) error               { return os.Remove(n) }
func (osFS) Lstat(n string) (fs.FileInfo, error) { return os.Lstat(n) }
func (osFS) SyncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // inside the allowed target folder
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
