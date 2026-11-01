package totp

import (
	"encoding/base32"
	"errors"
	"strings"
	"testing"
	"time"
)

// b32 encodes an RFC 6238 test seed the way a site would show it.
func b32(seed string) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(seed))
}

// The RFC 6238 appendix B test vectors (8 digits, 30 s).
func TestCode_RFC6238Vectors(t *testing.T) {
	t.Parallel()
	seeds := map[string]string{
		"SHA1":   "12345678901234567890",
		"SHA256": "12345678901234567890123456789012",
		"SHA512": "1234567890123456789012345678901234567890123456789012345678901234",
	}
	vectors := []struct {
		unix int64
		want map[string]string
	}{
		{59, map[string]string{"SHA1": "94287082", "SHA256": "46119246", "SHA512": "90693936"}},
		{1111111109, map[string]string{"SHA1": "07081804", "SHA256": "68084774", "SHA512": "25091201"}},
		{1111111111, map[string]string{"SHA1": "14050471", "SHA256": "67062674", "SHA512": "99943326"}},
		{1234567890, map[string]string{"SHA1": "89005924", "SHA256": "91819424", "SHA512": "93441116"}},
		{2000000000, map[string]string{"SHA1": "69279037", "SHA256": "90698825", "SHA512": "38618901"}},
		{20000000000, map[string]string{"SHA1": "65353130", "SHA256": "77737706", "SHA512": "47863826"}},
	}
	for alg, seed := range seeds {
		k, err := Parse("otpauth://totp/Example:me?digits=8&algorithm=" + alg + "&secret=" + b32(seed))
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		for _, v := range vectors {
			if got := k.Code(time.Unix(v.unix, 0).UTC()); got != v.want[alg] {
				t.Errorf("%s at %d = %s, want %s", alg, v.unix, got, v.want[alg])
			}
		}
	}
}

func TestParse_PlainSecretsAsSitesShowThem(t *testing.T) {
	t.Parallel()
	seed := b32("12345678901234567890") // GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ
	at := time.Unix(59, 0)
	for _, s := range []string{
		seed,
		strings.ToLower(seed),
		"  gezd gnbv gy3t qojq gezd gnbv gy3t qojq  ",
		"GEZD-GNBV-GY3T-QOJQ-GEZD-GNBV-GY3T-QOJQ",
		seed + "====",
		"GEZD\u00a0GNBV\u00a0GY3T\u00a0QOJQ\u00a0GEZD\u00a0GNBV\u00a0GY3T\u00a0QOJQ", // non-breaking spaces
		"otpauth://totp/x?secret=" + seed,
		"  otpauth://totp/x?secret=" + seed + "  \n",
		"otpauth://totp/x?SECRET=" + seed + "&Digits=6", // parameter names in any case
		"otpauth://totp/50%off:me?secret=" + seed,       // a raw % in the label
		"otpauth://totp/a#b?secret=" + seed + "#frag",   // # in the label, and a fragment
	} {
		k, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		// The 6-digit code is the last six digits of the 8-digit vector.
		if got := k.Code(at); got != "287082" {
			t.Errorf("Parse(%q).Code = %s, want 287082", s, got)
		}
		if k.Period() != 30*time.Second {
			t.Errorf("period = %v", k.Period())
		}
	}
}

func TestParse_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		wantErr error
		wantMsg string
	}{
		{"", ErrEmpty, ""},
		{"   ", ErrEmpty, ""},
		{"====", ErrEmpty, ""},
		{"not base32!", ErrNotBase32, ""},
		{"ABC1", ErrNotBase32, ""}, // 1 is not a base32 digit
		{"A", ErrNotBase32, ""},    // too short to hold a byte
		{"otpauth://totp/x", ErrEmpty, ""},
		{"otpauth://totp/x?secret=0OO", ErrNotBase32, ""},
		{"otpauth://hotp/x?secret=GEZDGNBV&counter=1", nil, "HOTP"},
		{"otpauth://steam/x?secret=GEZDGNBV", nil, "not totp"},
		{"otpauth://totp/x?secret=GEZDGNBV&digits=5", nil, "6 to 8"},
		{"otpauth://totp/x?secret=GEZDGNBV&digits=9", nil, "6 to 8"},
		{"otpauth://totp/x?secret=GEZDGNBV&digits=six", nil, "6 to 8"},
		{"otpauth://totp/x?secret=GEZDGNBV&period=0", nil, "1 to 300"},
		{"otpauth://totp/x?secret=GEZDGNBV&period=301", nil, "1 to 300"},
		{"otpauth://totp/x?secret=GEZDGNBV&algorithm=MD5", nil, "SHA1, SHA256 and SHA512"},
		{"otpauth://totp/x?secret=%zz", nil, "parameters cannot be read"},
		{"otpauth:totp/x?secret=GEZDGNBV", nil, "cannot be read"},
		{"otpauth://totp/Steam:me?secret=GEZDGNBV&issuer=Steam&encoder=steam", nil, `"steam" codes`},
	}
	for _, tt := range tests {
		_, err := Parse(tt.in)
		if err == nil {
			t.Errorf("Parse(%q): no error", tt.in)
			continue
		}
		if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
			t.Errorf("Parse(%q) = %v, want %v", tt.in, err, tt.wantErr)
		}
		if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
			t.Errorf("Parse(%q) = %v, want it to mention %q", tt.in, err, tt.wantMsg)
		}
	}
}

func TestParse_LinkOptions(t *testing.T) {
	t.Parallel()
	k, err := Parse("OTPAUTH://TOTP/GitHub:me?secret=" + b32("12345678901234567890") + "&issuer=GitHub&digits=7&period=60&algorithm=sha1")
	if err != nil {
		t.Fatal(err)
	}
	if k.digits != 7 || k.Period() != time.Minute {
		t.Fatalf("key = %d digits, %v", k.digits, k.Period())
	}
	if got := k.Code(time.Unix(59, 0)); len(got) != 7 {
		t.Fatalf("code = %q", got)
	}
	// A 60 s period: 0–59 s share a code, 60 s starts the next.
	if k.Code(time.Unix(0, 0)) != k.Code(time.Unix(59, 0)) || k.Code(time.Unix(59, 0)) == k.Code(time.Unix(60, 0)) {
		t.Fatal("the code should change exactly at the period boundary")
	}
}

func TestCode_PadsWithZeros(t *testing.T) {
	t.Parallel()
	// RFC 6238 at 1111111109 gives 07081804: the leading zero is kept.
	k, err := Parse("otpauth://totp/x?digits=8&secret=" + b32("12345678901234567890"))
	if err != nil {
		t.Fatal(err)
	}
	if got := k.Code(time.Unix(1111111109, 0)); got != "07081804" {
		t.Fatalf("code = %s", got)
	}
}

func TestRemaining(t *testing.T) {
	t.Parallel()
	k, err := Parse(b32("12345678901234567890"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		at   time.Time
		want time.Duration
	}{
		{time.Unix(0, 0), 30 * time.Second},
		{time.Unix(29, 0), time.Second},
		{time.Unix(29, 500_000_000), 500 * time.Millisecond},
		{time.Unix(30, 0), 30 * time.Second},
		{time.Unix(1111111109, 0), 1 * time.Second},
	} {
		if got := k.Remaining(tt.at); got != tt.want {
			t.Errorf("Remaining(%v) = %v, want %v", tt.at.Unix(), got, tt.want)
		}
	}
}

func TestParse_AcceptsTheEndsOfEachRange(t *testing.T) {
	t.Parallel()
	seed := b32("12345678901234567890")
	for _, opt := range []string{"digits=6", "digits=8", "period=1", "period=300"} {
		if _, err := Parse("otpauth://totp/x?secret=" + seed + "&" + opt); err != nil {
			t.Errorf("%s: %v", opt, err)
		}
	}
}

func TestStep_RoundsDownBefore1970(t *testing.T) {
	t.Parallel()
	k, err := Parse(b32("12345678901234567890"))
	if err != nil {
		t.Fatal(err)
	}
	if got := k.Remaining(time.Unix(-1, 0)); got != time.Second {
		t.Fatalf("Remaining(-1 s) = %v, want 1s", got)
	}
	if k.Code(time.Unix(-1, 0)) != k.Code(time.Unix(-30, 0)) || k.Code(time.Unix(-1, 0)) == k.Code(time.Unix(0, 0)) {
		t.Fatal("-30 s to -1 s should be one period, before the one starting at 0")
	}
	// Far in the future, past where nanosecond counts overflow.
	if got := k.Remaining(time.Date(2300, 1, 1, 0, 0, 10, 0, time.UTC)); got != 20*time.Second {
		t.Fatalf("Remaining(2300) = %v, want 20s", got)
	}
}

func TestWipe_ClearsTheSecret(t *testing.T) {
	t.Parallel()
	k, err := Parse(b32("12345678901234567890"))
	if err != nil {
		t.Fatal(err)
	}
	before := k.Code(time.Unix(59, 0))
	k.Wipe()
	for _, b := range k.secret {
		if b != 0 {
			t.Fatal("the secret should be zeroed")
		}
	}
	if k.Code(time.Unix(59, 0)) == before {
		t.Fatal("a wiped key should no longer make the same codes")
	}
}
