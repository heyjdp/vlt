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

// tamperings returns ways an attacker who can write a vault file might alter
// a valid age file. Every one must be refused, never decrypted, even in part.
func tamperings(t *testing.T, good []byte) map[string][]byte {
	t.Helper()
	// The header ends with the line "--- <MAC>\n"; the payload (a 16-byte
	// nonce, then 64 KiB chunks each with a 16-byte tag) follows it.
	mac := bytes.Index(good, []byte("\n--- "))
	if mac < 0 {
		t.Fatal("no header MAC line in ciphertext")
	}
	header := mac + 1 + bytes.IndexByte(good[mac+1:], '\n') + 1
	const nonce, chunk = 16, 64*1024 + 16

	flip := func(i int) []byte {
		b := bytes.Clone(good)
		b[i] ^= 0x01
		return b
	}
	// The MAC is base64; swap one character for another valid one, so the
	// header still parses and only the MAC check can refuse it.
	badMAC := bytes.Clone(good)
	if i := mac + 6; badMAC[i] == 'A' {
		badMAC[i] = 'B'
	} else {
		badMAC[i] = 'A'
	}
	// Not covered: altering an scrypt stanza, which age (rightly) can't tell
	// from a wrong passphrase.
	cases := map[string][]byte{
		"the last byte flipped":          flip(len(good) - 1),
		"a payload byte flipped":         flip(header + nonce + 100),
		"a header MAC character changed": badMAC,
		"a nonce byte flipped":           flip(header),
		"the last 16 bytes cut off":      good[:len(good)-16],
		"a byte appended":                append(bytes.Clone(good), 0),
		"only the header and the nonce":  good[:header+nonce],
		"only the header":                good[:header],
		"empty":                          {},
	}
	if len(good) > header+nonce+chunk {
		cases["the final chunk cut off"] = good[:header+nonce+chunk]
	}
	return cases
}

// largePlaintext is just over one age chunk, so a file can lose its whole
// final chunk.
func largePlaintext() []byte {
	return bytes.Repeat([]byte("secret!"), 10*1024)
}

func TestDecrypt_RejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	id := newID(t)
	good, err := crypto.Encrypt(id, largePlaintext())
	if err != nil {
		t.Fatal(err)
	}
	cases := tamperings(t, good)
	if _, ok := cases["the final chunk cut off"]; !ok {
		t.Fatal("the plaintext should span two chunks")
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := crypto.Decrypt(id, bad)
			if err == nil || got != nil {
				t.Fatalf("Decrypt = %d bytes, %v; want nothing and an error", len(got), err)
			}
		})
	}
}

func TestDecryptWithPassphrase_RejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	r, err := age.NewScryptRecipient("right-pass")
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(10)
	good, err := encryptToRecipient(r, largePlaintext())
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range tamperings(t, good) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := crypto.DecryptWithPassphrase("right-pass", bad)
			if err == nil || got != nil {
				t.Fatalf("DecryptWithPassphrase = %d bytes, %v; want nothing and an error", len(got), err)
			}
			if errors.Is(err, crypto.ErrWrongPassphrase) {
				t.Fatalf("a damaged file with the right passphrase is reported as a wrong passphrase: %v", err)
			}
		})
	}
}

func encryptToRecipient(r age.Recipient, plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
