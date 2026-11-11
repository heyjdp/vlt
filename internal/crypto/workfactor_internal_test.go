package crypto

import (
	"errors"
	"strconv"
	"testing"
)

// scryptHeader returns the header of a passphrase file at work factor logN;
// checkWorkFactor reads nothing else.
func scryptHeader(logN int) []byte {
	return []byte("age-encryption.org/v1\n-> scrypt c2FsdHNhbHRzYWx0c2FsdA " + strconv.Itoa(logN) + "\nYm9keQ\n--- bWFj\n")
}

func TestCheckWorkFactor_TheCapIsInclusive(t *testing.T) {
	t.Parallel()
	for logN, refused := range map[int]bool{
		DefaultWorkFactor: false,
		MaxWorkFactor:     false,
		MaxWorkFactor + 1: true,
	} {
		err := checkWorkFactor(scryptHeader(logN))
		if got := errors.Is(err, ErrWorkFactorTooHigh); got != refused {
			t.Errorf("work factor %d: refused = %v (%v), want %v", logN, got, err, refused)
		}
	}
}

func TestCheckWorkFactor_AcceptsWhatWrapIdentityWrites(t *testing.T) {
	t.Parallel()
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	defer id.Zero()
	wrapped, err := WrapIdentity("a passphrase", id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkWorkFactor(wrapped); err != nil {
		t.Fatalf("checkWorkFactor refused a file vlt wrote: %v", err)
	}
}

func TestWrapIdentity_RefusesAWorkFactorItCouldNotOpen(t *testing.T) {
	t.Parallel()
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	defer id.Zero()
	for _, logN := range []int{MaxWorkFactor + 1, 31, 64} {
		if _, err := WrapIdentity("a passphrase", id, logN); !errors.Is(err, ErrWorkFactorTooHigh) {
			t.Errorf("WrapIdentity at %d: err = %v; want ErrWorkFactorTooHigh", logN, err)
		}
	}
}

// Odd spellings age itself would reject are left to age, or refused when
// they read as a number above the cap; checkWorkFactor never lets a high
// factor through.
func TestCheckWorkFactor_OddHeaders(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		header  string
		refused bool
	}{
		"a factor that isn't a number": {"age-encryption.org/v1\n-> scrypt c2FsdA x\n--- bWFj\n", false},
		"no footer":                    {"age-encryption.org/v1\n-> scrypt c2FsdA 22\n", true},
		"a plus sign":                  {"age-encryption.org/v1\n-> scrypt c2FsdA +21\n--- bWFj\n", true},
		"a leading zero":               {"age-encryption.org/v1\n-> scrypt c2FsdA 021\n--- bWFj\n", true},
		"no scrypt stanza":             {"age-encryption.org/v1\n-> X25519 abc\n--- bWFj\n", false},
	} {
		err := checkWorkFactor([]byte(tc.header))
		if got := errors.Is(err, ErrWorkFactorTooHigh); got != tc.refused {
			t.Errorf("%s: refused = %v (%v), want %v", name, got, err, tc.refused)
		}
	}
}
