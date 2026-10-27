package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yourorg/vlt/internal/store"
	"github.com/yourorg/vlt/internal/testutil"
)

const testPass = "correct-horse-battery-staple"

// testWorkFactor keeps scrypt cheap (2^10, ~1 MiB) so parallel tests do not
// need gigabytes of memory; the production default is 2^18.
const testWorkFactor = 10

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func createVault(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Create(dir, testPass, quietLog(), store.WithWorkFactor(testWorkFactor))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func openVault(t *testing.T, dir, pass string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), dir, pass, quietLog(), store.WithWorkFactor(testWorkFactor))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// countScryptStanzas counts age scrypt recipient stanzas across every file
// under dir. Each one costs a scrypt derivation to decrypt.
func countScryptStanzas(t *testing.T, dir string) (total int, files []string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".age" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if n := bytes.Count(data, []byte("\n-> scrypt ")); n > 0 {
			total += n
			files = append(files, filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return total, files
}

func TestStore_CreateOpen_RoundTrip(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "vault")
	s := createVault(t, dir)
	if err := s.Create(context.Background(), testutil.PasswordSecret("gh", "ghp_x", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s.Close()

	reopened := openVault(t, dir, testPass)
	got, err := reopened.Get(context.Background(), "gh", "work")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != "ghp_x" {
		t.Fatalf("value mismatch: got %q", got.Value)
	}
}

func TestStore_Unlock_RunsExactlyOneScrypt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := createVault(t, dir)
	for i := range 100 {
		sec := testutil.PasswordSecret(fmt.Sprintf("secret-%03d", i), "v", fmt.Sprintf("g%d", i%5))
		if err := s.Create(context.Background(), sec); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	n, files := countScryptStanzas(t, dir)
	if n != 1 || len(files) != 1 || files[0] != "vlt.age" {
		t.Fatalf("expected exactly one scrypt stanza, in vlt.age; got %d in %v", n, files)
	}

	listed, err := openVault(t, dir, testPass).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 100 {
		t.Fatalf("expected 100 secrets, got %d", len(listed))
	}
}

func TestStore_Open_ReturnsErrWrongPassphrase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createVault(t, dir).Close()

	_, err := store.Open(context.Background(), dir, "wrong-passphrase", quietLog())
	if !errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase, got %v", err)
	}
}

func TestStore_Open_ReturnsErrVaultNotFound(t *testing.T) {
	t.Parallel()

	_, err := store.Open(context.Background(), t.TempDir(), testPass, quietLog())
	if !errors.Is(err, store.ErrVaultNotFound) {
		t.Fatalf("expected ErrVaultNotFound, got %v", err)
	}
}

func TestStore_Create_RefusesExistingVault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createVault(t, dir)

	_, err := store.Create(dir, "another-passphrase", quietLog())
	if !errors.Is(err, store.ErrVaultExists) {
		t.Fatalf("expected ErrVaultExists, got %v", err)
	}
}

func TestStore_Create_WritesFormatMarker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createVault(t, dir)

	data, err := os.ReadFile(filepath.Join(dir, "vlt.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(data, []byte(`"format":2`)) {
		t.Fatalf("unexpected format marker: %s", data)
	}
}

func mustExist(t *testing.T, dir string) bool {
	t.Helper()
	ok, err := store.Exists(dir)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	return ok
}

func TestStore_Exists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if mustExist(t, dir) {
		t.Fatal("empty directory should not be a vault")
	}
	if mustExist(t, filepath.Join(dir, "missing")) {
		t.Fatal("missing directory should not be a vault")
	}
	createVault(t, dir)
	if !mustExist(t, dir) {
		t.Fatal("created vault should exist")
	}
}

func TestStore_Exists_ReportsUnreadableGroupInsteadOfNoVault(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission semantics need a non-root Unix user")
	}

	dir := t.TempDir()
	locked := filepath.Join(dir, "work")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	if _, err := store.Exists(dir); err == nil {
		t.Fatal("expected an error when a group directory cannot be read")
	}
	if _, err := store.Create(dir, testPass, quietLog(), store.WithWorkFactor(testWorkFactor)); err == nil {
		t.Fatal("Create must refuse a directory it cannot fully inspect")
	}
}

func TestStore_Create_RefusesDirectoryWithOnlySecretFiles(t *testing.T) {
	t.Parallel()

	src := createVault(t, t.TempDir())
	if err := src.Create(context.Background(), testutil.PasswordSecret("a", "1", "work")); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "work", "orphan.age"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := src.CopyTo(context.Background(), dest); !errors.Is(err, store.ErrVaultExists) {
		t.Fatalf("CopyTo onto loose secret files: expected ErrVaultExists, got %v", err)
	}
}

func TestStore_ChangePassphrase_RewritesOnlyKeyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := createVault(t, dir)
	ctx := context.Background()
	if err := s.Create(ctx, testutil.PasswordSecret("a", "1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	secretPath := filepath.Join(dir, "work", "a.age")
	before, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if err := s.ChangePassphrase(context.Background(), testPass, "new-passphrase"); err != nil {
		t.Fatalf("ChangePassphrase: %v", err)
	}

	after, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("secret file was rewritten by a passphrase change")
	}
	if _, err := store.Open(ctx, dir, testPass, quietLog()); !errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("old passphrase should no longer open the vault, got %v", err)
	}
	got, err := openVault(t, dir, "new-passphrase").Get(ctx, "a", "work")
	if err != nil {
		t.Fatalf("Get after change: %v", err)
	}
	if got.Value != "1" {
		t.Fatalf("value mismatch: got %q", got.Value)
	}
}

func TestStore_ChangePassphrase_RejectsWrongCurrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := createVault(t, dir)
	keyBefore, err := os.ReadFile(filepath.Join(dir, "vlt.age"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	err = s.ChangePassphrase(context.Background(), "not-the-passphrase", "new-passphrase")
	if !errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase, got %v", err)
	}
	keyAfter, err := os.ReadFile(filepath.Join(dir, "vlt.age"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("vlt.age changed despite wrong current passphrase")
	}
}

func TestStore_ChangePassphrase_FailedWriteKeepsOldPassphrase(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only directory semantics need a non-root Unix user")
	}

	dir := t.TempDir()
	s := createVault(t, dir)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := s.ChangePassphrase(context.Background(), testPass, "new-passphrase"); err == nil {
		t.Fatal("expected ChangePassphrase to fail in a read-only vault")
	}
	openVault(t, dir, testPass)
}

func TestStore_CopyTo_CopiesAllSecrets(t *testing.T) {
	t.Parallel()

	s := createVault(t, t.TempDir())
	ctx := context.Background()
	if err := s.Create(ctx, testutil.PasswordSecret("copy-1", "v1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Create(ctx, testutil.RecordSecret("copy-2", "u", "p", "https://example.com", "personal")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	destDir := filepath.Join(t.TempDir(), "moved")
	if err := s.CopyTo(context.Background(), destDir); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}

	dest := openVault(t, destDir, testPass)
	got, err := dest.Get(ctx, "copy-1", "work")
	if err != nil {
		t.Fatalf("Get from dest: %v", err)
	}
	if got.Value != "v1" {
		t.Fatalf("value mismatch: got %q", got.Value)
	}
	if _, err := os.Stat(filepath.Join(destDir, "vlt.json")); err != nil {
		t.Fatalf("format marker not copied: %v", err)
	}
}

func TestStore_Close_ZeroesKey(t *testing.T) {
	t.Parallel()

	s := createVault(t, t.TempDir())
	s.Close()

	if err := s.Create(context.Background(), testutil.PasswordSecret("a", "1", "")); err == nil {
		t.Fatal("expected Save to fail after Close")
	}
}

func TestStore_Open_CorruptKeyFileIsNotReportedAsWrongPassphrase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createVault(t, dir).Close()
	if err := os.WriteFile(filepath.Join(dir, "vlt.age"), []byte("age-encryption.org/v1\ngarbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := store.Open(context.Background(), dir, testPass, quietLog())
	if err == nil || errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("expected a corruption error, got %v", err)
	}
}

func TestStore_Open_RejectsUnrecognisedKeyFileContents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createVault(t, dir).Close()
	bogus := testutil.EncryptWithPassphrase(t, testPass, []byte("not a key"))
	if err := os.WriteFile(filepath.Join(dir, "vlt.age"), bogus, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := store.Open(context.Background(), dir, testPass, quietLog())
	if err == nil || errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("expected an unrecognised-contents error, got %v", err)
	}
}

func TestStore_Open_RestoresMissingFormatMarker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createVault(t, dir).Close()
	if err := os.Remove(filepath.Join(dir, "vlt.json")); err != nil {
		t.Fatal(err)
	}

	openVault(t, dir, testPass)
	if !fileExists(filepath.Join(dir, "vlt.json")) {
		t.Fatal("format marker not restored")
	}
}

func TestStore_Open_RejectsNewerFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createVault(t, dir).Close()
	if err := os.WriteFile(filepath.Join(dir, "vlt.json"), []byte(`{"format":3}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := store.Open(context.Background(), dir, testPass, quietLog())
	if !errors.Is(err, store.ErrUnsupportedFormat) {
		t.Fatalf("expected ErrUnsupportedFormat, got %v", err)
	}
}

func TestStore_Open_ReportsMissingKeyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := createVault(t, dir)
	if err := s.Create(context.Background(), testutil.PasswordSecret("a", "1", "work")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "vlt.age")); err != nil {
		t.Fatal(err)
	}

	_, err := store.Open(context.Background(), dir, testPass, quietLog())
	if !errors.Is(err, store.ErrKeyMissing) {
		t.Fatalf("expected ErrKeyMissing, got %v", err)
	}
}

// A partial backup without vlt.age and vlt.json looks like a format-1 vault,
// but its secrets are encrypted to the lost vault key: report the missing
// key rather than a wrong passphrase.
func TestStore_Open_ReportsMissingKeyWhenTheFormatMarkerIsGoneToo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := createVault(t, dir)
	if err := s.Create(context.Background(), testutil.PasswordSecret("a", "1", "work")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vlt.age", "vlt.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	_, err := store.Open(context.Background(), dir, testPass, quietLog(), store.WithWorkFactor(testWorkFactor))
	if !errors.Is(err, store.ErrKeyMissing) {
		t.Fatalf("expected ErrKeyMissing, got %v", err)
	}
}

func TestStore_ChangePassphrase_RejectsForeignKeyFile(t *testing.T) {
	t.Parallel()

	dirA, dirB := t.TempDir(), t.TempDir()
	a := createVault(t, dirA)
	createVault(t, dirB).Close()
	foreign, err := os.ReadFile(filepath.Join(dirB, "vlt.age"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "vlt.age"), foreign, 0o600); err != nil {
		t.Fatal(err)
	}

	err = a.ChangePassphrase(context.Background(), testPass, "new-passphrase")
	if err == nil {
		t.Fatal("expected ChangePassphrase to refuse a vlt.age holding another vault's key")
	}
	after, err := os.ReadFile(filepath.Join(dirA, "vlt.age"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(foreign, after) {
		t.Fatal("vlt.age was rewritten despite the key mismatch")
	}
}

func TestStore_CopyTo_RefusesExistingVault(t *testing.T) {
	t.Parallel()

	src := createVault(t, t.TempDir())
	destDir := t.TempDir()
	createVault(t, destDir).Close()
	destKey, err := os.ReadFile(filepath.Join(destDir, "vlt.age"))
	if err != nil {
		t.Fatal(err)
	}

	if err := src.CopyTo(context.Background(), destDir); !errors.Is(err, store.ErrVaultExists) {
		t.Fatalf("expected ErrVaultExists, got %v", err)
	}
	after, err := os.ReadFile(filepath.Join(destDir, "vlt.age"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destKey, after) {
		t.Fatal("destination vault key was overwritten")
	}
}

// writeDummy creates a file (and its directories) holding placeholder bytes.
func writeDummy(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("age"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Opening the folder above a vault (an easy slip in a folder picker) must not
// treat the vault as a group and migrate it, which would re-encrypt its key.
func TestStore_FolderAboveAVaultIsNotAVault(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	inner := filepath.Join(parent, "vault")
	s := createVault(t, inner)
	if err := s.Create(context.Background(), testutil.PasswordSecret("gh", "x", "work")); err != nil {
		t.Fatal(err)
	}

	if mustExist(t, parent) {
		t.Fatal("the folder above a vault must not count as a vault")
	}
	_, err := store.Open(context.Background(), parent, testPass, quietLog(), store.WithWorkFactor(testWorkFactor))
	if !errors.Is(err, store.ErrVaultNotFound) {
		t.Fatalf("Open(parent) = %v, want ErrVaultNotFound", err)
	}

	got, err := openVault(t, inner, testPass).Get(context.Background(), "gh", "work")
	if err != nil || got.Value != "x" {
		t.Fatalf("the inner vault must still open with its passphrase: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "vlt.age")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing may be written to the parent folder")
	}
}

func TestStore_Exists_TellsNestedVaultsFromGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files []string // relative to the directory checked
		want  bool
	}{
		{"secret in a group", []string{"work/gh.age"}, true},
		{"secret named vlt in a group", []string{"work/vlt.age"}, true},
		{"format-2 vault one level down", []string{"vault/vlt.age", "vault/vlt.json", "vault/work/gh.age"}, false},
		// Not detectable by layout; Open's content check keeps it safe (see
		// TestOpen_FolderAboveAFormat1VaultIsNotMigrated).
		{"format-1 vault one level down", []string{"old/vlt.age", "old/work/gh.age"}, true},
		{"migration in progress one level down", []string{"vault/vlt.migrating.age", "vault/work/gh.age"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, f := range tt.files {
				writeDummy(t, filepath.Join(dir, filepath.FromSlash(f)))
			}
			if got := mustExist(t, dir); got != tt.want {
				t.Fatalf("Exists = %v, want %v", got, tt.want)
			}
		})
	}
}
