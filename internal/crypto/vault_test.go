package crypto_test

import (
	"testing"

	"github.com/yourorg/vlt/internal/crypto"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase-encrypt")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	defer id.Zero()

	plaintext := []byte(`{"type":"password","name":"test","value":"secret123"}`)

	ciphertext, err := crypto.Encrypt(id, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if len(ciphertext) == 0 {
		t.Fatal("expected non-empty ciphertext")
	}

	got, err := crypto.Decrypt(id, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if string(got) != string(plaintext) {
		t.Fatalf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase-empty")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	defer id.Zero()

	ciphertext, err := crypto.Encrypt(id, []byte{})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got, err := crypto.Decrypt(id, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected empty plaintext, got %d bytes", len(got))
	}
}

func TestDecrypt_FailsWithWrongPassphrase(t *testing.T) {
	t.Parallel()

	id1, err := crypto.NewIdentity("passphrase-one")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	defer id1.Zero()

	id2, err := crypto.NewIdentity("passphrase-two")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	defer id2.Zero()

	plaintext := []byte("secret data")

	ciphertext, err := crypto.Encrypt(id1, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = crypto.Decrypt(id2, ciphertext)
	if err == nil {
		t.Fatal("expected error decrypting with wrong passphrase")
	}
}

func TestEncrypt_FailsWithZeroedIdentity(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	id.Zero()

	_, err = crypto.Encrypt(id, []byte("data"))
	if err == nil {
		t.Fatal("expected error encrypting with zeroed identity")
	}
}

func TestDecrypt_FailsWithZeroedIdentity(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	id.Zero()

	_, err = crypto.Decrypt(id, []byte("not-valid-ciphertext"))
	if err == nil {
		t.Fatal("expected error decrypting with zeroed identity")
	}
}

func TestDecrypt_FailsWithInvalidCiphertext(t *testing.T) {
	t.Parallel()

	id, err := crypto.NewIdentity("test-passphrase")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	defer id.Zero()

	_, err = crypto.Decrypt(id, []byte("not-age-ciphertext"))
	if err == nil {
		t.Fatal("expected error decrypting invalid ciphertext")
	}
}
