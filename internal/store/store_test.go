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

func TestStore_Init_CreatesVaultDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	vaultDir := filepath.Join(dir, "myvault")
	id := testutil.NewTestIdentity(t, "test-init-passphrase")
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

	if err := s.Save(ctx, testutil.PasswordSecret("key-1", "val1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, testutil.RecordSecret("rec-1", "user", "pass", "https://example.com", "personal")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	newID := testutil.NewTestIdentity(t, "new-passphrase")
	if err := s.ReEncryptAll(ctx, newID); err != nil {
		t.Fatalf("ReEncryptAll: %v", err)
	}

	got, err := s.Get(ctx, "key-1", "work")
	if err != nil {
		t.Fatalf("Get after re-encrypt: %v", err)
	}
	if got.Value != "val1" {
		t.Fatalf("value mismatch after re-encrypt: got %q", got.Value)
	}

	oldID2 := testutil.NewTestIdentity(t, "old-passphrase")
	oldStore := store.New(oldID2, dir, log)
	_, err = oldStore.Get(ctx, "key-1", "work")
	if err == nil {
		t.Fatal("expected error decrypting with old passphrase after re-encrypt")
	}
}

func TestStore_CopyTo_CopiesAllSecrets(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateSentinel(); err != nil {
		t.Fatalf("CreateSentinel: %v", err)
	}

	if err := s.Save(ctx, testutil.PasswordSecret("copy-1", "v1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, testutil.RecordSecret("copy-2", "u", "p", "https://example.com", "personal")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	destDir := t.TempDir()
	if err := s.CopyTo(destDir); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}

	// Verify group directories exist in destination
	for _, group := range []string{"work", "personal"} {
		entries, err := os.ReadDir(filepath.Join(destDir, group))
		if err != nil {
			t.Fatalf("ReadDir %q: %v", group, err)
		}
		if len(entries) == 0 {
			t.Fatalf("expected files in %q", group)
		}
	}

	// Verify we can read from the new location with same identity
	id := testutil.NewTestIdentity(t, "test-store-passphrase")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	destStore := store.New(id, destDir, log)

	got, err := destStore.Get(ctx, "copy-1", "work")
	if err != nil {
		t.Fatalf("Get from dest: %v", err)
	}
	if got.Value != "v1" {
		t.Fatalf("value mismatch: got %q", got.Value)
	}

	// Verify sentinel was copied
	if destStore.IsNew() {
		t.Fatal("expected sentinel to be copied to destination (IsNew should be false)")
	}
	if err := destStore.Verify(); err != nil {
		t.Fatalf("Verify on dest should succeed: %v", err)
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

func TestStore_SwitchToNewDir_IsNew(t *testing.T) {
	t.Parallel()

	// Simulates switching vault to a new empty directory
	newDir := t.TempDir()
	id := testutil.NewTestIdentity(t, "test-switch")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.New(id, newDir, log)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// New dir has no sentinel
	if !s.IsNew() {
		t.Fatal("expected IsNew() == true for new vault dir")
	}

	// Creating sentinel makes it no longer new
	if err := s.CreateSentinel(); err != nil {
		t.Fatalf("CreateSentinel: %v", err)
	}
	if s.IsNew() {
		t.Fatal("expected IsNew() == false after CreateSentinel")
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestStore_IsNew_TrueForFreshVault(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	if !s.IsNew() {
		t.Fatal("expected IsNew() == true for fresh vault")
	}
}

func TestStore_CreateSentinel_MakesIsNewFalse(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	if err := s.CreateSentinel(); err != nil {
		t.Fatalf("CreateSentinel: %v", err)
	}

	if s.IsNew() {
		t.Fatal("expected IsNew() == false after CreateSentinel")
	}
}

func TestStore_Verify_SucceedsWithCorrectPassphrase(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	if err := s.CreateSentinel(); err != nil {
		t.Fatalf("CreateSentinel: %v", err)
	}

	if err := s.Verify(); err != nil {
		t.Fatalf("Verify should succeed with correct passphrase: %v", err)
	}
}

func TestStore_Verify_FailsWithWrongPassphrase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create sentinel with one passphrase
	id1 := testutil.NewTestIdentity(t, "correct-passphrase")
	s1 := store.New(id1, dir, log)
	if err := s1.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s1.CreateSentinel(); err != nil {
		t.Fatalf("CreateSentinel: %v", err)
	}

	// Try to verify with a different passphrase
	id2 := testutil.NewTestIdentity(t, "wrong-passphrase")
	s2 := store.New(id2, dir, log)

	err := s2.Verify()
	if err == nil {
		t.Fatal("expected error verifying with wrong passphrase")
	}
	if !errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase, got: %v", err)
	}
}

func TestStore_Verify_SucceedsWithNoSentinel(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	// No sentinel created — Verify should return nil (new vault)
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify should succeed when no sentinel exists: %v", err)
	}
}

func TestStore_RecreateSentinel_AfterReEncrypt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create vault with old passphrase
	oldID := testutil.NewTestIdentity(t, "old-pass")
	s := store.New(oldID, dir, log)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.CreateSentinel(); err != nil {
		t.Fatalf("CreateSentinel: %v", err)
	}

	ctx := context.Background()
	if err := s.Save(ctx, testutil.PasswordSecret("k1", "v1", "work")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Re-encrypt with new passphrase
	newID := testutil.NewTestIdentity(t, "new-pass")
	if err := s.ReEncryptAll(ctx, newID); err != nil {
		t.Fatalf("ReEncryptAll: %v", err)
	}
	if err := s.RecreateSentinel(); err != nil {
		t.Fatalf("RecreateSentinel: %v", err)
	}

	// New passphrase should verify
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify with new passphrase failed: %v", err)
	}

	// Old passphrase should not verify
	oldID2 := testutil.NewTestIdentity(t, "old-pass")
	oldStore := store.New(oldID2, dir, log)
	err := oldStore.Verify()
	if !errors.Is(err, store.ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase for old passphrase, got: %v", err)
	}
}

func TestStore_VaultStructure_GroupBasedPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := testutil.NewTestIdentity(t, "test-structure")
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
