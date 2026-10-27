package store_test

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yourorg/vlt/internal/store"
	"github.com/yourorg/vlt/internal/testutil"
)

// TestStore_Create_ConcurrentSameNameExactlyOneWins checks the no-overwrite
// guarantee under a race: names sharing a slug, created at once, yield one
// success and ErrSecretExists for the rest.
func TestStore_Create_ConcurrentSameNameExactlyOneWins(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	const n = 16
	var ok, exists atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			name := []string{"GitHub Key", "github-key"}[i%2]
			err := s.Create(context.Background(), testutil.PasswordSecret(name, "v", "work"))
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, store.ErrSecretExists):
				exists.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if ok.Load() != 1 || exists.Load() != n-1 {
		t.Fatalf("expected 1 success and %d ErrSecretExists, got ok=%d exists=%d", n-1, ok.Load(), exists.Load())
	}
}

// TestStore_ImportTwice_CreateRefusesThenUpdateReplaces mirrors the import
// flow in ui/fileops.go: the second import of a file is refused by Create, and
// only an explicit Update (after the user confirms) replaces it.
func TestStore_ImportTwice_CreateRefusesThenUpdateReplaces(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()
	first := testutil.FileSecret("id_rsa", "id_rsa", base64.StdEncoding.EncodeToString([]byte("v1")), "keys")
	if err := s.Create(ctx, first); err != nil {
		t.Fatalf("first import: %v", err)
	}

	second := testutil.FileSecret("id_rsa", "id_rsa", base64.StdEncoding.EncodeToString([]byte("v2")), "keys")
	if err := s.Create(ctx, second); !errors.Is(err, store.ErrSecretExists) {
		t.Fatalf("second import: expected ErrSecretExists, got %v", err)
	}
	got, err := s.Get(ctx, "id_rsa", "keys")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != first.Value {
		t.Fatal("refused import still changed the stored file")
	}

	if err := s.Update(ctx, second); err != nil {
		t.Fatalf("confirmed replace: %v", err)
	}
	got, err = s.Get(ctx, "id_rsa", "keys")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != second.Value {
		t.Fatal("confirmed replace did not store the new contents")
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("replace changed CreatedAt: got %v, want %v", got.CreatedAt, first.CreatedAt)
	}
}
