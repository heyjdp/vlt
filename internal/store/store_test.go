package store_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/yourorg/vlt/internal/store"
	"github.com/yourorg/vlt/internal/testutil"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	id := testutil.NewTestIdentity(t, "test-store-passphrase")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.New(id, dir, log)
	if err := s.Init(); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return s
}

func TestStore_Init_CreatesDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := testutil.NewTestIdentity(t, "test-init-passphrase")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.New(id, dir, log)

	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, sub := range []string{"passwords", "records", "files"} {
		path := filepath.Join(dir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected directory %q to exist: %v", sub, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %q to be a directory", sub)
		}
	}
}

func TestStore_SaveAndGet_Password(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secret := testutil.PasswordSecret("GitHub API Key", "ghp_xxxxxxxxxxxx")
	secret.Tags = []string{"github", "ci"}

	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "GitHub API Key", store.TypePassword)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != "GitHub API Key" {
		t.Fatalf("name mismatch: got %q", got.Name)
	}
	if got.Value != "ghp_xxxxxxxxxxxx" {
		t.Fatalf("value mismatch: got %q", got.Value)
	}
	if got.Type != store.TypePassword {
		t.Fatalf("type mismatch: got %q", got.Type)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "github" {
		t.Fatalf("tags mismatch: got %v", got.Tags)
	}
}

func TestStore_SaveAndGet_Record(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secret := testutil.RecordSecret("Work Jira", "user@example.com", "secret", "https://jira.example.com")

	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "Work Jira", store.TypeRecord)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Username != "user@example.com" {
		t.Fatalf("username mismatch: got %q", got.Username)
	}
	if got.URL != "https://jira.example.com" {
		t.Fatalf("url mismatch: got %q", got.URL)
	}
}

func TestStore_Get_ReturnsErrNotFoundForMissing(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "nonexistent", store.TypePassword)
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !errors.Is(err, store.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got: %v", err)
	}
}

func TestStore_Delete_RemovesSecret(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secret := testutil.PasswordSecret("to-delete", "value")
	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.Delete(ctx, "to-delete", store.TypePassword); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Get(ctx, "to-delete", store.TypePassword)
	if !errors.Is(err, store.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound after delete, got: %v", err)
	}
}

func TestStore_Delete_ReturnsErrNotFoundForMissing(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	err := s.Delete(ctx, "nonexistent", store.TypePassword)
	if !errors.Is(err, store.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got: %v", err)
	}
}

func TestStore_List_ReturnsAllSecrets(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secrets := []*store.Secret{
		testutil.PasswordSecret("key-1", "val1"),
		testutil.PasswordSecret("key-2", "val2"),
		testutil.RecordSecret("record-1", "user", "pass", "https://example.com"),
	}

	for _, sec := range secrets {
		if err := s.Save(ctx, sec); err != nil {
			t.Fatalf("Save %q: %v", sec.Name, err)
		}
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 secrets, got %d", len(got))
	}
}

func TestStore_List_ReturnsEmptyForNewVault(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected 0 secrets, got %d", len(got))
	}
}

func TestStore_Save_UpdatesExisting(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secret := testutil.PasswordSecret("update-me", "original")
	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	secret.Value = "updated"
	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	got, err := s.Get(ctx, "update-me", store.TypePassword)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Value != "updated" {
		t.Fatalf("expected updated value, got %q", got.Value)
	}
}

func TestStore_Save_RejectsEmptyName(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secret := testutil.PasswordSecret("", "value")
	err := s.Save(ctx, secret)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestStore_ReEncryptAll_ChangesPassphrase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oldID := testutil.NewTestIdentity(t, "old-passphrase")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.New(oldID, dir, log)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx := context.Background()

	// Save some secrets with old passphrase
	if err := s.Save(ctx, testutil.PasswordSecret("key-1", "val1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, testutil.RecordSecret("rec-1", "user", "pass", "https://example.com")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Re-encrypt with new passphrase
	newID := testutil.NewTestIdentity(t, "new-passphrase")
	if err := s.ReEncryptAll(ctx, newID); err != nil {
		t.Fatalf("ReEncryptAll: %v", err)
	}

	// Verify we can read with the new identity (store now uses newID)
	got, err := s.Get(ctx, "key-1", store.TypePassword)
	if err != nil {
		t.Fatalf("Get after re-encrypt: %v", err)
	}
	if got.Value != "val1" {
		t.Fatalf("value mismatch after re-encrypt: got %q", got.Value)
	}

	// Verify old identity can no longer decrypt
	oldID2 := testutil.NewTestIdentity(t, "old-passphrase")
	oldStore := store.New(oldID2, dir, log)
	_, err = oldStore.Get(ctx, "key-1", store.TypePassword)
	if err == nil {
		t.Fatal("expected error decrypting with old passphrase after re-encrypt")
	}
}

func TestStore_CopyTo_CopiesAllSecrets(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Save(ctx, testutil.PasswordSecret("copy-1", "v1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, testutil.RecordSecret("copy-2", "u", "p", "https://example.com")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	destDir := t.TempDir()
	if err := s.CopyTo(destDir); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}

	// Verify files exist in destination
	for _, sub := range []string{"passwords", "records"} {
		entries, err := os.ReadDir(filepath.Join(destDir, sub))
		if err != nil {
			t.Fatalf("ReadDir %q: %v", sub, err)
		}
		if len(entries) == 0 {
			t.Fatalf("expected files in %q", sub)
		}
	}

	// Verify we can read from the new location with same identity
	id := testutil.NewTestIdentity(t, "test-store-passphrase")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	destStore := store.New(id, destDir, log)

	got, err := destStore.Get(ctx, "copy-1", store.TypePassword)
	if err != nil {
		t.Fatalf("Get from dest: %v", err)
	}
	if got.Value != "v1" {
		t.Fatalf("value mismatch: got %q", got.Value)
	}
}
