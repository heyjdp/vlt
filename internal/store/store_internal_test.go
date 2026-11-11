package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// cancelOnRead cancels a context once a secret file has been read.
type cancelOnRead struct {
	vaultFS
	cancel context.CancelFunc
}

func (f cancelOnRead) ReadFile(name string) ([]byte, error) {
	data, err := f.vaultFS.ReadFile(name)
	if strings.HasSuffix(name, ".age") && name != keyFile {
		f.cancel()
	}
	return data, err
}

// copySource returns an open vault holding two secrets in group "work".
func copySource(t *testing.T) *Store {
	t.Helper()
	s := openedVault(t)
	for _, name := range []string{"a", "b"} {
		if err := s.Create(context.Background(), &Secret{Type: TypePassword, Name: name, Group: "work", Value: "pw"}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// A group folder that can't be listed fails the copy rather than leave
// that group's secrets behind.
func TestStore_CopyTo_FailsWhenAGroupFolderCannotBeListed(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	s.fs = groupReadFails{vaultFS: s.fs, group: "work", err: ErrConnectionLost}

	err := s.CopyTo(context.Background(), filepath.Join(t.TempDir(), "dest"))

	if !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("CopyTo = %v; want the listing's error", err)
	}
}

// Cancelling (a lock) during the copy stops it before the next file.
func TestStore_CopyTo_StopsWhenCancelledMidCopy(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.fs = cancelOnRead{vaultFS: s.fs, cancel: cancel}
	dest := filepath.Join(t.TempDir(), "dest")

	err := s.CopyTo(ctx, dest)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CopyTo = %v; want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "work", "b.age")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file after the cancel was copied (stat: %v)", err)
	}
}

// A file where a group folder belongs at the destination fails the copy
// rather than drop that group's secrets.
func TestStore_CopyTo_FailsWhenAGroupFolderIsAFileAtTheDestination(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports a different error for a file used as a folder")
	}
	s := copySource(t)
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "work"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := s.CopyTo(context.Background(), dest)

	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("CopyTo = %v; want it to fail on the file in the folder's place", err)
	}
}

// A secret file that can't be written at the destination fails the copy.
func TestStore_CopyTo_FailsWhenASecretFileCannotBeWritten(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not restrict this user")
	}
	s := copySource(t)
	dest := t.TempDir()
	group := filepath.Join(dest, "work")
	if err := os.Mkdir(group, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(group, 0o700) })

	err := s.CopyTo(context.Background(), dest)

	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("CopyTo = %v; want permission denied writing the secret", err)
	}
}
