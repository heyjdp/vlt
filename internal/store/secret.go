// Package store provides secret storage operations on an age-encrypted vault directory.
package store

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// SecretType identifies the category of a secret.
type SecretType string

const (
	// TypePassword is for passwords, API keys, tokens, and other single-value text secrets.
	TypePassword SecretType = "password"
	// TypeRecord is for structured login records with URL, username, password, and optional TOTP.
	TypeRecord SecretType = "record"
	// TypeFile is for arbitrary binary files (SSH keys, TLS certs, etc).
	TypeFile SecretType = "file"
)

// Secret is the JSON envelope stored inside each .age file in the vault.
// All secret types share this structure, differentiated by the Type field.
// Secrets are stored at vault/<group>/<slug>.age.
type Secret struct {
	Version    int        `json:"version"`
	Type       SecretType `json:"type"`
	Name       string     `json:"name"`
	Group      string     `json:"group"`
	CreatedAt  time.Time  `json:"created_at"`
	ModifiedAt time.Time  `json:"modified_at"`
	Tags       []string   `json:"tags,omitempty"`
	Notes      string     `json:"notes,omitempty"`

	// Password type fields
	Value string `json:"value,omitempty"`

	// Record type fields
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	URL        string `json:"url,omitempty"`
	TOTPSecret string `json:"totp_secret,omitempty"`

	// File type fields
	OriginalFilename string `json:"original_filename,omitempty"`
	MIMEType         string `json:"mime_type,omitempty"`
	Encoding         string `json:"encoding,omitempty"`
}

// SecretName is a validated, non-empty secret name.
type SecretName struct {
	value string
}

// NewSecretName validates and returns a SecretName.
func NewSecretName(s string) (SecretName, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return SecretName{}, errors.New("secret name cannot be empty")
	}
	if len(s) > 255 {
		return SecretName{}, errors.New("secret name exceeds 255 characters")
	}
	return SecretName{value: s}, nil
}

// String returns the secret name as a string.
func (n SecretName) String() string { return n.value }

// Slug returns the slugified form of the secret name, suitable for filenames.
func (n SecretName) Slug() string { return Slugify(n.value) }

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts a string to a URL/filename-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// DefaultGroup is used when no group is specified.
const DefaultGroup = "default"

// ValidSecretTypes returns all valid secret types.
func ValidSecretTypes() []SecretType {
	return []SecretType{TypePassword, TypeRecord, TypeFile}
}

// NormalizeGroup returns the slugified group name, falling back to DefaultGroup.
func NormalizeGroup(group string) string {
	g := Slugify(group)
	if g == "" {
		return DefaultGroup
	}
	return g
}
