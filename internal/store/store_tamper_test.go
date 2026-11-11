package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/heyjdp/vlt/internal/crypto"
	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

// damagedSecrets fills s with files a hostile server, a shared folder or a
// bad disk could leave, beside one good secret, "good" in group "work". It
// returns where each damaged file is, by the name and group it is read as.
func damagedSecrets(t *testing.T, s *store.Store, id *crypto.Identity) map[string][2]string {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{"good", "flipped", "swapped"} {
		if err := s.Create(ctx, testutil.PasswordSecret(name, "pw-"+name, "work")); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}
	work := filepath.Join(s.Dir(), "work")
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sealed := func(plaintext string) []byte {
		t.Helper()
		data, err := crypto.Encrypt(id, []byte(plaintext))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	good := read(filepath.Join(work, "good.age"))
	flipped := read(filepath.Join(work, "flipped.age"))
	flipped[len(flipped)-1] ^= 0x01
	write(filepath.Join(work, "flipped.age"), flipped)

	// Valid files of this vault, put where they don't belong.
	write(filepath.Join(work, "swapped.age"), good)
	write(filepath.Join(s.Dir(), "personal", "good.age"), good)

	// A secret from another vault, under a name of this one.
	other := newTestStore(t)
	if err := other.Create(ctx, testutil.PasswordSecret("foreign", "theirs", "work")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(work, "foreign.age"), read(filepath.Join(other.Dir(), "work", "foreign.age")))

	// Encrypted to this vault's key, but not secrets.
	write(filepath.Join(work, "garbage.age"), sealed("not json"))
	write(filepath.Join(work, "empty.age"), sealed("{}"))
	write(filepath.Join(work, "untyped.age"), sealed(`{"type":"note","name":"untyped","group":"work"}`))
	write(filepath.Join(work, "nameless.age"), sealed(`{"type":"password","name":" ","group":"work","value":"x"}`))

	return map[string][2]string{
		"a flipped byte":          {"flipped", "work"},
		"another secret's file":   {"swapped", "work"},
		"another group's file":    {"good", "personal"},
		"another vault's file":    {"foreign", "work"},
		"not JSON":                {"garbage", "work"},
		"an empty object":         {"empty", "work"},
		"an unknown type":         {"untyped", "work"},
		"a secret without a name": {"nameless", "work"},
	}
}

func TestStore_Get_RefusesDamagedForeignOrMisplacedFiles(t *testing.T) {
	t.Parallel()

	s, id := newTestStoreAndKey(t, t.TempDir())
	for name, at := range damagedSecrets(t, s, id) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := s.Get(context.Background(), at[0], at[1])
			if err == nil || got != nil {
				t.Fatalf("Get(%q, %q) = %+v, %v; want an error", at[0], at[1], got, err)
			}
			if errors.Is(err, store.ErrSecretNotFound) {
				t.Fatalf("a file that is there but can't be used is reported as missing: %v", err)
			}
		})
	}
	if got, err := s.Get(context.Background(), "good", "work"); err != nil || got.Value != "pw-good" {
		t.Fatalf("Get(good) = %+v, %v; the good secret should still open", got, err)
	}
}

func TestStore_Contents_SkipsDamagedForeignOrMisplacedFilesAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	s, id := newTestStoreAndKey(t, t.TempDir())
	damagedSecrets(t, s, id)

	got, err := s.Contents(context.Background())
	if err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if len(got.Secrets) != 1 || got.Secrets[0].Name != "good" || got.Secrets[0].Group != "work" || got.Secrets[0].Value != "pw-good" {
		t.Fatalf("Contents = %v; want only the good secret, once", names(got.Secrets))
	}
}

func TestStore_Contents_NamesTheFilesItCouldNotRead(t *testing.T) {
	t.Parallel()

	s, id := newTestStoreAndKey(t, t.TempDir())
	damagedSecrets(t, s, id)

	got, err := s.Contents(context.Background())
	if err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if len(got.Secrets) != 1 || got.Secrets[0].Name != "good" {
		t.Fatalf("Contents.Secrets = %v; want only the good secret", names(got.Secrets))
	}
	want := []string{
		"personal/good.age", "work/empty.age", "work/flipped.age", "work/foreign.age",
		"work/garbage.age", "work/nameless.age", "work/swapped.age", "work/untyped.age",
	}
	if !slices.Equal(got.Unreadable, want) {
		t.Fatalf("Contents.Unreadable = %v; want %v", got.Unreadable, want)
	}
}

func TestStore_Contents_NamesAGroupFolderItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix folder permissions that apply to this user")
	}
	t.Parallel()

	s := newTestStore(t)
	ctx := context.Background()
	for _, group := range []string{"home", "work"} {
		if err := s.Create(ctx, testutil.PasswordSecret("key", "pw", group)); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(s.Dir(), "home")
	if err := os.Chmod(home, 0o300); err != nil { // can't be listed
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	got, err := s.Contents(ctx)
	if err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if len(got.Secrets) != 1 || got.Secrets[0].Group != "work" || !slices.Equal(got.Unreadable, []string{"home/"}) {
		t.Fatalf("Contents = %v, unreadable %v; want work's secret, and home/ named", names(got.Secrets), got.Unreadable)
	}
}

// A folder or file named by hand may differ from the slug in case; the
// secret is still where its name and group put it.
func TestStore_Contents_AcceptsAFileWhosePathDiffersOnlyInCase(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	if err := s.Create(context.Background(), testutil.PasswordSecret("GitHub Key", "pw", "Work")); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(s.Dir(), "work")
	to := filepath.Join(s.Dir(), "WORK")
	if err := os.Rename(filepath.Join(from, "github-key.age"), filepath.Join(from, "GitHub-Key.age")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}

	got, err := s.Contents(context.Background())
	if err != nil || len(got.Secrets) != 1 || got.Secrets[0].Name != "GitHub Key" {
		t.Fatalf("Contents = %v, %v; want the secret despite the case of its path", names(got.Secrets), err)
	}
}

func names(secrets []*store.Secret) []string {
	out := make([]string, len(secrets))
	for i, s := range secrets {
		out[i] = s.Group + "/" + s.Name
	}
	return out
}
