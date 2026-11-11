package store

import (
	"context"
	"errors"
	"io/fs"
	"testing"
)

// groupReadFails makes listing one group folder fail with err.
type groupReadFails struct {
	vaultFS
	group string
	err   error
}

func (f groupReadFails) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.group {
		return nil, f.err
	}
	return f.vaultFS.ReadDir(name)
}

// A connection lost while listing a group folder fails the whole listing,
// as it does while reading a secret, rather than leave that group's secrets
// silently missing.
func TestContents_ALostConnectionWhileListingAGroupFails(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	if err := s.Create(context.Background(), &Secret{Type: TypePassword, Name: "key", Group: "work", Value: "pw"}); err != nil {
		t.Fatal(err)
	}
	s.fs = groupReadFails{vaultFS: s.fs, group: "work", err: ErrConnectionLost}

	if _, err := s.Contents(context.Background()); !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("Contents = %v; want the lost connection", err)
	}
}
