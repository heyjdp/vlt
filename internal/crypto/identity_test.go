package crypto_test

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/heyjdp/vlt/internal/crypto"
)

func newID(t *testing.T) *crypto.Identity {
	t.Helper()
	id, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	t.Cleanup(id.Zero)
	return id
}

func TestGenerateIdentity_ReturnsUsableKey(t *testing.T) {
	t.Parallel()

	id := newID(t)
	r, err := id.Recipient()
	if err != nil {
		t.Fatalf("Recipient: %v", err)
	}
	if !strings.HasPrefix(r.String(), "age1") {
		t.Fatalf("unexpected recipient %q", r.String())
	}
}

func TestGenerateIdentity_KeysAreUnique(t *testing.T) {
	t.Parallel()

	a, err := newID(t).Recipient()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newID(t).Recipient()
	if err != nil {
		t.Fatal(err)
	}
	if a.String() == b.String() {
		t.Fatal("two generated keys share a recipient")
	}
}

func TestIdentity_Zero_IsIdempotent(t *testing.T) {
	t.Parallel()

	id := newID(t)
	id.Zero()
	id.Zero()
	if _, err := id.Recipient(); !errors.Is(err, crypto.ErrIdentityZeroed) {
		t.Fatalf("expected ErrIdentityZeroed after Zero, got %v", err)
	}
}

func TestIdentity_Recipient_FailsAfterZero(t *testing.T) {
	t.Parallel()

	id := newID(t)
	id.Zero()
	if _, err := id.Recipient(); !errors.Is(err, crypto.ErrIdentityZeroed) {
		t.Fatalf("expected ErrIdentityZeroed, got %v", err)
	}
}

func TestWrapUnwrap_RoundTrip(t *testing.T) {
	t.Parallel()

	id := newID(t)
	wrapped, err := crypto.WrapIdentity("correct-horse", id, 10)
	if err != nil {
		t.Fatalf("WrapIdentity: %v", err)
	}

	got, err := crypto.UnwrapIdentity("correct-horse", wrapped)
	if err != nil {
		t.Fatalf("UnwrapIdentity: %v", err)
	}
	defer got.Zero()

	want, _ := id.Recipient()
	have, _ := got.Recipient()
	if want.String() != have.String() {
		t.Fatal("unwrapped key differs from the original")
	}
}

func TestUnwrapIdentity_FailsWithWrongPassphrase(t *testing.T) {
	t.Parallel()

	wrapped, err := crypto.WrapIdentity("correct-horse", newID(t), 10)
	if err != nil {
		t.Fatalf("WrapIdentity: %v", err)
	}
	if _, err := crypto.UnwrapIdentity("wrong-horse", wrapped); !errors.Is(err, crypto.ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase, got %v", err)
	}
}

func TestUnwrapIdentity_RejectsNonKeyPayload(t *testing.T) {
	t.Parallel()

	// A legacy format-1 sentinel decrypts fine but is not a key.
	r, err := age.NewScryptRecipient("pw")
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

	_, err = crypto.UnwrapIdentity("pw", buf.Bytes())
	if err == nil || errors.Is(err, crypto.ErrWrongPassphrase) {
		t.Fatalf("expected a not-a-key error, got %v", err)
	}
}

func TestWrapIdentity_RejectsEmptyPassphrase(t *testing.T) {
	t.Parallel()

	if _, err := crypto.WrapIdentity("", newID(t), 10); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}

func TestWrapIdentity_FailsAfterZero(t *testing.T) {
	t.Parallel()

	id := newID(t)
	id.Zero()
	if _, err := crypto.WrapIdentity("pw", id, 10); !errors.Is(err, crypto.ErrIdentityZeroed) {
		t.Fatalf("expected ErrIdentityZeroed, got %v", err)
	}
}

func TestParseIdentity_RejectsGarbage(t *testing.T) {
	t.Parallel()

	if _, err := crypto.ParseIdentity([]byte("AGE-SECRET-KEY-1NOTAKEY")); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestIsSecretKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"secret key", "AGE-SECRET-KEY-1QQQ", true},
		{"legacy sentinel", "unlocked", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := crypto.IsSecretKey([]byte(tt.input)); got != tt.want {
				t.Fatalf("IsSecretKey(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// withWorkFactor rewrites the work factor in an scrypt file's header, as
// whoever can write vlt.age could. The header's MAC no longer matches, but
// the work factor must be refused before any key derivation runs.
func withWorkFactor(t *testing.T, file []byte, logN int) []byte {
	t.Helper()
	lines := strings.Split(string(file), "\n")
	for i, line := range lines {
		if f := strings.Fields(line); len(f) == 4 && f[0] == "->" && f[1] == "scrypt" {
			f[3] = strconv.Itoa(logN)
			lines[i] = strings.Join(f, " ")
			return []byte(strings.Join(lines, "\n"))
		}
	}
	t.Fatal("no scrypt stanza in the file")
	return nil
}

func TestDecryptWithPassphrase_RefusesAWorkFactorAboveTheCap(t *testing.T) {
	t.Parallel()

	r, err := age.NewScryptRecipient("right-pass")
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(10)
	good, err := encryptToRecipient(r, []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, logN := range []int{crypto.MaxWorkFactor + 1, 22, 30} {
		start := time.Now()
		_, err := crypto.DecryptWithPassphrase("right-pass", withWorkFactor(t, good, logN))
		if !errors.Is(err, crypto.ErrWorkFactorTooHigh) {
			t.Errorf("work factor %d: err = %v; want ErrWorkFactorTooHigh", logN, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("work factor %d: refusing took %v; no key derivation should run", logN, d)
		}
	}
}

func TestMaxWorkFactor_IsNotBelowTheDefault(t *testing.T) {
	t.Parallel()
	if crypto.MaxWorkFactor < crypto.DefaultWorkFactor {
		t.Fatalf("MaxWorkFactor %d is below DefaultWorkFactor %d: vlt couldn't open its own vaults",
			crypto.MaxWorkFactor, crypto.DefaultWorkFactor)
	}
}
