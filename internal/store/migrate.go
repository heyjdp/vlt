package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/heyjdp/vlt/internal/crypto"
)

// Migration from format 1 (every secret encrypted directly to the passphrase,
// vlt.age = Enc("unlocked")) to format 2 (secrets encrypted to a vault key).
//
// The steps are ordered so that a crash at any point leaves a vault that the
// next Open can finish migrating, and no secret is ever unreadable:
//
//  1. Generate the vault key and write it, wrapped, to vlt.migrating.age.
//     The write is exclusive; if the file already exists (another instance
//     got there first), migration resumes with that key instead.
//  2. Re-encrypt each secret to the vault key, one atomic write per file.
//     Files that already decrypt with the vault key are skipped, so step 2
//     is idempotent and resumable.
//  3. Atomically replace vlt.age (the legacy sentinel) with the wrapped key.
//  4. Write vlt.json, then delete vlt.migrating.age.
//
// Files that do not decrypt with the passphrase are left untouched and
// reported via Store.SkippedFiles: they were already unreadable, and
// migration must not destroy data it cannot read.

// beginMigration starts migrating the format-1 vault in fsys.
func beginMigration(ctx context.Context, fsys vaultFS, loc Location, passphrase string, log *slog.Logger, o options) (*Store, error) {
	id, err := crypto.GenerateIdentity()
	if err != nil {
		return nil, fmt.Errorf("store.migrate: %w", err)
	}
	wrapped, err := o.wrap(passphrase, id)
	if err != nil {
		id.Zero()
		return nil, fmt.Errorf("store.migrate: %w", err)
	}
	// Exclusive create: if another vlt instance started migrating first, use
	// its key rather than replacing it (which would strand its rewrites).
	err = fsys.WriteFileExclusive(migratingFile, wrapped)
	if errors.Is(err, fs.ErrExist) {
		id.Zero()
		existing, readErr := fsys.ReadFile(migratingFile)
		if readErr != nil {
			return nil, fmt.Errorf("store.migrate: %w", readErr)
		}
		return resumeMigration(ctx, fsys, loc, passphrase, existing, log, o)
	}
	if err != nil {
		id.Zero()
		return nil, fmt.Errorf("store.migrate: write %s: %w", migratingFile, err)
	}
	log.Info("migrating vault to format 2", "location", loc.String())

	s := newStore(id, fsys, loc, log, o)
	if err := s.finishMigration(ctx, passphrase, wrapped); err != nil {
		id.Zero()
		return nil, err
	}
	return s, nil
}

// resumeMigration continues a migration interrupted after step 1.
func resumeMigration(ctx context.Context, fsys vaultFS, loc Location, passphrase string, wrapped []byte, log *slog.Logger, o options) (*Store, error) {
	o.kdf()
	id, err := crypto.UnwrapIdentity(passphrase, wrapped)
	if err != nil {
		return nil, fmt.Errorf("store.Open: %w", err)
	}
	log.Info("resuming vault migration", "location", loc.String())

	s := newStore(id, fsys, loc, log, o)
	if err := s.finishMigration(ctx, passphrase, wrapped); err != nil {
		id.Zero()
		return nil, err
	}
	return s, nil
}

// finishMigration runs steps 2–4.
func (s *Store) finishMigration(ctx context.Context, passphrase string, wrapped []byte) error {
	paths, err := secretFiles(s.fs)
	if err != nil {
		return fmt.Errorf("store.migrate: %w", err)
	}

	migrated := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("store.migrate: %w", err)
		}
		done, err := s.migrateFile(passphrase, path)
		if err != nil {
			return fmt.Errorf("store.migrate %q: %w", path, err)
		}
		if done {
			migrated++
		}
	}

	if err := s.fs.WriteFileAtomic(keyFile, wrapped); err != nil {
		return fmt.Errorf("store.migrate: replace %s: %w", keyFile, err)
	}
	if err := s.writeFormatMarker(); err != nil {
		return fmt.Errorf("store.migrate: %w", err)
	}
	if err := s.fs.Remove(migratingFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("store.migrate: remove %s: %w", migratingFile, err)
	}
	if err := s.fs.SyncDir("."); err != nil {
		return fmt.Errorf("store.migrate: %w", err)
	}

	s.log.Info("vault migrated", "location", s.loc.String(), "migrated", migrated, "skipped", len(s.skipped))
	return nil
}

// migrateFile re-encrypts one legacy file to the vault key. It reports
// whether the file was rewritten. Already-migrated files are left alone, and
// files the passphrase cannot decrypt are recorded in s.skipped.
func (s *Store) migrateFile(passphrase, name string) (bool, error) {
	data, err := s.fs.ReadFile(name)
	if err != nil {
		return false, err
	}
	if probe, err := crypto.Decrypt(s.id, data); err == nil {
		crypto.Wipe(probe) // already migrated; the plaintext is not needed
		return false, nil
	}

	plaintext, err := s.opts.decryptWithPassphrase(passphrase, data)
	if err == nil && !isSecretEnvelope(plaintext) {
		// Another vault's sentinel or key, e.g. a vault kept in a
		// subfolder: re-encrypting it would lock that vault out.
		crypto.Wipe(plaintext)
		err = errNotASecret
	}
	if err != nil {
		s.log.Warn("cannot migrate secret; left untouched", "path", name, "err", err)
		s.skipped = append(s.skipped, name)
		return false, nil
	}

	ciphertext, err := crypto.Encrypt(s.id, plaintext)
	crypto.Wipe(plaintext)
	if err != nil {
		return false, err
	}
	if err := s.fs.WriteFileAtomic(name, ciphertext); err != nil {
		return false, err
	}
	return true, nil
}

// errNotASecret marks a .age file that decrypts but holds no secret.
var errNotASecret = errors.New("file decrypts to something other than a secret (another vault's key?)")

// isSecretEnvelope reports whether plaintext is a secret's JSON envelope.
// A vault's legacy sentinel ("unlocked") and its wrapped key (an age secret
// key) are not, so neither can pass for a secret when a passphrase is
// checked or a format-1 vault is migrated.
func isSecretEnvelope(plaintext []byte) bool {
	trimmed := bytes.TrimSpace(plaintext)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}
