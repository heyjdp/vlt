// Package totp generates time-based one-time passwords (RFC 6238), the
// six-digit codes of authenticator apps, from a record's TOTP secret.
package totp

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238's default; HMAC-SHA-1 is not broken
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Defaults from RFC 6238 and the otpauth:// key URI format.
const (
	defaultDigits = 6
	defaultPeriod = 30 * time.Second
)

// Errors returned by Parse. They and Parse's other errors are written to
// be shown to the user as they are, so they carry no "totp.Parse:" prefix.
var (
	// ErrEmpty means there is no secret to generate codes from.
	ErrEmpty = errors.New("the TOTP secret is empty")
	// ErrNotBase32 means the secret is not base32, the form sites show
	// next to their QR code.
	ErrNotBase32 = errors.New("the TOTP secret is not valid base32 (letters A–Z and digits 2–7)")
)

// Key generates codes for one account. Make one with Parse; the zero Key
// is not usable.
type Key struct {
	secret []byte
	digits int
	period time.Duration
	hash   func() hash.Hash
}

// Parse reads a TOTP secret as stored in a record: either the base32 secret
// a site shows (spaces, hyphens, lower case and missing padding allowed), or
// an otpauth://totp/… link as encoded in the QR code, whose digits (6–8),
// period (1–300 seconds) and algorithm (SHA1, SHA256, SHA512) are honoured.
// HOTP links, other link types and Steam Guard links (encoder=steam) are
// refused with a reason.
func Parse(s string) (Key, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Key{}, ErrEmpty
	}
	if len(s) >= len("otpauth:") && strings.EqualFold(s[:len("otpauth:")], "otpauth:") {
		return parseURI(s)
	}
	secret, err := decodeSecret(s)
	if err != nil {
		return Key{}, err
	}
	return Key{secret: secret, digits: defaultDigits, period: defaultPeriod, hash: sha1.New}, nil
}

// decodeSecret decodes a base32 secret, forgiving the ways people copy it:
// white space (including non-breaking spaces) and hyphens are ignored, and
// case and padding don't matter.
func decodeSecret(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' {
			return -1
		}
		return unicode.ToUpper(r)
	}, s)
	s = strings.TrimRight(s, "=")
	if s == "" {
		return nil, ErrEmpty
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil || len(secret) == 0 {
		return nil, ErrNotBase32
	}
	return secret, nil
}

// parseURI reads an otpauth://totp/ link (the Key Uri Format used by
// Google Authenticator and most sites). The label (the account name) is
// not used, so it is not parsed: a raw "%" or "#" in it does no harm.
func parseURI(s string) (Key, error) {
	rest := s[len("otpauth:"):]
	if !strings.HasPrefix(rest, "//") {
		return Key{}, errors.New("the otpauth:// link cannot be read")
	}
	typ, query, _ := strings.Cut(rest[2:], "?")
	typ, _, _ = strings.Cut(typ, "/")
	switch strings.ToLower(typ) {
	case "totp":
	case "hotp":
		return Key{}, errors.New("counter-based (HOTP) codes are not supported; only time-based (TOTP) ones")
	default:
		return Key{}, fmt.Errorf("the otpauth:// link is for %q, not totp", typ)
	}
	query, _, _ = strings.Cut(query, "#")
	values, err := url.ParseQuery(query)
	if err != nil {
		return Key{}, errors.New("the otpauth:// link's parameters cannot be read")
	}
	// Parameter names are matched in any case (Secret=, SECRET=).
	params := make(map[string]string, len(values))
	for name, v := range values {
		params[strings.ToLower(name)] = v[0]
	}
	return keyFromParams(params)
}

// keyFromParams builds a key from an otpauth link's parameters, refusing
// those it can't honour.
func keyFromParams(params map[string]string) (Key, error) {
	if params["encoder"] != "" {
		// Steam Guard links say encoder=steam: five-character codes from
		// their own alphabet, which this package does not make.
		return Key{}, fmt.Errorf("the otpauth:// link asks for %q codes (encoder); only standard digit codes are supported", params["encoder"])
	}
	secret, err := decodeSecret(params["secret"])
	if err != nil {
		return Key{}, err
	}
	k := Key{secret: secret, digits: defaultDigits, period: defaultPeriod, hash: sha1.New}
	if d := params["digits"]; d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 6 || n > 8 {
			return Key{}, fmt.Errorf("the otpauth:// link asks for %q digits; 6 to 8 are supported", d)
		}
		k.digits = n
	}
	if p := params["period"]; p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 300 {
			return Key{}, fmt.Errorf("the otpauth:// link has a period of %q seconds; 1 to 300 are supported", p)
		}
		k.period = time.Duration(n) * time.Second
	}
	switch alg := strings.ToUpper(params["algorithm"]); alg {
	case "", "SHA1":
	case "SHA256":
		k.hash = sha256.New
	case "SHA512":
		k.hash = sha512.New
	default:
		return Key{}, fmt.Errorf("the otpauth:// link uses the %q algorithm; SHA1, SHA256 and SHA512 are supported", alg)
	}
	return k, nil
}

// step returns the number of the period t falls in (counting from the Unix
// epoch, rounding down) and how far into that period t is.
func (k Key) step(t time.Time) (n int64, into time.Duration) {
	p := int64(k.period / time.Second)
	n, r := t.Unix()/p, t.Unix()%p
	if r < 0 { // before 1970: round down, not towards zero
		n, r = n-1, r+p
	}
	return n, time.Duration(r)*time.Second + time.Duration(t.Nanosecond())
}

// Code returns the code valid at t, zero-padded to the key's digits.
func (k Key) Code(t time.Time) string {
	n, _ := k.step(t)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(n)) //nolint:gosec // RFC 6238's counter is the two's-complement value
	mac := hmac.New(k.hash, k.secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	// Dynamic truncation (RFC 4226 section 5.3).
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for range k.digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", k.digits, value%mod)
}

// Remaining returns how long the code valid at t stays valid.
func (k Key) Remaining(t time.Time) time.Duration {
	_, into := k.step(t)
	return k.period - into
}

// Wipe overwrites the key's secret, so it no longer generates codes; vlt
// calls it when the code stops being shown. Copies made inside HMAC are
// out of reach.
func (k Key) Wipe() { clear(k.secret) }

// Period returns how long each code is valid.
func (k Key) Period() time.Duration { return k.period }
