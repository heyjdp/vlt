// Package crypto provides age-based encryption primitives for the vlt secret manager.
// It wraps filippo.io/age with memguard secure memory for passphrase-derived identities.
package crypto

import (
	"errors"
	"fmt"
	"sync"

	"filippo.io/age"
	"github.com/awnumar/memguard"
)

// Identity holds a derived age scrypt identity in a secure memory buffer.
// Call Zero() to wipe the identity from memory when no longer needed.
type Identity struct {
	mu       sync.Mutex
	buf      *memguard.LockedBuffer
	identity *age.ScryptIdentity
	zeroed   bool
}

// NewIdentity derives an age ScryptIdentity from a passphrase.
// The passphrase is stored in a memguard LockedBuffer (mlock'd with guard pages).
// Caller must call Zero() when finished.
func NewIdentity(passphrase string) (*Identity, error) {
	if passphrase == "" {
		return nil, errors.New("crypto.NewIdentity: passphrase cannot be empty")
	}

	buf := memguard.NewBufferFromBytes([]byte(passphrase))

	identity, err := age.NewScryptIdentity(string(buf.Bytes()))
	if err != nil {
		buf.Destroy()
		return nil, fmt.Errorf("crypto.NewIdentity: %w", err)
	}

	return &Identity{
		buf:      buf,
		identity: identity,
	}, nil
}

// ScryptIdentity returns the underlying age identity for use in decryption.
// Returns an error if the identity has been zeroed.
func (id *Identity) ScryptIdentity() (*age.ScryptIdentity, error) {
	id.mu.Lock()
	defer id.mu.Unlock()

	if id.zeroed {
		return nil, errors.New("crypto.Identity: identity has been zeroed")
	}
	return id.identity, nil
}

// Recipient returns an age ScryptRecipient for encrypting to this identity.
func (id *Identity) Recipient() (*age.ScryptRecipient, error) {
	id.mu.Lock()
	defer id.mu.Unlock()

	if id.zeroed {
		return nil, errors.New("crypto.Identity: identity has been zeroed")
	}

	r, err := age.NewScryptRecipient(string(id.buf.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("crypto.Identity.Recipient: %w", err)
	}
	return r, nil
}

// Zero destroys the secure buffer and marks the identity as unusable.
func (id *Identity) Zero() {
	id.mu.Lock()
	defer id.mu.Unlock()

	if !id.zeroed {
		id.buf.Destroy()
		id.identity = nil
		id.zeroed = true
	}
}

// IsZeroed reports whether the identity has been zeroed.
func (id *Identity) IsZeroed() bool {
	id.mu.Lock()
	defer id.mu.Unlock()
	return id.zeroed
}
