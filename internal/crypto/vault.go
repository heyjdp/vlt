package crypto

import (
	"bytes"
	"fmt"
	"io"

	"filippo.io/age"
)

// Encrypt encrypts plaintext using the identity's scrypt recipient.
// Returns the age-encrypted ciphertext.
func Encrypt(id *Identity, plaintext []byte) ([]byte, error) {
	recipient, err := id.Recipient()
	if err != nil {
		return nil, fmt.Errorf("crypto.Encrypt: %w", err)
	}

	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	if err != nil {
		return nil, fmt.Errorf("crypto.Encrypt: %w", err)
	}

	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("crypto.Encrypt: write: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("crypto.Encrypt: close: %w", err)
	}

	return buf.Bytes(), nil
}

// Decrypt decrypts age-encrypted ciphertext using the identity.
// Returns the original plaintext.
func Decrypt(id *Identity, ciphertext []byte) ([]byte, error) {
	identity, err := id.ScryptIdentity()
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
