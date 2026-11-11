package crypto

import (
	"errors"
	"testing"
	"time"

	"filippo.io/age"
)

// slowRecipient stands in for the scrypt recipient: Wrap signals that the
// key derivation has started, then waits until the test lets it finish.
type slowRecipient struct {
	age.Recipient
	started chan struct{}
	finish  chan struct{}
}

func (r slowRecipient) Wrap(fileKey []byte) ([]*age.Stanza, error) {
	close(r.started)
	<-r.finish
	return r.Recipient.Wrap(fileKey)
}

func TestWrapTo_ZeroDuringTheKeyDerivationIsPromptAndTheWrapFails(t *testing.T) {
	t.Parallel()
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	defer id.Zero()
	x, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	r := slowRecipient{Recipient: x.Recipient(), started: make(chan struct{}), finish: make(chan struct{})}

	type result struct {
		out []byte
		err error
	}
	wrapped := make(chan result, 1)
	go func() {
		out, err := wrapTo(r, id)
		wrapped <- result{out, err}
	}()
	<-r.started

	zeroed := make(chan struct{})
	go func() {
		id.Zero()
		close(zeroed)
	}()
	select {
	case <-zeroed:
	case <-time.After(5 * time.Second):
		close(r.finish) // let the wrap end, so the test does not hang
		t.Fatal("Zero waited for the key derivation to finish")
	}
	close(r.finish)

	got := <-wrapped
	if !errors.Is(got.err, ErrIdentityZeroed) || got.out != nil {
		t.Fatalf("wrap after Zero = %d bytes, %v; want ErrIdentityZeroed and nothing", len(got.out), got.err)
	}
}
