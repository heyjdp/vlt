package crypto_test

import (
	"testing"

	"github.com/yourorg/vlt/internal/crypto"
)

func TestNewIdentity_CreatesIdentityFromPassphrase(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	defer id.Zero()

	if id == nil {
		t.Fatal("expected non-nil identity")
	}
	if id.IsZeroed() {
		t.Fatal("new identity should not be zeroed")
	}
}

func TestNewIdentity_RejectsEmptyPassphrase(t *testing.T) {
	t.Parallel()

	_, err := crypto.NewIdentity("")
	if err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

func TestIdentity_Zero_ClearsBuffer(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase")
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}

	id.Zero()

	if !id.IsZeroed() {
		t.Fatal("expected identity to be zeroed")
	}
}

func TestIdentity_Zero_IsIdempotent(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase")
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}

	id.Zero()
	id.Zero() // should not panic
}

func TestIdentity_ScryptIdentity_ReturnsErrorAfterZero(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase")
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	id.Zero()

	_, err = id.ScryptIdentity()
	if err == nil {
		t.Fatal("expected error from zeroed identity")
	}
}

func TestIdentity_Recipient_ReturnsErrorAfterZero(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase")
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	id.Zero()

	_, err = id.Recipient()
	if err == nil {
		t.Fatal("expected error from zeroed identity")
	}
}

func TestIdentity_Recipient_ReturnsValidRecipient(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase")
	if err != nil {
		t.Fatalf("NewIdentity returned error: %v", err)
	}
	defer id.Zero()

	r, err := id.Recipient()
	if err != nil {
		t.Fatalf("Recipient returned error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil recipient")
	}
}
