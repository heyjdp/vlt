package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yourorg/vlt/internal/store"
	"github.com/yourorg/vlt/internal/testutil"
)

func TestStore_Open_MigratesLegacyVault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteLegacyVault(t, dir, testPass,
		testutil.PasswordSecret("gh", "ghp_x", "work"),
		testutil.RecordSecret("bank", "me", "pw", "https://bank.example", "personal"),
		testutil.PasswordSecret("misc", "m", ""),
	)

	s := openVault(t, dir, testPass)
	ctx := context.Background()

	listed, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("expected 3 secrets after migration, got %d", len(listed))
	}
	if n, files := countScryptStanzas(t, dir); n != 1 || files[0] != "vlt.age" {
		t.Fatalf("expected only vlt.age to use scrypt after migration; got %d in %v", n, files)
	}
	if _, err := os.Stat(filepath.Join(dir, "vlt.migrating.age")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migration marker should be removed, stat err = %v", err)
	}
	if len(s.SkippedFiles()) != 0 {
		t.Fatalf("unexpected skipped files: %v", s.SkippedFiles())
	}

	// The migrated vault opens as format 2 with the same passphrase.
	got, err := openVault(t, dir, testPass).Get(ctx, "bank", "personal")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Password != "pw" {
		t.Fatalf("password mismatch: got %q", got.Password)
	}
}

func TestStore_Open_LegacyWrongPassphraseChangesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := testutil.WriteLegacyVault(t, dir, testPass, testutil.PasswordSecret("gh", "x", "work"))
	before, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	_, err = store.Open(context.Background(), dir, "wrong", quietLog())
	if !errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase, got %v", err)
	}
	after, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("secret was modified by a failed unlock")
	}
	if _, err := os.Stat(filepath.Join(dir, "vlt.migrating.age")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed unlock must not start a migration")
	}
}

func TestStore_Open_LegacyUndecryptableFileIsLeftUntouched(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteLegacyVault(t, dir, testPass, testutil.PasswordSecret("good", "g", "work"))
	// A file encrypted to a different passphrase, as if copied from elsewhere.
	strayPath := filepath.Join(dir, "work", "stray.age")
	stray := testutil.EncryptWithPassphrase(t, "other-passphrase", []byte(`{"name":"stray"}`))
	if err := os.WriteFile(strayPath, stray, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := openVault(t, dir, testPass)

	if got := s.SkippedFiles(); len(got) != 1 || got[0] != filepath.Join("work", "stray.age") {
		t.Fatalf("expected stray file to be reported as skipped, got %v", got)
	}
	after, err := os.ReadFile(strayPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(stray, after) {
		t.Fatal("undecryptable file was modified")
	}
	if _, err := s.Get(context.Background(), "good", "work"); err != nil {
		t.Fatalf("Get migrated secret: %v", err)
	}
}

func TestStore_Open_CancelledMigrationCanResume(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteLegacyVault(t, dir, testPass,
		testutil.PasswordSecret("a", "1", "work"),
		testutil.PasswordSecret("b", "2", "work"),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Open(ctx, dir, testPass, quietLog(), store.WithWorkFactor(testWorkFactor)); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vlt.migrating.age")); err != nil {
		t.Fatalf("interrupted migration should leave its marker: %v", err)
	}

	s := openVault(t, dir, testPass)
	if skipped := s.SkippedFiles(); len(skipped) != 0 {
		t.Fatalf("resume skipped files: %v", skipped)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := s.Get(context.Background(), name, "work"); err != nil {
			t.Fatalf("Get %q after resume: %v", name, err)
		}
	}
}

func TestStore_Open_MigratesLegacyVaultWithoutSentinel(t *testing.T) {
	t.Parallel()

	// Vaults moved by early versions of vlt lost their vlt.age sentinel.
	dir := t.TempDir()
	testutil.WriteLegacyVault(t, dir, testPass, testutil.PasswordSecret("gh", "ghp_x", "work"))
	if err := os.Remove(filepath.Join(dir, "vlt.age")); err != nil {
		t.Fatal(err)
	}

	if !mustExist(t, dir) {
		t.Fatal("a directory of legacy secrets must count as an existing vault")
	}
	if _, err := store.Create(dir, testPass, quietLog(), store.WithWorkFactor(testWorkFactor)); !errors.Is(err, store.ErrVaultExists) {
		t.Fatalf("Create over legacy secrets: expected ErrVaultExists, got %v", err)
	}

	got, err := openVault(t, dir, testPass).Get(context.Background(), "gh", "work")
	if err != nil {
		t.Fatalf("Get after migration: %v", err)
	}
	if got.Value != "ghp_x" {
		t.Fatalf("value mismatch: got %q", got.Value)
	}
}

func TestStore_Open_LegacyWithoutSentinelRejectsWrongPassphrase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := testutil.WriteLegacyVault(t, dir, testPass, testutil.PasswordSecret("gh", "x", "work"))
	if err := os.Remove(filepath.Join(dir, "vlt.age")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Open(context.Background(), dir, "wrong", quietLog())
	if !errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase, got %v", err)
	}
	after, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("secret modified by failed unlock")
	}
	for _, name := range []string{"vlt.age", "vlt.migrating.age", "vlt.json"} {
		if fileExists(filepath.Join(dir, name)) {
			t.Fatalf("failed unlock must not create %s", name)
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
