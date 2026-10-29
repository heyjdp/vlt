package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// vaultFS is the storage a vault lives on. Every name is a slash-separated
// path relative to the vault directory ("." is the directory itself, and
// "work/github-key.age" a secret), so the store never builds paths for a
// particular operating system or server. Errors wrap fs.ErrNotExist and
// fs.ErrExist where those apply.
//
// Implementations must keep the guarantees the store relies on:
// WriteFileAtomic leaves either the old or the new contents, never a partial
// file; WriteFileExclusive never replaces an existing file; new files are
// mode 0600 and new directories 0700. Methods may be called concurrently.
type vaultFS interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	Stat(name string) (fs.FileInfo, error)
	Remove(name string) error
	// Rename moves oldname to newname, atomically replacing newname.
	Rename(oldname, newname string) error
	// MkdirAll creates a directory and its parents and makes them durable.
	MkdirAll(name string) error
	WriteFileAtomic(name string, data []byte) error
	// WriteFileExclusive writes a new file, failing with an error wrapping
	// fs.ErrExist if name exists.
	WriteFileExclusive(name string, data []byte) error
	// SyncDir makes a preceding rename or removal in a directory durable,
	// where the storage supports it.
	SyncDir(name string) error
	// Local reports whether the storage is this computer's file system.
	Local() bool
	Close() error
}

// openFS returns the storage for a location: the local file system, or an
// SFTP session over the system's ssh client for a remote location.
func openFS(ctx context.Context, loc Location, o options) (vaultFS, error) {
	if !loc.IsRemote() {
		return localFS{root: loc.Path}, nil
	}
	dial := o.dial
	if dial == nil {
		dial = dialSSH
	}
	return newRemoteFS(ctx, loc, dial)
}

// openLocation parses a location string and opens its storage.
func openLocation(ctx context.Context, location string, o options) (vaultFS, Location, error) {
	loc, err := ParseLocation(location)
	if err != nil {
		return nil, Location{}, err
	}
	fsys, err := openFS(ctx, loc, o)
	if err != nil {
		return nil, loc, err
	}
	return fsys, loc, nil
}

// localFS is a vault directory on this computer.
type localFS struct{ root string }

func (l localFS) abs(name string) string { return filepath.Join(l.root, filepath.FromSlash(name)) }

func (l localFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(l.abs(name)) //nolint:wrapcheck // *PathError already names the file
}

func (l localFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(l.abs(name)) //nolint:wrapcheck // *PathError already names the file
}

func (l localFS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(l.abs(name)) //nolint:wrapcheck // *PathError already names the file
}

func (l localFS) Remove(name string) error {
	return os.Remove(l.abs(name)) //nolint:wrapcheck // *PathError already names the file
}

func (l localFS) Rename(oldname, newname string) error {
	if err := os.Rename(l.abs(oldname), l.abs(newname)); err != nil {
		return err //nolint:wrapcheck // *LinkError already names both files
	}
	return syncDir(filepath.Dir(l.abs(newname)))
}

func (l localFS) MkdirAll(name string) error { return mkdirAllSynced(l.abs(name)) }

func (l localFS) WriteFileAtomic(name string, data []byte) error {
	return writeFileAtomic(l.abs(name), data)
}

func (l localFS) WriteFileExclusive(name string, data []byte) error {
	return writeFileExclusive(l.abs(name), data)
}

func (l localFS) SyncDir(name string) error { return syncDir(l.abs(name)) }

func (localFS) Local() bool  { return true }
func (localFS) Close() error { return nil }

// String names the directory in messages.
func (l localFS) String() string { return l.root }

// check fails if fsys is not a vaultFS (compile-time).
var _ vaultFS = localFS{}

// errorf wraps err with the operation and the name it was applied to.
func errorf(op, name string, err error) error {
	return fmt.Errorf("%s %s: %w", op, name, err)
}
