// Package testutil provides shared test helpers and fake implementations.
package testutil

import (
	"testing"
	"time"

	"github.com/yourorg/vlt/internal/crypto"
	"github.com/yourorg/vlt/internal/store"
)

// NewTestIdentity creates a crypto.Identity for testing.
// The identity is automatically zeroed when the test completes.
func NewTestIdentity(t *testing.T, passphrase string) *crypto.Identity {
	t.Helper()
	id, err := crypto.NewIdentity(passphrase)
	if err != nil {
		t.Fatalf("testutil.NewTestIdentity: %v", err)
	}
	t.Cleanup(func() { id.Zero() })
	return id
}

// PasswordSecret creates a test password secret.
func PasswordSecret(name, value string) *store.Secret {
	now := time.Now().UTC()
	return &store.Secret{
		Version:    1,
		Type:       store.TypePassword,
		Name:       name,
		CreatedAt:  now,
		ModifiedAt: now,
		Value:      value,
	}
}

// RecordSecret creates a test record secret.
func RecordSecret(name, username, password, url string) *store.Secret {
	now := time.Now().UTC()
	return &store.Secret{
		Version:    1,
		Type:       store.TypeRecord,
		Name:       name,
		CreatedAt:  now,
		ModifiedAt: now,
		Username:   username,
		Password:   password,
		URL:        url,
	}
}

// FileSecret creates a test file secret.
func FileSecret(name, filename, value string) *store.Secret {
	now := time.Now().UTC()
	return &store.Secret{
		Version:          1,
		Type:             store.TypeFile,
		Name:             name,
		CreatedAt:        now,
		ModifiedAt:       now,
		OriginalFilename: filename,
		MIMEType:         "application/octet-stream",
		Encoding:         "base64",
		Value:            value,
	}
}
