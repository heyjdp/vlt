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

	"github.com/yourorg/vlt/internal/crypto"
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

// beginMigration starts migrating the format-1 vault in dir.
func beginMigration(ctx context.Context, dir, passphrase string, log *slog.Logger, o options) (*Store, error) {
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
	err = writeFileExclusive(filepath.Join(dir, migratingFile), wrapped)
	if errors.Is(err, os.ErrExist) {
		id.Zero()
		existing, readErr := os.ReadFile(filepath.Join(dir, migratingFile))
		if readErr != nil {
			return nil, fmt.Errorf("store.migrate: %w", readErr)
		}
		return resumeMigration(ctx, dir, passphrase, existing, log, o)
	}
	if err != nil {
		id.Zero()
		return nil, fmt.Errorf("store.migrate: write %s: %w", migratingFile, err)
	}
	log.Info("migrating vault to format 2", "dir", dir)

	s := newStore(id, dir, log, o)
	if err := s.finishMigration(ctx, passphrase, wrapped); err != nil {
		id.Zero()
		return nil, err
	}
	return s, nil
}

// resumeMigration continues a migration interrupted after step 1.
func resumeMigration(ctx context.Context, dir, passphrase string, wrapped []byte, log *slog.Logger, o options) (*Store, error) {
	o.kdf()
	id, err := crypto.UnwrapIdentity(passphrase, wrapped)
	if err != nil {
		return nil, fmt.Errorf("store.Open: %w", err)
	}
	log.Info("resuming vault migration", "dir", dir)

	s := newStore(id, dir, log, o)
	if err := s.finishMigration(ctx, passphrase, wrapped); err != nil {
		id.Zero()
		return nil, err
	}
	return s, nil
}

// finishMigration runs steps 2–4.
func (s *Store) finishMigration(ctx context.Context, passphrase string, wrapped []byte) error {
	paths, err := secretFiles(s.dir)
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

	if err := writeFileAtomic(filepath.Join(s.dir, keyFile), wrapped); err != nil {
		return fmt.Errorf("store.migrate: replace %s: %w", keyFile, err)
	}
	if err := s.writeFormatMarker(); err != nil {
		return fmt.Errorf("store.migrate: %w", err)
	}
	if err := os.Remove(filepath.Join(s.dir, migratingFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store.migrate: remove %s: %w", migratingFile, err)
	}
	if err := syncDir(s.dir); err != nil {
		return fmt.Errorf("store.migrate: %w", err)
	}

	s.log.Info("vault migrated", "dir", s.dir, "migrated", migrated, "skipped", len(s.skipped))
	return nil
}

// migrateFile re-encrypts one legacy file to the vault key. It reports
// whether the file was rewritten. Already-migrated files are left alone, and
// files the passphrase cannot decrypt are recorded in s.skipped.
func (s *Store) migrateFile(passphrase, path string) (bool, error) {
	data, err := os.ReadFile(path)
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
		rel, relErr := filepath.Rel(s.dir, path)
		if relErr != nil {
			rel = path
		}
		s.log.Warn("cannot migrate secret; left untouched", "path", rel, "err", err)
		s.skipped = append(s.skipped, rel)
		return false, nil
	}

	ciphertext, err := crypto.Encrypt(s.id, plaintext)
	crypto.Wipe(plaintext)
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(path, ciphertext); err != nil {
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

// secretFiles returns the path of every .age file in a group directory of
// dir, in directory order. A subdirectory holding a format marker or a
// migration in progress is another vault, not a group, so its files are not
// counted, probed or migrated. Other nested vaults (format 1, or missing
// vlt.json) can't be told from a group by layout alone, because a secret
// named "vlt" is stored in its group as vlt.age; isSecretEnvelope keeps
// their files from being taken for secrets.
func secretFiles(dir string) ([]string, error) {
	groups, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, g := range groups {
		if !g.IsDir() {
			continue
		}
		groupDir := filepath.Join(dir, g.Name())
		entries, err := os.ReadDir(groupDir)
		if err != nil {
			return nil, err
		}
		if hasVaultMarker(entries) {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".age" {
				paths = append(paths, filepath.Join(groupDir, e.Name()))
			}
		}
	}
	return paths, nil
}

// hasVaultMarker reports whether a directory with these entries holds a
// format marker or a migration in progress. Neither name can be a secret's
// file, so such a directory is a vault, never a group.
func hasVaultMarker(entries []os.DirEntry) bool {
	for _, e := range entries {
		if e.Name() == formatFile || e.Name() == migratingFile {
			return true
		}
	}
	return false
}
