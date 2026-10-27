package crypto

import (
	"bytes"
	"fmt"
	"io"

	"filippo.io/age"
)

// Encrypt encrypts plaintext to the vault key's X25519 recipient.
// It runs no key derivation, so it is cheap enough to call per secret.
func Encrypt(id *Identity, plaintext []byte) ([]byte, error) {
	recipient, err := id.Recipient()
	if err != nil {
		return nil, fmt.Errorf("crypto.Encrypt: %w", err)
	}
	out, err := encryptTo(recipient, plaintext)
	if err != nil {
		return nil, fmt.Errorf("crypto.Encrypt: %w", err)
	}
	return out, nil
}

// Decrypt decrypts ciphertext that was encrypted to the vault key.
func Decrypt(id *Identity, ciphertext []byte) ([]byte, error) {
	identity, err := id.x25519()
	if err != nil {
		return nil, fmt.Errorf("crypto.Decrypt: %w", err)
	}

	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return nil, fmt.Errorf("crypto.Decrypt: %w", err)
	}

	plaintext, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("crypto.Decrypt: read: %w", err)
	}

	return plaintext, nil
}

// encryptTo encrypts plaintext to a single age recipient.
func encryptTo(r age.Recipient, plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close: %w", err)
	}
	return buf.Bytes(), nil
}
