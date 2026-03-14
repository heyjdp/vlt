package store_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yourorg/vlt/internal/store"
)

func TestSecretType_Constants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  store.SecretType
		want string
	}{
		{"password", store.TypePassword, "password"},
		{"record", store.TypeRecord, "record"},
		{"file", store.TypeFile, "file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if string(tt.got) != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, tt.got)
			}
		})
	}
}

func TestSecret_MarshalJSON_Password(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	s := &store.Secret{
		Version:    1,
		Type:       store.TypePassword,
		Name:       "GitHub API Key",
		CreatedAt:  now,
		ModifiedAt: now,
		Tags:       []string{"github", "ci"},
		Notes:      "Expires 2027-03-14",
		Value:      "ghp_xxxxxxxxxxxx",
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got store.Secret
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Name != s.Name {
		t.Fatalf("name mismatch: got %q, want %q", got.Name, s.Name)
	}
	if got.Value != s.Value {
		t.Fatalf("value mismatch: got %q, want %q", got.Value, s.Value)
	}
	if got.Type != store.TypePassword {
		t.Fatalf("type mismatch: got %q, want %q", got.Type, store.TypePassword)
	}
}

func TestSecret_MarshalJSON_Record(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	s := &store.Secret{
		Version:    1,
		Type:       store.TypeRecord,
		Name:       "Work Jira",
		CreatedAt:  now,
		ModifiedAt: now,
		Username:   "user@example.com",
		Password:   "secret",
		URL:        "https://jira.example.com",
		TOTPSecret: "JBSWY3DPEHPK3PXP",
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got store.Secret
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Username != s.Username {
		t.Fatalf("username mismatch: got %q, want %q", got.Username, s.Username)
	}
	if got.URL != s.URL {
		t.Fatalf("url mismatch: got %q, want %q", got.URL, s.URL)
	}
}

func TestSecret_MarshalJSON_File(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	s := &store.Secret{
		Version:          1,
		Type:             store.TypeFile,
		Name:             "Server Key",
		CreatedAt:        now,
		ModifiedAt:       now,
		OriginalFilename: "id_ed25519",
		MIMEType:         "application/octet-stream",
		Encoding:         "base64",
		Value:            "LS0tLS1CRUdJTi...",
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got store.Secret
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.OriginalFilename != s.OriginalFilename {
		t.Fatalf("original_filename mismatch: got %q, want %q", got.OriginalFilename, s.OriginalFilename)
	}
	if got.MIMEType != s.MIMEType {
		t.Fatalf("mime_type mismatch: got %q, want %q", got.MIMEType, s.MIMEType)
	}
}

func TestSlugify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "GitHub API Key", "github-api-key"},
		{"special chars", "my@secret!val", "my-secret-val"},
		{"spaces", "  lots   of   spaces  ", "lots-of-spaces"},
		{"dashes", "already-slugged", "already-slugged"},
		{"numbers", "token-42", "token-42"},
		{"trailing special", "test!!!", "test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := store.Slugify(tt.input)
			if got != tt.want {
				t.Fatalf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewSecretName_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid name", "GitHub API Key", false},
		{"empty string", "", true},
		{"only whitespace", "   ", true},
		{"max length", strings.Repeat("a", 255), false},
		{"over max length", strings.Repeat("a", 256), true},
		{"trimmed whitespace", "  hello  ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := store.NewSecretName(tt.input)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSecretName_Slug(t *testing.T) {
	t.Parallel()

	n, err := store.NewSecretName("GitHub API Key")
	if err != nil {
		t.Fatalf("NewSecretName: %v", err)
	}

	if got := n.Slug(); got != "github-api-key" {
		t.Fatalf("Slug() = %q, want %q", got, "github-api-key")
	}
}

func TestCategoryDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		secretType store.SecretType
		want       string
	}{
		{store.TypePassword, "passwords"},
		{store.TypeRecord, "records"},
		{store.TypeFile, "files"},
		{store.SecretType("unknown"), "other"},
	}

	for _, tt := range tests {
		t.Run(string(tt.secretType), func(t *testing.T) {
			t.Parallel()
			got := store.CategoryDir(tt.secretType)
			if got != tt.want {
				t.Fatalf("CategoryDir(%q) = %q, want %q", tt.secretType, got, tt.want)
			}
		})
	}
}
