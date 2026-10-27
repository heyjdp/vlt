package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	"github.com/yourorg/vlt/internal/crypto"
)

// Vault-level files in the vault root. Group directories hold the secrets.
const (
	// keyFile holds the vault key, encrypted to the passphrase (format 2), or
	// the encrypted word "unlocked" (legacy format 1).
	keyFile = "vlt.age"
	// formatFile records the on-disk format version.
	formatFile = "vlt.json"
	// migratingFile holds the new wrapped vault key while a format-1 vault is
	// being migrated. Its presence means a migration is in progress.
	migratingFile = "vlt.migrating.age"
	// legacySentinel is the plaintext of a format-1 vlt.age.
	legacySentinel = "unlocked"
	// legacyProbeLimit caps how many secret files Open tries when proving a
	// passphrase against a format-1 vault that has lost its vlt.age.
	legacyProbeLimit = 3
)

// FormatVersion is the on-disk format this build writes.
const FormatVersion = 2

// ErrWrongPassphrase is returned when the passphrase cannot decrypt vlt.age.
var ErrWrongPassphrase = crypto.ErrWrongPassphrase

// ErrVaultNotFound is returned by Open when dir holds no vault.
var ErrVaultNotFound = errors.New("vault not found")

// ErrVaultExists is returned by Create and CopyTo when the target directory
// already holds a vault or secret files.
var ErrVaultExists = errors.New("vault already exists")

// ErrKeyMissing is returned by Open when a format-2 vault has lost vlt.age.
// Its secrets cannot be decrypted without restoring vlt.age from a backup.
var ErrKeyMissing = errors.New("vault key file vlt.age is missing")

// ErrUnsupportedFormat is returned by Open for a vault written by a newer
// version of vlt.
var ErrUnsupportedFormat = errors.New("vault format is newer than this version of vlt supports")

// Option configures Create and Open.
type Option func(*options)

// options holds the settings Options apply.
type options struct {
	workFactor int
	// onKDF, when set, is called before every scrypt derivation. It exists
	// so internal tests can count derivations.
	onKDF func()
}

// WithWorkFactor sets the scrypt work factor (log2 N) used when wrapping the
// vault key. The default is crypto.DefaultWorkFactor; lower values are for
// tests only.
func WithWorkFactor(logN int) Option {
	return func(o *options) { o.workFactor = logN }
}

func buildOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// kdf records a scrypt derivation for instrumentation.
func (o options) kdf() {
	if o.onKDF != nil {
		o.onKDF()
	}
}

// decryptWithPassphrase runs crypto.DecryptWithPassphrase (one scrypt).
func (o options) decryptWithPassphrase(passphrase string, data []byte) ([]byte, error) {
	o.kdf()
	return crypto.DecryptWithPassphrase(passphrase, data) //nolint:wrapcheck // callers wrap
}

// wrap runs crypto.WrapIdentity (one scrypt) at the configured work factor.
func (o options) wrap(passphrase string, id *crypto.Identity) ([]byte, error) {
	o.kdf()
	return crypto.WrapIdentity(passphrase, id, o.workFactor) //nolint:wrapcheck // callers wrap
}

// formatMarker is the JSON schema of vlt.json.
type formatMarker struct {
	Format int `json:"format"`
}

// Exists reports whether dir contains a vault: a wrapped key or legacy
// sentinel, an in-progress migration, a format marker, or any secret file.
// A directory for which Exists is true must be opened, never created over.
// A missing dir is not an error. Any other error (e.g. permission denied on
// dir or a group directory) is returned, because "cannot tell" must not be
// treated as "no vault" and lead to creating one over existing secrets.
func Exists(dir string) (bool, error) {
	for _, name := range []string{keyFile, migratingFile, formatFile} {
		_, err := os.Stat(filepath.Join(dir, name))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return false, fmt.Errorf("store.Exists: %w", err)
		}
	}
	paths, err := secretFiles(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store.Exists: %w", err)
	}
	return len(paths) > 0, nil
}

// Create initialises a new vault in dir: it generates a random vault key,
// stores it encrypted to passphrase in vlt.age, and writes the format marker.
// It returns ErrVaultExists if dir already holds a vault or secret files.
func Create(dir, passphrase string, log *slog.Logger, opts ...Option) (*Store, error) {
	exists, err := Exists(dir)
	if err != nil {
		return nil, fmt.Errorf("store.Create: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("store.Create %q: %w", dir, ErrVaultExists)
	}
	if err := mkdirAllSynced(dir); err != nil {
		return nil, fmt.Errorf("store.Create: %w", err)
	}

	o := buildOptions(opts)
	id, err := crypto.GenerateIdentity()
	if err != nil {
		return nil, fmt.Errorf("store.Create: %w", err)
	}
	wrapped, err := o.wrap(passphrase, id)
	if err != nil {
		id.Zero()
		return nil, fmt.Errorf("store.Create: %w", err)
	}

	s := newStore(id, dir, log, o)
	// Exclusive: another instance creating a vault here at the same moment
	// must not have its vlt.age replaced.
	err = writeFileExclusive(filepath.Join(dir, keyFile), wrapped)
	if errors.Is(err, os.ErrExist) {
		id.Zero()
		return nil, fmt.Errorf("store.Create %q: %w", dir, ErrVaultExists)
	}
	if err != nil {
		id.Zero()
		return nil, fmt.Errorf("store.Create: write key: %w", err)
	}
	if err := s.writeFormatMarker(); err != nil {
		id.Zero()
		return nil, fmt.Errorf("store.Create: %w", err)
	}

	log.Info("vault created", "dir", dir, "format", FormatVersion)
	return s, nil
}

// Open unlocks the vault in dir with passphrase. A format-2 vault costs
// exactly one scrypt derivation. A legacy format-1 vault is migrated in place
// first, costing about one scrypt per secret, once; an interrupted migration
// is resumed. Open returns ErrWrongPassphrase if the passphrase is incorrect,
// ErrVaultNotFound if dir holds no vault, ErrKeyMissing if a format-2 vault
// has lost vlt.age, and ErrUnsupportedFormat for a newer format.
func Open(ctx context.Context, dir, passphrase string, log *slog.Logger, opts ...Option) (*Store, error) {
	o := buildOptions(opts)

	format, err := readFormat(dir)
	if err != nil {
		return nil, fmt.Errorf("store.Open: %w", err)
	}
	if format > FormatVersion {
		return nil, fmt.Errorf("store.Open: format %d: %w", format, ErrUnsupportedFormat)
	}

	if wrapped, err := os.ReadFile(filepath.Join(dir, migratingFile)); err == nil {
		return resumeMigration(ctx, dir, passphrase, wrapped, log, o)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("store.Open: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, keyFile))
	if errors.Is(err, os.ErrNotExist) {
		return openWithoutKeyFile(ctx, dir, passphrase, format, log, o)
	}
	if err != nil {
		return nil, fmt.Errorf("store.Open: %w", err)
	}

	plaintext, err := o.decryptWithPassphrase(passphrase, data)
	if err != nil {
		return nil, fmt.Errorf("store.Open: %w", err)
	}

	switch {
	case crypto.IsSecretKey(plaintext):
		id, err := crypto.ParseIdentity(plaintext)
		if err != nil {
			return nil, fmt.Errorf("store.Open: %w", err)
		}
		s := newStore(id, dir, log, o)
		if format != FormatVersion {
			// vlt.json is missing or stale (lost, or not copied); restore it.
			if err := s.writeFormatMarker(); err != nil {
				id.Zero()
				return nil, fmt.Errorf("store.Open: %w", err)
			}
		}
		return s, nil

	case string(plaintext) == legacySentinel:
		crypto.Wipe(plaintext)
		return beginMigration(ctx, dir, passphrase, log, o)

	default:
		crypto.Wipe(plaintext)
		return nil, fmt.Errorf("store.Open: %s has unrecognised contents", keyFile)
	}
}

// openWithoutKeyFile handles a directory that has no vlt.age. Format-1 vaults
// copied by early versions of vlt lost their sentinel; if any secret decrypts
// with passphrase, the passphrase is proven and the vault is migrated.
func openWithoutKeyFile(ctx context.Context, dir, passphrase string, format int, log *slog.Logger, o options) (*Store, error) {
	if format == FormatVersion {
		return nil, fmt.Errorf("store.Open %q: %w", dir, ErrKeyMissing)
	}
	paths, err := secretFiles(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("store.Open: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("store.Open %q: %w", dir, ErrVaultNotFound)
	}

	passphraseFiles := 0
	for i, path := range paths {
		if i == legacyProbeLimit {
			break
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("store.Open: %w", err)
		}
		if !encryptedToPassphrase(data) {
			continue // a format-2 secret, encrypted to a vault key
		}
		passphraseFiles++
		plaintext, err := o.decryptWithPassphrase(passphrase, data)
		if err != nil {
			continue
		}
		isSecret := isSecretEnvelope(plaintext)
		crypto.Wipe(plaintext)
		if !isSecret {
			continue // another vault's sentinel or key proves nothing about this folder
		}
		log.Info("format-1 vault without vlt.age; passphrase verified against a secret", "dir", dir)
		return beginMigration(ctx, dir, passphrase, log, o)
	}
	if passphraseFiles == 0 {
		// Only vault-key secrets: a format-2 vault that lost both vlt.age
		// and vlt.json. No passphrase can open it, so don't call it wrong.
		return nil, fmt.Errorf("store.Open %q: %w", dir, ErrKeyMissing)
	}
	return nil, fmt.Errorf("store.Open: %w", ErrWrongPassphrase)
}

// encryptedToPassphrase reports whether an age file has a scrypt
// (passphrase) recipient stanza in its header, as format-1 secrets do.
// Format-2 secrets have only an X25519 stanza.
func encryptedToPassphrase(data []byte) bool {
	header, _, _ := bytes.Cut(data, []byte("\n---"))
	return bytes.Contains(header, []byte("\n-> scrypt "))
}

// ChangePassphrase re-encrypts the vault key from oldPass to newPass. Only
// vlt.age is rewritten, atomically, so an interrupted change leaves the old
// passphrase working. It returns ErrWrongPassphrase if oldPass is incorrect.
// ctx is checked before the final write.
func (s *Store) ChangePassphrase(ctx context.Context, oldPass, newPass string) error {
	data, err := os.ReadFile(filepath.Join(s.dir, keyFile))
	if err != nil {
		return fmt.Errorf("store.ChangePassphrase: %w", err)
	}
	s.opts.kdf()
	current, err := crypto.UnwrapIdentity(oldPass, data)
	if err != nil {
		return fmt.Errorf("store.ChangePassphrase: %w", err)
	}
	defer current.Zero()

	same, err := sameKey(current, s.id)
	if err != nil {
		return fmt.Errorf("store.ChangePassphrase: %w", err)
	}
	if !same {
		return errors.New("store.ChangePassphrase: vlt.age does not hold this vault's key")
	}

	wrapped, err := s.opts.wrap(newPass, s.id)
	if err != nil {
		return fmt.Errorf("store.ChangePassphrase: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.ChangePassphrase: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(s.dir, keyFile), wrapped); err != nil {
		return fmt.Errorf("store.ChangePassphrase: %w", err)
	}
	s.log.Info("passphrase changed", "dir", s.dir)
	return nil
}

// Close zeroes the vault key. The Store must not be used afterwards.
func (s *Store) Close() {
	s.id.Zero()
}

// SkippedFiles lists legacy secret files, relative to the vault directory,
// that could not be decrypted with the passphrase during migration and were
// left untouched.
func (s *Store) SkippedFiles() []string {
	return s.skipped
}

// sameKey reports whether two identities share a public recipient.
func sameKey(a, b *crypto.Identity) (bool, error) {
	ra, err := a.Recipient()
	if err != nil {
		return false, err
	}
	rb, err := b.Recipient()
	if err != nil {
		return false, err
	}
	return ra.String() == rb.String(), nil
}

// readFormat returns the format recorded in dir/vlt.json, or 0 if absent.
func readFormat(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, formatFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var m formatMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return 0, fmt.Errorf("parse %s: %w", formatFile, err)
	}
	return m.Format, nil
}

// hasFormatMarker reports whether vlt.json records the current format.
func (s *Store) hasFormatMarker() bool {
	f, err := readFormat(s.dir)
	return err == nil && f == FormatVersion
}

// writeFormatMarker writes vlt.json for the current format.
func (s *Store) writeFormatMarker() error {
	data, err := json.Marshal(formatMarker{Format: FormatVersion})
	if err != nil {
		return fmt.Errorf("marshal format marker: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(s.dir, formatFile), append(data, '\n')); err != nil {
		return fmt.Errorf("write format marker: %w", err)
	}
	return nil
}
