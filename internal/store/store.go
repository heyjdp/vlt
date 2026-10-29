package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/heyjdp/vlt/internal/crypto"
)

// ErrSecretNotFound is returned when a secret does not exist in the vault.
var ErrSecretNotFound = errors.New("secret not found")

// ErrSecretExists is returned by Create when the secret's file is already
// taken, and by Update when it is held by a differently named secret.
var ErrSecretExists = errors.New("a secret with that name already exists")

// Store manages secrets in an age-encrypted vault, in a local directory or
// on an SSH server. Secrets are stored at <group>/<slug>.age, encrypted to
// the vault key. Use Create or Open to obtain one; New is for callers that
// already hold the vault key. Its methods may be called concurrently.
type Store struct {
	fs  vaultFS
	loc Location
	id  *crypto.Identity
	log *slog.Logger

	opts options

	// skipped lists legacy files that could not be migrated (see migrate.go).
	skipped []string

	// lockMu guards lockToken, the token of the vault's lock file while this
	// Store holds it (see lock.go).
	lockMu    sync.Mutex
	lockToken string

	closeOnce sync.Once
	closed    chan struct{}
}

// New creates a Store over the local directory dir that encrypts and
// decrypts with the given vault key. It does not read or write vlt.age.
func New(id *crypto.Identity, dir string, log *slog.Logger) *Store {
	return newStore(id, localFS{root: dir}, Location{Path: dir}, log, options{})
}

func newStore(id *crypto.Identity, fsys vaultFS, loc Location, log *slog.Logger, o options) *Store {
	return &Store{
		fs:     fsys,
		loc:    loc,
		id:     id,
		log:    log,
		opts:   o,
		closed: make(chan struct{}),
	}
}

// Dir returns the vault's location, in the form ParseLocation reads.
func (s *Store) Dir() string {
	return s.loc.String()
}

// Location returns where the vault lives.
func (s *Store) Location() Location {
	return s.loc
}

// Init creates the vault directory with secure permissions.
func (s *Store) Init() error {
	if err := s.fs.MkdirAll("."); err != nil {
		return fmt.Errorf("store.Init: %w", err)
	}
	return nil
}

// Create encrypts and writes a new secret to vault/<group>/<slug>.age. It
// returns ErrSecretExists if any secret already occupies that path, including
// a different name with the same slug (e.g. "GitHub Key" and "github-key"),
// so it never overwrites. The name must pass NewSecretName. The secret's Name
// is trimmed in place and its Group set to GroupLabel(Group).
func (s *Store) Create(ctx context.Context, secret *Secret) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.Create: %w", err)
	}
	sn, err := NewSecretName(secret.Name)
	if err != nil {
		return fmt.Errorf("store.Create: %w", err)
	}
	secret.Name = sn.String()
	name := s.path(secret.Name, secret.Group)

	data, err := s.seal(secret)
	if err != nil {
		return fmt.Errorf("store.Create: %w", err)
	}
	if err := s.fs.MkdirAll(path.Dir(name)); err != nil {
		return fmt.Errorf("store.Create: mkdir: %w", err)
	}

	err = s.fs.WriteFileExclusive(name, data)
	if errors.Is(err, fs.ErrExist) {
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
// copy, and Group is set to GroupLabel(Group). Names that predate
// NewSecretName's rules are accepted here so legacy secrets stay editable.
func (s *Store) Update(ctx context.Context, secret *Secret) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.Update: %w", err)
	}
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
	if err := s.fs.WriteFileAtomic(s.path(name, secret.Group), data); err != nil {
		return fmt.Errorf("store.Update: write: %w", err)
	}

	s.log.Info("secret updated", "name", secret.Name, "group", secret.Group)
	return nil
}

// seal sets the secret's group label, stamps its timestamps, and returns its
// ciphertext. The caller has already validated and trimmed the name.
func (s *Store) seal(secret *Secret) ([]byte, error) {
	secret.Group = GroupLabel(secret.Group)
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

// path returns <group>/<slug>.age, relative to the vault, for a (trimmed)
// name. Slugs only hold [a-z0-9-], so the path cannot leave the vault.
func (s *Store) path(name, group string) string {
	return path.Join(NormalizeGroup(group), Slugify(name)+".age")
}

// Get decrypts and returns a secret by name and group.
func (s *Store) Get(ctx context.Context, name, group string) (*Secret, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("store.Get: %w", err)
	}
	name, err := lookupName(name)
	if err != nil {
		return nil, fmt.Errorf("store.Get: %w", err)
	}
	ciphertext, err := s.fs.ReadFile(s.path(name, group))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
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
func (s *Store) Delete(ctx context.Context, name, group string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.Delete: %w", err)
	}
	name, err := lookupName(name)
	if err != nil {
		return fmt.Errorf("store.Delete: %w", err)
	}
	if err := s.fs.Remove(s.path(name, group)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("store.Delete %q: %w", name, ErrSecretNotFound)
		}
		return fmt.Errorf("store.Delete %q: %w", name, err)
	}

	s.log.Info("secret deleted", "name", name, "group", NormalizeGroup(group))
	return nil
}

// listWorkers bounds how many secret files List reads at once. Reading in
// parallel hides the round trip to a server; locally it changes little.
const listWorkers = 8

// List returns all secrets in the vault by walking every group subdirectory,
// in directory order. Files that cannot be read, decrypted or parsed are
// logged and skipped. It checks ctx before each file and returns ctx's error
// once it is cancelled.
func (s *Store) List(ctx context.Context) ([]*Secret, error) {
	groups, err := s.fs.ReadDir(".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("store.List: %w", err)
	}

	var names []string
	for _, groupEntry := range groups {
		if !isGroupDir(groupEntry) {
			continue
		}
		entries, err := s.fs.ReadDir(groupEntry.Name())
		if err != nil {
			s.log.Warn("failed to read group directory", "group", groupEntry.Name(), "err", err)
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() && path.Ext(entry.Name()) == ".age" {
				names = append(names, path.Join(groupEntry.Name(), entry.Name()))
			}
		}
	}

	// Each worker fills its own slots, so the result keeps directory order.
	found := make([]*Secret, len(names))
	failed := make([]error, len(names))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(listWorkers, len(names)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				found[i], failed[i] = s.readSecret(names[i])
			}
		}()
	}
	var cancelled error
	for i := range names {
		if cancelled = ctx.Err(); cancelled != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if cancelled != nil {
		return nil, fmt.Errorf("store.List: %w", cancelled)
	}
	// A lost connection would otherwise leave secrets silently missing from
	// the list; fail instead, so the user sees why.
	for _, err := range failed {
		if errors.Is(err, ErrConnectionLost) || errors.Is(err, ErrUnreachable) {
			return nil, fmt.Errorf("store.List: %w", err)
		}
	}

	var secrets []*Secret
	for _, secret := range found {
		if secret != nil {
			secrets = append(secrets, secret)
		}
	}
	return secrets, nil
}

// readSecret reads and decrypts one secret file for List. A file that
// cannot be read, decrypted or parsed is logged and skipped (nil, nil); a
// lost connection is returned, so List can fail rather than show a partial
// list.
func (s *Store) readSecret(name string) (*Secret, error) {
	ciphertext, err := s.fs.ReadFile(name)
	if err != nil {
		if errors.Is(err, ErrConnectionLost) || errors.Is(err, ErrUnreachable) {
			return nil, err
		}
		s.log.Warn("failed to read secret file", "path", name, "err", err)
		return nil, nil
	}
	plaintext, err := crypto.Decrypt(s.id, ciphertext)
	if err != nil {
		s.log.Warn("failed to decrypt secret", "path", name, "err", err)
		return nil, nil
	}
	var secret Secret
	err = json.Unmarshal(plaintext, &secret)
	crypto.Wipe(plaintext)
	if err != nil {
		s.log.Warn("failed to unmarshal secret", "path", name, "err", err)
		return nil, nil
	}
	return &secret, nil
}

// CopyTo copies the wrapped vault key, the format marker, and every .age
// file into the vault location dest (local, or on a server), preserving the
// group subdirectory structure. It refuses (ErrVaultExists) to copy into a
// location that already holds a vault or secret files, since overwriting
// another vault's vlt.age would make its secrets unrecoverable. An open
// Store never has a migration in progress, so vlt.migrating.age is not
// copied, and the lock file belongs to this location, so it is not either.
func (s *Store) CopyTo(ctx context.Context, dest string) error {
	dst, loc, err := openLocation(ctx, dest, s.opts)
	if err != nil {
		return fmt.Errorf("store.CopyTo: %w", err)
	}
	defer func() { _ = dst.Close() }()

	exists, err := existsIn(dst)
	if err != nil {
		return fmt.Errorf("store.CopyTo: %w", err)
	}
	if exists {
		return fmt.Errorf("store.CopyTo %q: %w", loc, ErrVaultExists)
	}
	if err := dst.MkdirAll("."); err != nil {
		return fmt.Errorf("store.CopyTo: mkdir %q: %w", loc, err)
	}

	// Copy the vault-level files that exist
	for _, name := range []string{keyFile, formatFile} {
		data, err := s.fs.ReadFile(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("store.CopyTo: read %s: %w", name, err)
		}
		if err := dst.WriteFileAtomic(name, data); err != nil {
			return fmt.Errorf("store.CopyTo: write %s: %w", name, err)
		}
	}

	// Copy group subdirectories
	groups, err := s.fs.ReadDir(".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("store.CopyTo: read %q: %w", s.loc, err)
	}

	for _, groupEntry := range groups {
		if !isGroupDir(groupEntry) {
			continue
		}
		group := groupEntry.Name()
		if err := dst.MkdirAll(group); err != nil {
			return fmt.Errorf("store.CopyTo: mkdir %q: %w", group, err)
		}

		entries, err := s.fs.ReadDir(group)
		if err != nil {
			return fmt.Errorf("store.CopyTo: read %q: %w", group, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || path.Ext(entry.Name()) != ".age" {
				continue
			}
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("store.CopyTo: %w", err)
			}
			name := path.Join(group, entry.Name())
			data, err := s.fs.ReadFile(name)
			if err != nil {
				return fmt.Errorf("store.CopyTo: read file %q: %w", entry.Name(), err)
			}
			if err := dst.WriteFileAtomic(name, data); err != nil {
				return fmt.Errorf("store.CopyTo: write file %q: %w", entry.Name(), err)
			}
		}
	}

	s.log.Info("vault copied", "from", s.loc.String(), "to", loc.String())
	return nil
}
