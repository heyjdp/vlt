// Package crypto provides age-based encryption primitives for the vlt secret manager.
//
// A vault is protected by a single random X25519 identity (the vault key).
// Secrets are encrypted to that key, which is cheap. The vault key itself is
// stored encrypted to the user's passphrase with age's scrypt recipient, so
// unlocking costs exactly one scrypt derivation regardless of vault size.
package crypto

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"filippo.io/age"
	"github.com/awnumar/memguard"
)

// ErrWrongPassphrase is returned when a passphrase cannot decrypt a wrapped key.
var ErrWrongPassphrase = errors.New("wrong passphrase")

// ErrIdentityZeroed is returned when an Identity is used after Zero.
var ErrIdentityZeroed = errors.New("identity has been zeroed")

// errEmptyPassphrase is returned for an empty passphrase.
var errEmptyPassphrase = errors.New("passphrase cannot be empty")

// DefaultWorkFactor is the scrypt work factor (log2 N) used to wrap the vault
// key: N = 2^18 with r = 8 costs 256 MiB and roughly a second per derivation.
const DefaultWorkFactor = 18

// MaxWorkFactor is the highest scrypt work factor DecryptWithPassphrase
// accepts: 2^20 costs 1 GiB and a few seconds. Whoever can write vlt.age (a
// server, a shared folder) could otherwise set one that takes gigabytes of
// memory and many seconds for every unlock, or for every file a migration
// reads. vlt writes DefaultWorkFactor.
const MaxWorkFactor = 20

// ErrWorkFactorTooHigh is returned for a file whose scrypt work factor is
// above MaxWorkFactor; no key derivation is run for it.
var ErrWorkFactorTooHigh = errors.New("the passphrase-protected file asks for more key-derivation work than vlt allows")

// Identity is a vault key: an age X25519 identity whose encoded secret key is
// held in a memguard LockedBuffer (mlock'd, guard pages). Zero destroys that
// buffer when the vault is locked. Protection is best-effort: age's parsed
// X25519Identity, and transient string copies made while parsing and
// generating, live on the ordinary Go heap until garbage-collected, and Go
// provides no way to wipe them.
type Identity struct {
	mu     sync.Mutex
	buf    *memguard.LockedBuffer
	parsed *age.X25519Identity
}

// GenerateIdentity creates a new random vault key.
func GenerateIdentity() (*Identity, error) {
	x, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("crypto.GenerateIdentity: %w", err)
	}
	return ParseIdentity([]byte(x.String()))
}

// ParseIdentity builds a vault key from an "AGE-SECRET-KEY-1..." string.
// secretKey is moved into a LockedBuffer and the caller's slice is wiped.
func ParseIdentity(secretKey []byte) (*Identity, error) {
	buf := memguard.NewBufferFromBytes(secretKey)
	x, err := age.ParseX25519Identity(string(buf.Bytes()))
	if err != nil {
		buf.Destroy()
		return nil, fmt.Errorf("crypto.ParseIdentity: %w", err)
	}
	return &Identity{buf: buf, parsed: x}, nil
}

// x25519 returns the parsed identity, or ErrIdentityZeroed.
func (id *Identity) x25519() (*age.X25519Identity, error) {
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.parsed == nil {
		return nil, ErrIdentityZeroed
	}
	return id.parsed, nil
}

// Recipient returns the public recipient secrets are encrypted to.
func (id *Identity) Recipient() (*age.X25519Recipient, error) {
	x, err := id.x25519()
	if err != nil {
		return nil, fmt.Errorf("crypto.Identity.Recipient: %w", err)
	}
	return x.Recipient(), nil
}

// Zero destroys the secure buffer and marks the identity as unusable.
// It is safe to call more than once.
func (id *Identity) Zero() {
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.parsed != nil {
		id.buf.Destroy()
		id.parsed = nil
	}
}

// WrapIdentity encrypts the vault key to passphrase with age's scrypt
// recipient at the given work factor (log2 N; 0 means DefaultWorkFactor).
// The result is what gets stored on disk as vlt.age. Lower work factors are
// for tests only. A factor above MaxWorkFactor is refused, since
// DecryptWithPassphrase would never open the result.
func WrapIdentity(passphrase string, id *Identity, workFactor int) ([]byte, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("crypto.WrapIdentity: %w", errEmptyPassphrase)
	}
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("crypto.WrapIdentity: %w", err)
	}
	if workFactor <= 0 {
		workFactor = DefaultWorkFactor
	}
	if workFactor > MaxWorkFactor {
		return nil, fmt.Errorf("crypto.WrapIdentity: %w (2^%d; at most 2^%d)", ErrWorkFactorTooHigh, workFactor, MaxWorkFactor)
	}
	r.SetWorkFactor(workFactor)

	id.mu.Lock()
	defer id.mu.Unlock()
	if id.parsed == nil {
		return nil, fmt.Errorf("crypto.WrapIdentity: %w", ErrIdentityZeroed)
	}
	out, err := encryptTo(r, id.buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("crypto.WrapIdentity: %w", err)
	}
	return out, nil
}

// UnwrapIdentity decrypts a wrapped vault key with passphrase.
// It returns ErrWrongPassphrase if the passphrase does not match.
func UnwrapIdentity(passphrase string, wrapped []byte) (*Identity, error) {
	plaintext, err := DecryptWithPassphrase(passphrase, wrapped)
	if err != nil {
		return nil, fmt.Errorf("crypto.UnwrapIdentity: %w", err)
	}
	if !IsSecretKey(plaintext) {
		Wipe(plaintext)
		return nil, fmt.Errorf("crypto.UnwrapIdentity: %w", errors.New("wrapped payload is not an age secret key"))
	}
	id, err := ParseIdentity(plaintext)
	if err != nil {
		return nil, fmt.Errorf("crypto.UnwrapIdentity: %w", err)
	}
	return id, nil
}

// IsSecretKey reports whether plaintext looks like an age X25519 secret key,
// which distinguishes a format-2 vlt.age from a legacy sentinel.
func IsSecretKey(plaintext []byte) bool {
	return bytes.HasPrefix(plaintext, []byte("AGE-SECRET-KEY-1"))
}

// DecryptWithPassphrase decrypts a file that was encrypted directly to a
// passphrase with age's scrypt recipient. Each call runs one scrypt
// derivation, so it is used only for vlt.age and for migrating legacy
// (format 1) vaults. It returns ErrWrongPassphrase if the passphrase does not
// match.
func DecryptWithPassphrase(passphrase string, ciphertext []byte) ([]byte, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("crypto.DecryptWithPassphrase: %w", errEmptyPassphrase)
	}
	if err := checkWorkFactor(ciphertext); err != nil {
		return nil, fmt.Errorf("crypto.DecryptWithPassphrase: %w", err)
	}
	sid, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptWithPassphrase: %w", err)
	}
	// age checks the cap too, but its error has no type; checkWorkFactor
	// says why first, and this stays as a second guard.
	sid.SetMaxWorkFactor(MaxWorkFactor)
	r, err := age.Decrypt(bytes.NewReader(ciphertext), sid)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, ErrWrongPassphrase
		}
		return nil, fmt.Errorf("crypto.DecryptWithPassphrase: %w", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("crypto.DecryptWithPassphrase: read: %w", err)
	}
	return out, nil
}

// checkWorkFactor refuses an age file whose scrypt stanza
// ("-> scrypt <salt> <logN>", in the header before the "---" line) asks for
// a work factor above MaxWorkFactor. Anything it can't read is left for age
// to judge.
func checkWorkFactor(ciphertext []byte) error {
	header, _, _ := bytes.Cut(ciphertext, []byte("\n---"))
	for line := range bytes.SplitSeq(header, []byte("\n")) {
		f := bytes.Fields(line)
		if len(f) != 4 || string(f[0]) != "->" || string(f[1]) != "scrypt" {
			continue
		}
		if logN, err := strconv.Atoi(string(f[3])); err == nil && logN > MaxWorkFactor {
			return fmt.Errorf("%w (2^%d; at most 2^%d)", ErrWorkFactorTooHigh, logN, MaxWorkFactor)
		}
	}
	return nil
}

// Wipe overwrites b with zeros. Use it on decrypted plaintext that is no
// longer needed.
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
