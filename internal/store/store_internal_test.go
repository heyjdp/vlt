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

// interruptedCopy copies s to a new folder, cancelling after the first
// secret is read (a lock in the middle of a move), and returns the folder.
// s's storage is left as it was.
func interruptedCopy(t *testing.T, s *Store) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	orig := s.fs
	s.fs = cancelOnRead{vaultFS: orig, cancel: cancel}
	defer func() { s.fs = orig }()
	dest := filepath.Join(t.TempDir(), "dest")
	if err := s.CopyTo(ctx, dest); !errors.Is(err, context.Canceled) {
		t.Fatalf("CopyTo = %v; want context.Canceled", err)
	}
	return dest
}

// The key file is copied last, so a copy stopped part-way never looks like
// a whole vault: unlocking it says the key is missing instead of showing a
// vault with secrets missing.
func TestStore_CopyTo_AnInterruptedCopyHasNoKeyFile(t *testing.T) {
	t.Parallel()
	dest := interruptedCopy(t, copySource(t))

	if _, err := os.Stat(filepath.Join(dest, keyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an interrupted copy left %s at the destination (stat: %v)", keyFile, err)
	}
	if _, err := Open(context.Background(), dest, internalPass, quiet()); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("Open of an interrupted copy = %v; want ErrKeyMissing", err)
	}
}

// Moving again to where a move was interrupted finishes the copy.
func TestStore_CopyTo_FinishesAnInterruptedCopyOfThisVault(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	dest := interruptedCopy(t, s)

	if err := s.CopyTo(context.Background(), dest); err != nil {
		t.Fatalf("moving again to an interrupted copy: %v", err)
	}
	moved, err := Open(context.Background(), dest, internalPass, quiet())
	if err != nil {
		t.Fatalf("Open the finished copy: %v", err)
	}
	defer moved.Close()
	for _, name := range []string{"a", "b"} {
		if _, err := moved.Get(context.Background(), name, "work"); err != nil {
			t.Fatalf("Get %q from the finished copy: %v", name, err)
		}
	}
}

// A folder without vlt.age whose files this vault did not write (another
// vault's interrupted copy, or a vault that lost its key) is not copied
// over: its secrets could be recovered with a backup of its key.
func TestStore_CopyTo_RefusesAKeylessVaultItDidNotCopy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		secret string // a file planted beside the interrupted copy's
	}{
		{"a secret this vault lacks", "work/other.age"},
		{"a secret this vault holds, with other contents", "work/a.age"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := copySource(t)
			dest := interruptedCopy(t, s)
			planted := filepath.Join(dest, filepath.FromSlash(tt.secret))
			if err := os.WriteFile(planted, []byte("not this vault's"), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := s.CopyTo(context.Background(), dest); !errors.Is(err, ErrVaultExists) {
				t.Fatalf("CopyTo = %v; want ErrVaultExists", err)
			}
			if got, err := os.ReadFile(planted); err != nil || string(got) != "not this vault's" {
				t.Fatalf("the destination's file was overwritten: %q, %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(dest, keyFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused copy wrote %s (stat: %v)", keyFile, err)
			}
		})
	}
}
