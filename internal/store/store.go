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
	"strings"
	"time"

	"github.com/yourorg/vlt/internal/crypto"
)

// ErrSecretNotFound is returned when a secret does not exist in the vault.
var ErrSecretNotFound = errors.New("secret not found")

// ErrSecretExists is returned by Create when the secret's file is already
// taken, and by Update when it is held by a differently named secret.
var ErrSecretExists = errors.New("a secret with that name already exists")

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

// Create encrypts and writes a new secret to vault/<group>/<slug>.age. It
// returns ErrSecretExists if any secret already occupies that path, including
// a different name with the same slug (e.g. "GitHub Key" and "github-key"),
// so it never overwrites. The name must pass NewSecretName. The secret's Name
// and Group are normalised in place.
func (s *Store) Create(_ context.Context, secret *Secret) error {
	sn, err := NewSecretName(secret.Name)
	if err != nil {
		return fmt.Errorf("store.Create: %w", err)
	}
	secret.Name = sn.String()
	path := s.path(secret.Name, secret.Group)

	data, err := s.seal(secret)
	if err != nil {
		return fmt.Errorf("store.Create: %w", err)
	}
	if err := mkdirAllSynced(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("store.Create: mkdir: %w", err)
	}

	err = writeFileExclusive(path, data, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("store.Create %q in %q: %w", secret.Name, secret.Group, ErrSecretExists)
	}
	if err != nil {
		return fmt.Errorf("store.Create: write: %w", err)
	}

	s.log.Info("secret created", "name", secret.Name, "group", secret.Group)
	return nil
}

// Update re-encrypts an existing secret in place. The file at the secret's
// path must exist and hold a secret with the same name (compared after
// trimming, so names stored untrimmed by older versions still match);
// otherwise it returns ErrSecretNotFound or ErrSecretExists (a slug collision
// with a differently named secret). CreatedAt is preserved from the stored
// copy. Names that predate NewSecretName's rules are accepted here so legacy
// secrets stay editable.
func (s *Store) Update(ctx context.Context, secret *Secret) error {
	name, err := lookupName(secret.Name)
	if err != nil {
		return fmt.Errorf("store.Update: %w", err)
	}
	existing, err := s.Get(ctx, name, secret.Group)
	if err != nil {
		return fmt.Errorf("store.Update: %w", err)
	}
	if strings.TrimSpace(existing.Name) != name {
		return fmt.Errorf("store.Update %q: file is held by %q: %w", name, existing.Name, ErrSecretExists)
	}
	secret.Name = name
	secret.CreatedAt = existing.CreatedAt

	data, err := s.seal(secret)
	if err != nil {
		return fmt.Errorf("store.Update: %w", err)
	}
	if err := writeFileAtomic(s.path(name, secret.Group), data, 0o600); err != nil {
		return fmt.Errorf("store.Update: write: %w", err)
	}

	s.log.Info("secret updated", "name", secret.Name, "group", secret.Group)
	return nil
}

// seal normalises the secret's group, stamps its timestamps, and returns its
// ciphertext. The caller has already validated and normalised the name.
func (s *Store) seal(secret *Secret) ([]byte, error) {
	secret.Group = NormalizeGroup(secret.Group)
	secret.ModifiedAt = time.Now().UTC()
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = secret.ModifiedAt
	}
	if secret.Version == 0 {
		secret.Version = 1
	}

	plaintext, err := json.MarshalIndent(secret, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	ciphertext, err := crypto.Encrypt(s.id, plaintext)
	crypto.Wipe(plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	return ciphertext, nil
}

// lookupName trims a name for locating an existing secret. Unlike
// NewSecretName it accepts names with an empty slug, which older versions
// stored at <group>/.age, so those secrets can still be opened, edited and
// deleted.
func lookupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("secret name cannot be empty")
	}
	if len(name) > 255 {
		return "", errors.New("secret name exceeds 255 bytes")
	}
	return name, nil
}

// path returns vault/<group>/<slug>.age for a (trimmed) name.
func (s *Store) path(name, group string) string {
	return filepath.Join(s.dir, NormalizeGroup(group), Slugify(name)+".age")
}

// Get decrypts and returns a secret by name and group.
func (s *Store) Get(_ context.Context, name string, group string) (*Secret, error) {
	name, err := lookupName(name)
	if err != nil {
		return nil, fmt.Errorf("store.Get: %w", err)
	}
	path := s.path(name, group)

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
	err = json.Unmarshal(plaintext, &secret)
	crypto.Wipe(plaintext)
	if err != nil {
		return nil, fmt.Errorf("store.Get %q: unmarshal: %w", name, err)
	}

	return &secret, nil
}

// Delete removes a secret from the vault by name and group.
func (s *Store) Delete(_ context.Context, name string, group string) error {
	name, err := lookupName(name)
	if err != nil {
		return fmt.Errorf("store.Delete: %w", err)
	}
	path := s.path(name, group)

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("store.Delete %q: %w", name, ErrSecretNotFound)
		}
		return fmt.Errorf("store.Delete %q: %w", name, err)
	}

	s.log.Info("secret deleted", "name", name, "group", NormalizeGroup(group))
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
	exists, err := Exists(destDir)
	if err != nil {
		return fmt.Errorf("store.CopyTo: %w", err)
	}
	if exists {
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
