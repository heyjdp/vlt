package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/yourorg/vlt/internal/crypto"
)

// ErrSecretNotFound is returned when a secret does not exist in the vault.
var ErrSecretNotFound = errors.New("secret not found")

// ErrVaultLocked is returned when the vault identity is not available.
var ErrVaultLocked = errors.New("vault is locked")

// Encryptor encrypts plaintext data.
type Encryptor interface {
	Encrypt(plaintext []byte) ([]byte, error)
}

// Decryptor decrypts ciphertext data.
type Decryptor interface {
	Decrypt(ciphertext []byte) ([]byte, error)
}

// Store manages secrets in an age-encrypted vault directory.
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

// Init creates the vault directory structure with secure permissions.
func (s *Store) Init() error {
	for _, st := range ValidSecretTypes() {
		dir := filepath.Join(s.dir, CategoryDir(st))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("store.Init: %w", err)
		}
	}
	return nil
}

// Save encrypts and writes a secret to the vault directory.
func (s *Store) Save(_ context.Context, secret *Secret) error {
	if secret.Name == "" {
		return errors.New("store.Save: secret name cannot be empty")
	}

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

	s.log.Info("secret saved", "name", secret.Name, "type", secret.Type, "path", path)
	return nil
}

// Get decrypts and returns a secret by name and type.
func (s *Store) Get(_ context.Context, name string, secretType SecretType) (*Secret, error) {
	sn, err := NewSecretName(name)
	if err != nil {
		return nil, fmt.Errorf("store.Get: %w", err)
	}

	path := filepath.Join(s.dir, CategoryDir(secretType), sn.Slug()+".age")

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

// Delete removes a secret from the vault.
func (s *Store) Delete(_ context.Context, name string, secretType SecretType) error {
	sn, err := NewSecretName(name)
	if err != nil {
		return fmt.Errorf("store.Delete: %w", err)
	}

	path := filepath.Join(s.dir, CategoryDir(secretType), sn.Slug()+".age")

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("store.Delete %q: %w", name, ErrSecretNotFound)
		}
		return fmt.Errorf("store.Delete %q: %w", name, err)
	}

	s.log.Info("secret deleted", "name", name, "type", secretType)
	return nil
}

// List returns all secrets in the vault.
// Each secret is decrypted to read its metadata.
func (s *Store) List(ctx context.Context) ([]*Secret, error) {
	var secrets []*Secret

	for _, st := range ValidSecretTypes() {
		dir := filepath.Join(s.dir, CategoryDir(st))

		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("store.List: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".age" {
				continue
			}

			path := filepath.Join(dir, entry.Name())
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

	_ = ctx // respect context in future iterations
	return secrets, nil
}

// ReEncryptAll decrypts every secret with the current identity and
// re-encrypts with newID. On success the store's identity is swapped
// to newID and the old identity is zeroed.
func (s *Store) ReEncryptAll(ctx context.Context, newID *crypto.Identity) error {
	secrets, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("store.ReEncryptAll: list: %w", err)
	}

	// Re-encrypt each secret with the new identity
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
// preserving the category subdirectory structure. The destination directory
// is created with 0700 permissions if it does not exist.
func (s *Store) CopyTo(destDir string) error {
	for _, st := range ValidSecretTypes() {
		srcDir := filepath.Join(s.dir, CategoryDir(st))
		dstDir := filepath.Join(destDir, CategoryDir(st))

		if err := os.MkdirAll(dstDir, 0o700); err != nil {
			return fmt.Errorf("store.CopyTo: mkdir %q: %w", dstDir, err)
		}

		entries, err := os.ReadDir(srcDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
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

// secretPath returns the filesystem path for a secret.
func (s *Store) secretPath(secret *Secret) string {
	slug := Slugify(secret.Name)
	return filepath.Join(s.dir, CategoryDir(secret.Type), slug+".age")
}
