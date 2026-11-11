package store

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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

			err := s.CopyTo(context.Background(), dest)
			if !errors.Is(err, ErrVaultExists) || errors.Is(err, ErrStaleCopy) {
				t.Fatalf("CopyTo = %v; want ErrVaultExists, not ErrStaleCopy", err)
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

// keyRace plants a vault key file just before the copy writes its own: as
// if another instance created a vault at the destination meanwhile.
type keyRace struct{ vaultFS }

func (f keyRace) WriteFileExclusive(name string, data []byte) error {
	if err := f.plant(name); err != nil {
		return err
	}
	return f.vaultFS.WriteFileExclusive(name, data)
}

func (f keyRace) WriteFileAtomic(name string, data []byte) error {
	if err := f.plant(name); err != nil {
		return err
	}
	return f.vaultFS.WriteFileAtomic(name, data)
}

// plant writes another vault's key file if name is the key file.
func (f keyRace) plant(name string) error {
	if name != keyFile {
		return nil
	}
	return f.vaultFS.WriteFileAtomic(keyFile, []byte("another vault's key"))
}

// A vault created at the destination while the copy ran keeps its key file:
// the copy is refused rather than overwrite it.
func TestStore_CopyTo_DoesNotReplaceAKeyFileWrittenMeanwhile(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	dest := filepath.Join(t.TempDir(), "dest")

	err := s.copyTo(context.Background(), keyRace{localFS{root: dest}})

	if !errors.Is(err, ErrVaultExists) {
		t.Fatalf("copyTo = %v; want ErrVaultExists", err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, keyFile)); err != nil || string(got) != "another vault's key" {
		t.Fatalf("the other vault's key file was replaced: %q, %v", got, err)
	}
}

// recordingFS records the files read from and written to it, and cancels
// (if cancel is set) once a secret file has been read.
type recordingFS struct {
	vaultFS
	cancel context.CancelFunc
	mu     sync.Mutex
	reads  []string
	writes []string
}

func (f *recordingFS) ReadFile(name string) ([]byte, error) {
	f.mu.Lock()
	f.reads = append(f.reads, name)
	f.mu.Unlock()
	if f.cancel != nil && path.Dir(name) != "." {
		f.cancel()
	}
	return f.vaultFS.ReadFile(name)
}

func (f *recordingFS) WriteFileAtomic(name string, data []byte) error {
	f.wrote(name)
	return f.vaultFS.WriteFileAtomic(name, data)
}

func (f *recordingFS) WriteFileExclusive(name string, data []byte) error {
	f.wrote(name)
	return f.vaultFS.WriteFileExclusive(name, data)
}

func (f *recordingFS) wrote(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, name)
}

// copiedUpToTheKey copies s to a new folder, cancelling after its last
// secret is read, so every secret but not vlt.age is copied; it returns
// the folder.
func copiedUpToTheKey(t *testing.T, s *Store) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	orig := s.fs
	s.fs = cancelOnReadOf{vaultFS: orig, name: "work/b.age", cancel: cancel}
	defer func() { s.fs = orig }()
	dest := filepath.Join(t.TempDir(), "dest")
	if err := s.CopyTo(ctx, dest); !errors.Is(err, context.Canceled) {
		t.Fatalf("CopyTo = %v; want context.Canceled", err)
	}
	return dest
}

// cancelOnReadOf cancels a context once the file name has been read.
type cancelOnReadOf struct {
	vaultFS
	name   string
	cancel context.CancelFunc
}

func (f cancelOnReadOf) ReadFile(name string) ([]byte, error) {
	data, err := f.vaultFS.ReadFile(name)
	if name == f.name {
		f.cancel()
	}
	return data, err
}

// A cancel after the last secret is copied still stops the copy before
// vlt.age, so the copy does not look whole; moving again finishes it.
func TestStore_CopyTo_ACancelAfterTheLastSecretLeavesOutTheKey(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	dest := copiedUpToTheKey(t, s)

	if _, err := os.Stat(filepath.Join(dest, "work", "b.age")); err != nil {
		t.Fatalf("the last secret was not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, keyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a copy cancelled before vlt.age wrote it (stat: %v)", err)
	}
	if err := s.CopyTo(context.Background(), dest); err != nil {
		t.Fatalf("moving again: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, keyFile)); err != nil {
		t.Fatalf("the finished copy has no %s: %v", keyFile, err)
	}
}

// Cancelling (a lock) while an unfinished copy is compared with the vault
// stops the comparison: over a slow connection it reads every file.
func TestStore_CopyTo_StopsWhenCancelledWhileComparing(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	dest := copiedUpToTheKey(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dst := &recordingFS{vaultFS: localFS{root: dest}, cancel: cancel}

	err := s.copyTo(ctx, dst)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copyTo = %v; want context.Canceled", err)
	}
	var secrets []string
	for _, name := range dst.reads {
		if path.Dir(name) != "." {
			secrets = append(secrets, name)
		}
	}
	if len(secrets) != 1 {
		t.Fatalf("read %v at the destination after the cancel; want only the first secret", secrets)
	}
	if len(dst.writes) != 0 {
		t.Fatalf("a cancelled copy wrote %v", dst.writes)
	}
}

// Finishing an unfinished copy writes only what it lacks: over a slow
// connection, writing files already there again would double the time.
func TestStore_CopyTo_FinishingACopyDoesNotRewriteFilesAlreadyThere(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	dest := interruptedCopy(t, s) // holds the marker and work/a.age
	dst := &recordingFS{vaultFS: localFS{root: dest}}

	if err := s.copyTo(context.Background(), dst); err != nil {
		t.Fatalf("copyTo: %v", err)
	}

	if want := []string{"work/b.age", keyFile}; !slices.Equal(dst.writes, want) {
		t.Fatalf("finishing the copy wrote %v; want only %v", dst.writes, want)
	}
}

// An unfinished copy that lacks the format marker (lost with the
// connection, say) is finished, marker included.
func TestStore_CopyTo_FinishesACopyWithoutAFormatMarker(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	dest := interruptedCopy(t, s)
	if err := os.Remove(filepath.Join(dest, formatFile)); err != nil {
		t.Fatal(err)
	}

	if err := s.CopyTo(context.Background(), dest); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}
	moved, err := Open(context.Background(), dest, internalPass, quiet())
	if err != nil {
		t.Fatalf("Open the finished copy: %v", err)
	}
	moved.Close()
}

// A folder whose format marker is not this vault's is not this vault's
// unfinished copy, even if its secret files are.
func TestStore_CopyTo_RefusesACopyWithAnotherFormatMarker(t *testing.T) {
	t.Parallel()
	s := copySource(t)
	dest := interruptedCopy(t, s)
	marker := filepath.Join(dest, formatFile)
	if err := os.WriteFile(marker, []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.CopyTo(context.Background(), dest); !errors.Is(err, ErrVaultExists) {
		t.Fatalf("CopyTo = %v; want ErrVaultExists", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != `{"version":99}` {
		t.Fatalf("the destination's marker was overwritten: %q, %v", got, err)
	}
}

// Moving again after the vault changed since a move was cut short is
// refused (a file there would be overwritten or left behind), and the error
// says why: the folder holds an outdated copy of this vault, which can go.
func TestStore_CopyTo_RefusesAnUnfinishedCopyTheVaultChangedSince(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(ctx context.Context, s *Store) error
	}{
		{"a copied secret changed", func(ctx context.Context, s *Store) error {
			return s.Update(ctx, &Secret{Type: TypePassword, Name: "a", Group: "work", Value: "new pw"})
		}},
		{"a copied secret deleted", func(ctx context.Context, s *Store) error {
			return s.Delete(ctx, "a", "work")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := copySource(t)
			dest := interruptedCopy(t, s)
			if err := tt.change(context.Background(), s); err != nil {
				t.Fatal(err)
			}

			err := s.CopyTo(context.Background(), dest)

			if !errors.Is(err, ErrStaleCopy) || !errors.Is(err, ErrVaultExists) {
				t.Fatalf("CopyTo = %v; want ErrStaleCopy, an ErrVaultExists", err)
			}
			if _, err := os.Stat(filepath.Join(dest, keyFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused copy wrote %s (stat: %v)", keyFile, err)
			}
		})
	}
}
