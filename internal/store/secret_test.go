package store_test

import (
	"encoding/json"
	"reflect"
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

// TestSecret_JSONRoundTrip checks every secret type survives the JSON
// envelope intact, field for field.
func TestSecret_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		secret store.Secret
	}{
		{"password", store.Secret{
			Version: 1, Type: store.TypePassword, Name: "GitHub API Key", Group: "work", CreatedAt: ts, ModifiedAt: ts,
			Tags: []string{"github", "ci"}, Notes: "Expires 2027-03-14", Value: "ghp_xxxxxxxxxxxx",
		}},
		{"record", store.Secret{
			Version: 1, Type: store.TypeRecord, Name: "Work Jira", Group: "work", CreatedAt: ts, ModifiedAt: ts,
			Username: "user@example.com", Password: "secret", URL: "https://jira.example.com", TOTPSecret: "JBSWY3DPEHPK3PXP",
		}},
		{"file", store.Secret{
			Version: 1, Type: store.TypeFile, Name: "Server Key", Group: "keys", CreatedAt: ts, ModifiedAt: ts,
			OriginalFilename: "id_ed25519", MIMEType: "application/octet-stream", Encoding: "base64", Value: "LS0tLS1CRUdJTi...",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tt.secret)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got store.Secret
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tt.secret) {
				t.Fatalf("round trip changed the secret:\n got  %+v\n want %+v", got, tt.secret)
			}
		})
	}
}

// TestSecret_JSONKeysAreStable pins the on-disk key names: vaults written by
// earlier versions must keep decoding, so renaming a JSON key is a format
// change, not a refactor.
func TestSecret_JSONKeysAreStable(t *testing.T) {
	t.Parallel()

	const stored = `{"version":1,"type":"record","name":"Work Jira","group":"work",
		"created_at":"2026-03-14T09:00:00Z","modified_at":"2026-03-14T09:00:00Z",
		"tags":["jira"],"notes":"n","value":"v","username":"u","password":"p",
		"url":"https://jira.example.com","totp_secret":"T","original_filename":"f",
		"mime_type":"m","encoding":"base64"}`
	var got store.Secret
	if err := json.Unmarshal([]byte(stored), &got); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)
	want := store.Secret{
		Version: 1, Type: store.TypeRecord, Name: "Work Jira", Group: "work", CreatedAt: ts, ModifiedAt: ts,
		Tags: []string{"jira"}, Notes: "n", Value: "v", Username: "u", Password: "p",
		URL: "https://jira.example.com", TOTPSecret: "T", OriginalFilename: "f", MIMEType: "m", Encoding: "base64",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoding the stored format changed:\n got  %+v\n want %+v", got, want)
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
		{"longest filename-safe slug", strings.Repeat("a", 251), false},
		{"slug too long for a filename", strings.Repeat("a", 252), true},
		{"long name with short slug", strings.Repeat("!", 250) + "abc", false},
		{"over max bytes", strings.Repeat("a!", 128), true},
		{"trimmed whitespace", "  hello  ", false},
		{"punctuation only", "!!!", true},
		{"non-ASCII only", "日本", true},
		{"non-ASCII with a letter", "日本 key", false},
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

func TestNormalizeGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "work", "work"},
		{"with spaces", "My Group", "my-group"},
		{"empty", "", "default"},
		{"whitespace only", "   ", "default"},
		{"special chars", "Work@Home!", "work-home"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := store.NormalizeGroup(tt.input)
			if got != tt.want {
				t.Fatalf("NormalizeGroup(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGroupLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "work", "work"},
		{"kept as typed", "Personal Finance", "Personal Finance"},
		{"trimmed", "  My Group\t", "My Group"},
		{"empty", "", "default"},
		{"whitespace only", "   ", "default"},
		{"no letter or digit", "!!!", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := store.GroupLabel(tt.input); got != tt.want {
				t.Fatalf("GroupLabel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
