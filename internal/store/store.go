package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yourorg/vlt/internal/crypto"
)

// ErrSecretNotFound is returned when a secret does not exist in the vault.
var ErrSecretNotFound = errors.New("secret not found")

// Store manages secrets in an age-encrypted vault directory.
// Secrets are stored at vault/<group>/<slug>.age, encrypted to the vault key.
// Use Create or Open to obtain one; New is for callers that already hold the
// vault key.
type Store struct {
	dir string
	id  *crypto.Identity
	log *slog.Logger

	opts options

	// skipped lists legacy files that could not be migrated (see migrate.go).
	skipped []string
}

// New creates a Store over dir that encrypts and decrypts with the given
// vault key. It does not read or write vlt.age.
func New(id *crypto.Identity, dir string, log *slog.Logger) *Store {
	return newStore(id, dir, log, options{})
}

func newStore(id *crypto.Identity, dir string, log *slog.Logger, o options) *Store {
	return &Store{
		dir:  dir,
		id:   id,
		log:  log,
		opts: o,
	}
}

// Dir returns the vault directory path.
func (s *Store) Dir() string {
	return s.dir
}

// Init creates the vault directory with secure permissions.
func (s *Store) Init() error {
	if err := mkdirAllSynced(s.dir, 0o700); err != nil {
		return fmt.Errorf("store.Init: %w", err)
	}
	return nil
}

// Save encrypts and writes a secret to the vault directory.
// The secret is stored at vault/<group>/<slug>.age.
func (s *Store) Save(_ context.Context, secret *Secret) error {
	if secret.Name == "" {
		return errors.New("store.Save: secret name cannot be empty")
	}

	secret.Group = NormalizeGroup(secret.Group)
	secret.ModifiedAt = time.Now().UTC()
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = secret.ModifiedAt
	}
	if secret.Version == 0 {
		secret.Version = 1
	}

	data, err := json.MarshalIndent(secret, "", "  ")
	if err != nil {
		return fmt.Errorf("store.Save: marshal: %w", err)
	}

	ciphertext, err := crypto.Encrypt(s.id, data)
	if err != nil {
		return fmt.Errorf("store.Save: encrypt: %w", err)
	}

	path := s.secretPath(secret)
	dir := filepath.Dir(path)
	if err := mkdirAllSynced(dir, 0o700); err != nil {
		return fmt.Errorf("store.Save: mkdir: %w", err)
	}

	if err := writeFileAtomic(path, ciphertext, 0o600); err != nil {
		return fmt.Errorf("store.Save: write: %w", err)
	}

	s.log.Info("secret saved", "name", secret.Name, "group", secret.Group, "path", path)
	return nil
}

// Get decrypts and returns a secret by name and group.
func (s *Store) Get(_ context.Context, name string, group string) (*Secret, error) {
	sn, err := NewSecretName(name)
	if err != nil {
		return nil, fmt.Errorf("store.Get: %w", err)
	}

	g := NormalizeGroup(group)
	path := filepath.Join(s.dir, g, sn.Slug()+".age")

	ciphertext, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("store.Get %q: %w", name, ErrSecretNotFound)
		}
		return nil, fmt.Errorf("store.Get %q: %w", name, err)
	}

	plaintext, err := crypto.Decrypt(s.id, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("store.Get %q: %w", name, err)
	}

	var secret Secret
	if err := json.Unmarshal(plaintext, &secret); err != nil {
		return nil, fmt.Errorf("store.Get %q: unmarshal: %w", name, err)
	}

	return &secret, nil
}

// Delete removes a secret from the vault by name and group.
func (s *Store) Delete(_ context.Context, name string, group string) error {
	sn, err := NewSecretName(name)
	if err != nil {
		return fmt.Errorf("store.Delete: %w", err)
	}

	g := NormalizeGroup(group)
	path := filepath.Join(s.dir, g, sn.Slug()+".age")

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("store.Delete %q: %w", name, ErrSecretNotFound)
		}
		return fmt.Errorf("store.Delete %q: %w", name, err)
	}

	s.log.Info("secret deleted", "name", name, "group", g)
	return nil
}

// List returns all secrets in the vault by walking every group subdirectory.
func (s *Store) List(ctx context.Context) ([]*Secret, error) {
	var secrets []*Secret

	groups, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store.List: %w", err)
	}

	for _, groupEntry := range groups {
		if !groupEntry.IsDir() {
			continue
		}

		groupDir := filepath.Join(s.dir, groupEntry.Name())
		entries, err := os.ReadDir(groupDir)
		if err != nil {
			s.log.Warn("failed to read group directory", "group", groupEntry.Name(), "err", err)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".age" {
				continue
			}

			path := filepath.Join(groupDir, entry.Name())
			ciphertext, err := os.ReadFile(path)
			if err != nil {
				s.log.Warn("failed to read secret file", "path", path, "err", err)
				continue
			}

			plaintext, err := crypto.Decrypt(s.id, ciphertext)
			if err != nil {
				s.log.Warn("failed to decrypt secret", "path", path, "err", err)
				continue
			}

			var secret Secret
			if err := json.Unmarshal(plaintext, &secret); err != nil {
				s.log.Warn("failed to unmarshal secret", "path", path, "err", err)
				continue
			}

			secrets = append(secrets, &secret)
		}
	}

	_ = ctx
	return secrets, nil
}

// Groups returns a sorted list of all group names that exist in the vault.
func (s *Store) Groups() []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}

	var groups []string
	for _, e := range entries {
		if e.IsDir() {
			groups = append(groups, e.Name())
		}
	}
	sort.Strings(groups)
	return groups
}

// CopyTo copies the wrapped vault key, the format marker, and every .age
// file from this store's directory into destDir, preserving the group
// subdirectory structure. It refuses (ErrVaultExists) to copy into a
// directory that already holds a vault or secret files, since overwriting
// another vault's vlt.age would make its secrets unrecoverable. An open
// Store never has a migration in progress, so vlt.migrating.age is not copied.
func (s *Store) CopyTo(destDir string) error {
	if Exists(destDir) {
		return fmt.Errorf("store.CopyTo %q: %w", destDir, ErrVaultExists)
	}
	if err := mkdirAllSynced(destDir, 0o700); err != nil {
		return fmt.Errorf("store.CopyTo: mkdir %q: %w", destDir, err)
	}

	// Copy the vault-level files that exist
	for _, name := range []string{keyFile, formatFile} {
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("store.CopyTo: read %s: %w", name, err)
		}
		if err := writeFileAtomic(filepath.Join(destDir, name), data, 0o600); err != nil {
			return fmt.Errorf("store.CopyTo: write %s: %w", name, err)
		}
	}

	// Copy group subdirectories
	groups, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("store.CopyTo: read %q: %w", s.dir, err)
	}

	for _, groupEntry := range groups {
		if !groupEntry.IsDir() {
			continue
		}

		srcDir := filepath.Join(s.dir, groupEntry.Name())
		dstDir := filepath.Join(destDir, groupEntry.Name())

		if err := mkdirAllSynced(dstDir, 0o700); err != nil {
			return fmt.Errorf("store.CopyTo: mkdir %q: %w", dstDir, err)
		}

		entries, err := os.ReadDir(srcDir)
		if err != nil {
			return fmt.Errorf("store.CopyTo: read %q: %w", srcDir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".age" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(srcDir, entry.Name()))
			if err != nil {
				return fmt.Errorf("store.CopyTo: read file %q: %w", entry.Name(), err)
			}
			if err := writeFileAtomic(filepath.Join(dstDir, entry.Name()), data, 0o600); err != nil {
				return fmt.Errorf("store.CopyTo: write file %q: %w", entry.Name(), err)
			}
		}
	}

	s.log.Info("vault copied", "from", s.dir, "to", destDir)
	return nil
}

// SetDir changes the vault directory the store operates on.
func (s *Store) SetDir(dir string) {
	s.dir = dir
}

// secretPath returns the filesystem path for a secret: vault/<group>/<slug>.age
func (s *Store) secretPath(secret *Secret) string {
	g := NormalizeGroup(secret.Group)
	slug := Slugify(secret.Name)
	return filepath.Join(s.dir, g, slug+".age")
}
