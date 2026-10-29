// Package testutil provides shared test helpers and fake implementations.
package testutil

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/heyjdp/vlt/internal/crypto"
	"github.com/heyjdp/vlt/internal/store"
)

// legacyWorkFactor keeps scrypt fast when building format-1 fixtures.
// age's scrypt identity accepts any work factor up to its maximum.
const legacyWorkFactor = 10

// NewTestIdentity generates a random vault key for testing.
// The identity is automatically zeroed when the test completes.
func NewTestIdentity(t *testing.T) *crypto.Identity {
	t.Helper()
	id, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatalf("testutil.NewTestIdentity: %v", err)
	}
	t.Cleanup(func() { id.Zero() })
	return id
}

// EncryptWithPassphrase encrypts plaintext directly to passphrase, as a
// format-1 vault did, using a low work factor for speed.
func EncryptWithPassphrase(t *testing.T, passphrase string, plaintext []byte) []byte {
	t.Helper()
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		t.Fatalf("testutil.EncryptWithPassphrase: %v", err)
	}
	r.SetWorkFactor(legacyWorkFactor)

	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		t.Fatalf("testutil.EncryptWithPassphrase: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("testutil.EncryptWithPassphrase: write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("testutil.EncryptWithPassphrase: close: %v", err)
	}
	return buf.Bytes()
}

// WriteLegacyVault writes a format-1 vault to dir: vlt.age holds the
// passphrase-encrypted word "unlocked" and each secret is encrypted directly
// to the passphrase at <group>/<slug>.age. It returns the secret file paths.
func WriteLegacyVault(t *testing.T, dir, passphrase string, secrets ...*store.Secret) []string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("testutil.WriteLegacyVault: %v", err)
	}
	sentinel := EncryptWithPassphrase(t, passphrase, []byte("unlocked"))
	if err := os.WriteFile(filepath.Join(dir, "vlt.age"), sentinel, 0o600); err != nil {
		t.Fatalf("testutil.WriteLegacyVault: %v", err)
	}

	paths := make([]string, 0, len(secrets))
	for _, sec := range secrets {
		data, err := json.Marshal(sec)
		if err != nil {
			t.Fatalf("testutil.WriteLegacyVault: marshal: %v", err)
		}
		groupDir := filepath.Join(dir, store.NormalizeGroup(sec.Group))
		if err := os.MkdirAll(groupDir, 0o700); err != nil {
			t.Fatalf("testutil.WriteLegacyVault: %v", err)
		}
		path := filepath.Join(groupDir, store.Slugify(sec.Name)+".age")
		if err := os.WriteFile(path, EncryptWithPassphrase(t, passphrase, data), 0o600); err != nil {
			t.Fatalf("testutil.WriteLegacyVault: %v", err)
		}
		paths = append(paths, path)
	}
	return paths
}

// PasswordSecret creates a test password secret in the given group.
func PasswordSecret(name, value, group string) *store.Secret {
	now := time.Now().UTC()
	return &store.Secret{
		Version:    1,
		Type:       store.TypePassword,
		Name:       name,
		Group:      group,
		CreatedAt:  now,
		ModifiedAt: now,
		Value:      value,
	}
}

// RecordSecret creates a test record secret in the given group.
func RecordSecret(name, username, password, url, group string) *store.Secret {
	now := time.Now().UTC()
	return &store.Secret{
		Version:    1,
		Type:       store.TypeRecord,
		Name:       name,
		Group:      group,
		CreatedAt:  now,
		ModifiedAt: now,
		Username:   username,
		Password:   password,
		URL:        url,
	}
}

// FileSecret creates a test file secret in the given group.
func FileSecret(name, filename, value, group string) *store.Secret {
	now := time.Now().UTC()
	return &store.Secret{
		Version:          1,
		Type:             store.TypeFile,
		Name:             name,
		Group:            group,
		CreatedAt:        now,
		ModifiedAt:       now,
		OriginalFilename: filename,
		MIMEType:         "application/octet-stream",
		Encoding:         "base64",
		Value:            value,
	}
}
