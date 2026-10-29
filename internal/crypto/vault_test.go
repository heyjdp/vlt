package crypto_test

import (
	"bytes"
	"errors"
	"testing"

	"filippo.io/age"

	"github.com/heyjdp/vlt/internal/crypto"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Parallel()

	id := newID(t)
	plaintext := []byte(`{"type":"password","name":"test","value":"secret123"}`)

	ciphertext, err := crypto.Encrypt(id, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := crypto.Decrypt(id, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestEncrypt_UsesX25519NotScrypt(t *testing.T) {
	t.Parallel()

	ciphertext, err := crypto.Encrypt(newID(t), []byte("x"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !bytes.Contains(ciphertext, []byte("\n-> X25519 ")) || bytes.Contains(ciphertext, []byte("\n-> scrypt ")) {
		header, _, _ := bytes.Cut(ciphertext, []byte("---"))
		t.Fatalf("secret should be encrypted to an X25519 recipient only:\n%s", header)
	}
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	t.Parallel()

	id := newID(t)
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

func TestDecrypt_FailsWithOtherKey(t *testing.T) {
	t.Parallel()

	ciphertext, err := crypto.Encrypt(newID(t), []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := crypto.Decrypt(newID(t), ciphertext); err == nil {
		t.Fatal("expected decryption with a different key to fail")
	}
}

func TestEncrypt_FailsWithZeroedIdentity(t *testing.T) {
	t.Parallel()

	id := newID(t)
	id.Zero()
	if _, err := crypto.Encrypt(id, []byte("x")); !errors.Is(err, crypto.ErrIdentityZeroed) {
		t.Fatalf("expected ErrIdentityZeroed, got %v", err)
	}
}

func TestDecrypt_FailsWithZeroedIdentity(t *testing.T) {
	t.Parallel()

	id := newID(t)
	ciphertext, err := crypto.Encrypt(id, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	id.Zero()
	if _, err := crypto.Decrypt(id, ciphertext); !errors.Is(err, crypto.ErrIdentityZeroed) {
		t.Fatalf("expected ErrIdentityZeroed, got %v", err)
	}
}

func TestDecrypt_FailsWithInvalidCiphertext(t *testing.T) {
	t.Parallel()

	if _, err := crypto.Decrypt(newID(t), []byte("not age")); err == nil {
		t.Fatal("expected error for invalid ciphertext")
	}
}

func TestDecryptWithPassphrase_LegacyFile(t *testing.T) {
	t.Parallel()

	r, err := age.NewScryptRecipient("legacy-pass")
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(10)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("unlocked")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := crypto.DecryptWithPassphrase("legacy-pass", buf.Bytes())
	if err != nil {
		t.Fatalf("DecryptWithPassphrase: %v", err)
	}
	if string(got) != "unlocked" {
		t.Fatalf("got %q", got)
	}
	if _, err := crypto.DecryptWithPassphrase("nope", buf.Bytes()); !errors.Is(err, crypto.ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase, got %v", err)
	}
}
