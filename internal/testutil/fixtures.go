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
