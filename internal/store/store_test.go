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

	if err := s.Create(ctx, secret); err != nil {
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

	if err := s.Create(ctx, secret); err != nil {
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

	if err := s.Create(ctx, secret); err != nil {
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
	if err := s.Create(ctx, secret); err != nil {
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
		if err := s.Create(ctx, sec); err != nil {
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

	if err := s.Create(ctx, testutil.PasswordSecret("key-1", "val1", "work")); err != nil {
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

func TestStore_Update_ReplacesValueAndKeepsCreatedAt(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	secret := testutil.PasswordSecret("update-me", "original", "work")
	if err := s.Create(ctx, secret); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created := secret.CreatedAt

	edited := testutil.PasswordSecret("update-me", "updated", "work")
	if err := s.Update(ctx, edited); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.Get(ctx, "update-me", "work")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != "updated" {
		t.Fatalf("expected updated value, got %q", got.Value)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt changed: got %v, want %v", got.CreatedAt, created)
	}
}

func TestStore_Create_RefusesExistingSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		existing string
		incoming string
	}{
		{"same name", "GitHub Key", "GitHub Key"},
		{"case differs", "GitHub Key", "github key"},
		{"punctuation differs", "GitHub Key", "github-key"},
		{"underscore and bang", "GitHub Key", "GitHub_Key!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newTestStore(t)
			ctx := context.Background()

			if err := s.Create(ctx, testutil.PasswordSecret(tt.existing, "original", "work")); err != nil {
				t.Fatalf("Create existing: %v", err)
			}
			err := s.Create(ctx, testutil.PasswordSecret(tt.incoming, "intruder", "work"))
			if !errors.Is(err, store.ErrSecretExists) {
				t.Fatalf("expected ErrSecretExists, got %v", err)
			}

			got, err := s.Get(ctx, tt.existing, "work")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Value != "original" || got.Name != tt.existing {
				t.Fatalf("existing secret was overwritten: %+v", got)
			}
		})
	}
}

func TestStore_Create_AllowsSameNameInDifferentGroups(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testutil.PasswordSecret("token", "a", "work")); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, testutil.PasswordSecret("token", "b", "personal")); err != nil {
		t.Fatalf("same name in another group should be allowed: %v", err)
	}
}

func TestStore_Update_RefusesSlugCollision(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testutil.PasswordSecret("GitHub Key", "original", "work")); err != nil {
		t.Fatal(err)
	}

	err := s.Update(ctx, testutil.PasswordSecret("github-key", "intruder", "work"))
	if !errors.Is(err, store.ErrSecretExists) {
		t.Fatalf("expected ErrSecretExists, got %v", err)
	}
	got, err := s.Get(ctx, "GitHub Key", "work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "original" {
		t.Fatalf("secret overwritten through a slug collision: %q", got.Value)
	}
}

func TestStore_Update_ReturnsErrNotFoundForMissing(t *testing.T) {
	t.Parallel()

	err := newTestStore(t).Update(context.Background(), testutil.PasswordSecret("ghost", "x", "work"))
	if !errors.Is(err, store.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got %v", err)
	}
}

func TestStore_Create_RejectsEmptyName(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"", "   ", "!!!", "日本"} {
		if err := s.Create(ctx, testutil.PasswordSecret(name, "value", "work")); err == nil {
			t.Fatalf("expected error for name %q", name)
		}
	}
}

func TestStore_CopyTo_IgnoresLeftoverTempFiles(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Create(ctx, testutil.PasswordSecret("copy-1", "v1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	leftover := filepath.Join(s.Dir(), "work", ".tmp-123456")
	if err := os.WriteFile(leftover, []byte("partial"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	destDir := t.TempDir()
	if err := s.CopyTo(context.Background(), destDir); err != nil {
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
	if err := s.Create(ctx, testutil.PasswordSecret("my-key", "val", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file is at vault/work/my-key.age
	expectedPath := filepath.Join(dir, "work", "my-key.age")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected file at %q: %v", expectedPath, err)
	}
}

func TestStore_List_StopsWhenContextCancelled(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	if err := s.Create(context.Background(), testutil.PasswordSecret("a", "1", "work")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := s.List(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if got != nil {
		t.Fatalf("a cancelled List must not return partial results, got %d", len(got))
	}
}
