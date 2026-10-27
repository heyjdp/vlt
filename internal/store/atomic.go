package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// Vault files are private to the user: files 0600, directories 0700.
const (
	filePerm os.FileMode = 0o600
	dirPerm  os.FileMode = 0o700
)

// tmpPrefix marks in-flight writes. Files with this prefix never end in
// ".age", so directory walks that filter on that extension skip them.
const tmpPrefix = ".tmp-"

// writeFileAtomic writes data to path so that readers observe either the old
// contents or the new contents, never a truncated or partial file. It writes
// to a temp file in the same directory, sets mode 0600 explicitly with Chmod (so
// the umask does not apply), fsyncs it, renames it over path, and fsyncs the
// directory so the rename survives a crash (see syncDir for platform limits).
// Atomicity is per file; callers that rewrite several files must order their
// writes so that any prefix of them leaves the vault readable.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)

	f, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return fmt.Errorf("writeFileAtomic: create temp: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("writeFileAtomic: write: %w", err)
	}
	if err = f.Chmod(filePerm); err != nil {
		return fmt.Errorf("writeFileAtomic: chmod: %w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("writeFileAtomic: sync: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("writeFileAtomic: close: %w", err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writeFileAtomic: rename: %w", err)
	}

	if err = syncDir(dir); err != nil {
		return fmt.Errorf("writeFileAtomic: sync dir: %w", err)
	}
	return nil
}

// writeFileExclusive writes data to path atomically, failing with an error
// wrapping os.ErrExist if path already exists. It writes and fsyncs a uniquely
// named temp file, then hard-links it into place; link(2) fails if the target
// exists, so exactly one concurrent caller wins. On filesystems without hard
// links (FAT32, exFAT, some network mounts) it falls back to O_EXCL creation,
// which keeps the no-overwrite guarantee but not crash atomicity.
func writeFileExclusive(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return fmt.Errorf("writeFileExclusive: create temp: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err = f.Write(data); err == nil {
		if err = f.Chmod(filePerm); err == nil {
			err = f.Sync()
		}
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("writeFileExclusive: %w", err)
	}

	linkErr := os.Link(tmp, path)
	switch {
	case linkErr == nil:
	case errors.Is(linkErr, os.ErrExist):
		return fmt.Errorf("writeFileExclusive: %w", linkErr)
	default:
		if err := createExclusive(path, data); err != nil {
			return fmt.Errorf("writeFileExclusive: link unsupported (%w); fallback: %w", linkErr, err)
		}
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("writeFileExclusive: %w", err)
	}
	return nil
}

// createExclusive creates path with O_EXCL and writes data, removing the file
// if the write fails. It is the fallback for filesystems without hard links.
func createExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// syncDir fsyncs a directory so a preceding rename or mkdir is durable.
// Windows does not support fsync on directory handles, and NTFS journals
// renames, so it is a no-op there. Filesystems that reject directory fsync
// (some network and FUSE mounts return EINVAL or ENOTSUP) are treated as
// best-effort rather than failing a write that has already landed.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("syncDir %q: %w", dir, err)
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, errors.ErrUnsupported) {
		syncErr = nil
	}
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("syncDir %q: %w", dir, err)
	}
	return nil
}

// mkdirAllSynced creates dir (and parents) with mode 0700 if it does not exist,
// then fsyncs its parent so the new directory entry survives a crash. Without
// this, the first secret saved into a new group could vanish with its
// directory after a power loss even though the file itself was fsynced.
func mkdirAllSynced(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("mkdirAllSynced: %w", err)
	}
	return syncDir(filepath.Dir(dir))
}
