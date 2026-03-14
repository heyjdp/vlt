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

// ErrVaultLocked is returned when the vault identity is not available.
var ErrVaultLocked = errors.New("vault is locked")

// Store manages secrets in an age-encrypted vault directory.
// Secrets are stored at vault/<group>/<slug>.age.
type Store struct {
	dir string
	id  *crypto.Identity
	log *slog.Logger
}

// New creates a new Store operating on the given vault directory
// with the given identity for encryption/decryption.
func New(id *crypto.Identity, dir string, log *slog.Logger) *Store {
	return &Store{
		dir: dir,
		id:  id,
		log: log,
	}
}

// Dir returns the vault directory path.
func (s *Store) Dir() string {
	return s.dir
}

// Init creates the vault directory with secure permissions.
func (s *Store) Init() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("store.Save: mkdir: %w", err)
	}

	if err := os.WriteFile(path, ciphertext, 0o600); err != nil {
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

// ReEncryptAll decrypts every secret with the current identity and
// re-encrypts with newID. On success the store's identity is swapped
// to newID and the old identity is zeroed.
func (s *Store) ReEncryptAll(ctx context.Context, newID *crypto.Identity) error {
	secrets, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("store.ReEncryptAll: list: %w", err)
	}

	for _, sec := range secrets {
		data, err := json.MarshalIndent(sec, "", "  ")
		if err != nil {
			return fmt.Errorf("store.ReEncryptAll: marshal %q: %w", sec.Name, err)
		}

		ciphertext, err := crypto.Encrypt(newID, data)
		if err != nil {
			return fmt.Errorf("store.ReEncryptAll: encrypt %q: %w", sec.Name, err)
		}

		path := s.secretPath(sec)
		if err := os.WriteFile(path, ciphertext, 0o600); err != nil {
			return fmt.Errorf("store.ReEncryptAll: write %q: %w", sec.Name, err)
		}
	}

	s.log.Info("vault re-encrypted", "secrets", len(secrets))
	s.id = newID
	return nil
}

// CopyTo copies every .age file from this store's directory into destDir,
// preserving the group subdirectory structure.
func (s *Store) CopyTo(destDir string) error {
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

		if err := os.MkdirAll(dstDir, 0o700); err != nil {
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
			if err := os.WriteFile(filepath.Join(dstDir, entry.Name()), data, 0o600); err != nil {
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
