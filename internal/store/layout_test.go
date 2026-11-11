package store_test

import (
	"context"
	"testing"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

func TestSecret_ID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b store.Secret
		same bool
	}{
		{"same name and group", store.Secret{Name: "GitHub", Group: "Work"}, store.Secret{Name: "GitHub", Group: "Work"}, true},
		{"names differing in case and punctuation", store.Secret{Name: "GitHub Key", Group: "work"}, store.Secret{Name: "github-key", Group: "work"}, true},
		{"group labels spelled differently", store.Secret{Name: "bank", Group: "Personal Finance"}, store.Secret{Name: "bank", Group: "personal-finance"}, true},
		{"untrimmed name", store.Secret{Name: "  key ", Group: "work"}, store.Secret{Name: "key", Group: "work"}, true},
		{"no group is the default group", store.Secret{Name: "key"}, store.Secret{Name: "key", Group: "Default"}, true},
		{"different groups", store.Secret{Name: "key", Group: "work"}, store.Secret{Name: "key", Group: "home"}, false},
		{"different names", store.Secret{Name: "key-1", Group: "work"}, store.Secret{Name: "key-2", Group: "work"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.a.ID() == tt.b.ID(); got != tt.same {
				t.Fatalf("%q in %q and %q in %q: same ID = %v, want %v", tt.a.Name, tt.a.Group, tt.b.Name, tt.b.Group, got, tt.same)
			}
			if got := tt.a.ID().Group() == store.GroupIDOf(tt.a.Group); !got {
				t.Fatalf("ID().Group() of %q differs from GroupIDOf(%q)", tt.a.Name, tt.a.Group)
			}
		})
	}
}

// Secrets with the same ID share one file: Create refuses the second, and
// those with different IDs are stored side by side.
func TestSecret_ID_SameIDIsTheSameFile(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()
	first := testutil.PasswordSecret("GitHub Key", "pw", "Work")
	if err := s.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	for _, sec := range []*store.Secret{
		testutil.PasswordSecret("github-key", "pw", "work"),
		testutil.PasswordSecret("github key", "pw", "home"),
	} {
		err := s.Create(ctx, sec)
		if sameID := sec.ID() == first.ID(); sameID != (err != nil) {
			t.Fatalf("%q in %q: same ID = %v, Create = %v", sec.Name, sec.Group, sameID, err)
		}
	}
}

func TestGroupID_Contains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		label string
		query string
		want  bool
	}{
		{"folder spelling", "Personal Finance", "personal-finance", true},
		{"label spelling", "personal-finance", "Personal Finance", true},
		{"part of the folder name", "Personal Finance", "finan", true},
		{"default group", "", "default", true},
		{"no match", "Work", "home", false},
		{"punctuation only", "Work", "--", false},
		{"empty query", "Work", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := store.GroupIDOf(tt.label).Contains(tt.query); got != tt.want {
				t.Fatalf("GroupIDOf(%q).Contains(%q) = %v, want %v", tt.label, tt.query, got, tt.want)
			}
		})
	}
}
