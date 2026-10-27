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
	id := testutil.NewTestIdentity(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.New(id, dir, log)
	if err := s.Init(); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return s
}

func TestStore_Init_CreatesVaultDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	vaultDir := filepath.Join(dir, "myvault")
	id := testutil.NewTestIdentity(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.New(id, vaultDir, log)

	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	info, err := os.Stat(vaultDir)
	if err != nil {
		t.Fatalf("expected vault directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected vault path to be a directory")
	}
}

func TestStore_SaveAndGet_Password(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secret := testutil.PasswordSecret("GitHub API Key", "ghp_xxxxxxxxxxxx", "work")
	secret.Tags = []string{"github", "ci"}

	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "GitHub API Key", "work")
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
	if got.Group != "work" {
		t.Fatalf("group mismatch: got %q", got.Group)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "github" {
		t.Fatalf("tags mismatch: got %v", got.Tags)
	}
}

func TestStore_SaveAndGet_Record(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secret := testutil.RecordSecret("Work Jira", "user@example.com", "secret", "https://jira.example.com", "work")

	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "Work Jira", "work")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Username != "user@example.com" {
		t.Fatalf("username mismatch: got %q", got.Username)
	}
	if got.URL != "https://jira.example.com" {
		t.Fatalf("url mismatch: got %q", got.URL)
	}
	if got.Group != "work" {
		t.Fatalf("group mismatch: got %q", got.Group)
	}
}

func TestStore_SaveAndGet_DefaultGroup(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	// Empty group should normalize to "default"
	secret := testutil.PasswordSecret("no-group", "val", "")

	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "no-group", "default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Group != "default" {
		t.Fatalf("expected group %q, got %q", "default", got.Group)
	}
}

func TestStore_Get_ReturnsErrNotFoundForMissing(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "nonexistent", "work")
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

	secret := testutil.PasswordSecret("to-delete", "value", "personal")
	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.Delete(ctx, "to-delete", "personal"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Get(ctx, "to-delete", "personal")
	if !errors.Is(err, store.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound after delete, got: %v", err)
	}
}

func TestStore_Delete_ReturnsErrNotFoundForMissing(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	err := s.Delete(ctx, "nonexistent", "work")
	if !errors.Is(err, store.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got: %v", err)
	}
}

func TestStore_List_ReturnsAllSecrets(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secrets := []*store.Secret{
		testutil.PasswordSecret("key-1", "val1", "work"),
		testutil.PasswordSecret("key-2", "val2", "personal"),
		testutil.RecordSecret("record-1", "user", "pass", "https://example.com", "work"),
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

// TestStore_List_IgnoresLeftoverTempFiles is a regression guard: List filters
// on the .age extension, which temp files written by writeFileAtomic never have.
func TestStore_List_IgnoresLeftoverTempFiles(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Save(ctx, testutil.PasswordSecret("key-1", "val1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Simulate a crash between temp-file creation and rename.
	leftover := filepath.Join(s.Dir(), "work", ".tmp-123456")
	if err := os.WriteFile(leftover, []byte("partial"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(got))
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

	secret := testutil.PasswordSecret("update-me", "original", "work")
	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	secret.Value = "updated"
	if err := s.Save(ctx, secret); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	got, err := s.Get(ctx, "update-me", "work")
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

	secret := testutil.PasswordSecret("", "value", "work")
	err := s.Save(ctx, secret)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestStore_Groups_ReturnsAllGroups(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Save(ctx, testutil.PasswordSecret("k1", "v1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, testutil.PasswordSecret("k2", "v2", "personal")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, testutil.PasswordSecret("k3", "v3", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	groups := s.Groups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d: %v", len(groups), groups)
	}
	if groups[0] != "personal" || groups[1] != "work" {
		t.Fatalf("expected [personal work], got %v", groups)
	}
}

func TestStore_CopyTo_IgnoresLeftoverTempFiles(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Save(ctx, testutil.PasswordSecret("copy-1", "v1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	leftover := filepath.Join(s.Dir(), "work", ".tmp-123456")
	if err := os.WriteFile(leftover, []byte("partial"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	destDir := t.TempDir()
	if err := s.CopyTo(destDir); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(destDir, "work"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "copy-1.age" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected only copy-1.age in destination, got %v", names)
	}
}

func TestStore_VaultStructure_GroupBasedPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := testutil.NewTestIdentity(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.New(id, dir, log)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx := context.Background()
	if err := s.Save(ctx, testutil.PasswordSecret("my-key", "val", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file is at vault/work/my-key.age
	expectedPath := filepath.Join(dir, "work", "my-key.age")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected file at %q: %v", expectedPath, err)
	}
}
