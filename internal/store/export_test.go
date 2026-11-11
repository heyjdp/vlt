package store

import (
	"fmt"
	"log/slog"

	"github.com/heyjdp/vlt/internal/crypto"
)

// New creates a Store over the local directory dir that encrypts and
// decrypts with the given vault key, for tests that already hold the key.
// It does not read or write vlt.age; production code uses Create or Open.
func New(id *crypto.Identity, dir string, log *slog.Logger) *Store {
	return newStore(id, localFS{root: dir}, Location{Path: dir}, log, options{})
}

// Init creates the vault directory with secure permissions, for a Store
// made by New.
func (s *Store) Init() error {
	if err := s.fs.MkdirAll("."); err != nil {
		return fmt.Errorf("store.Init: %w", err)
	}
	return nil
}
