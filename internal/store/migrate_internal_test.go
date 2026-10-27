package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/yourorg/vlt/internal/crypto"
)

const internalPass = "internal-test-passphrase"

// writeLegacy writes a format-1 vault. It duplicates testutil.WriteLegacyVault
// because testutil imports store, and an internal test cannot import it.
func writeLegacy(t *testing.T, dir string, names ...string) []string {
	t.Helper()
	enc := func(plaintext []byte) []byte {
		r, err := age.NewScryptRecipient(internalPass)
		if err != nil {
			t.Fatal(err)
		}
		r.SetWorkFactor(10)
		var buf bytes.Buffer
		w, err := age.Encrypt(&buf, r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(plaintext); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	if err := os.MkdirAll(filepath.Join(dir, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, keyFile), enc([]byte(legacySentinel)), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(names))
	for _, name := range names {
		data, err := json.Marshal(Secret{Version: 1, Type: TypePassword, Name: name, Group: "work", Value: name})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "work", Slugify(name)+".age")
		if err := os.WriteFile(path, enc(data), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestMigration_ResumesAfterCrashMidway simulates a crash after step 1 and
// one file of step 2: the vault must still open and finish migrating
// without rewriting the file that was already done.
func TestMigration_ResumesAfterCrashMidway(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := writeLegacy(t, dir, "a", "b", "c")

	id, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := crypto.WrapIdentity(internalPass, id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(dir, migratingFile), wrapped); err != nil {
		t.Fatal(err)
	}
	partial := New(id, dir, quiet())
	if done, err := partial.migrateFile(internalPass, paths[0]); err != nil || !done {
		t.Fatalf("migrateFile: done=%v err=%v", done, err)
	}
	firstAfterCrash, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	id.Zero()

	s, err := Open(context.Background(), dir, internalPass, quiet(), WithWorkFactor(10))
	if err != nil {
		t.Fatalf("Open after crash: %v", err)
	}
	defer s.Close()

	for _, name := range []string{"a", "b", "c"} {
		if _, err := s.Get(context.Background(), name, "work"); err != nil {
			t.Fatalf("Get %q: %v", name, err)
		}
	}
	if skipped := s.SkippedFiles(); len(skipped) != 0 {
		t.Fatalf("already-migrated file was treated as undecryptable: %v", skipped)
	}
	firstNow, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstAfterCrash, firstNow) {
		t.Fatal("already-migrated file was rewritten on resume")
	}
	if _, err := os.Stat(filepath.Join(dir, migratingFile)); !os.IsNotExist(err) {
		t.Fatalf("migration marker should be gone, stat err = %v", err)
	}
}

// TestMigration_ResumesAfterKeySwapBeforeMarker simulates a crash between
// step 3 (vlt.age replaced) and step 4 (marker written, migrating file gone).
func TestMigration_ResumesAfterKeySwapBeforeMarker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLegacy(t, dir, "a")
	s, err := Open(context.Background(), dir, internalPass, quiet(), WithWorkFactor(10))
	if err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	s.Close()

	key, err := os.ReadFile(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, migratingFile), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, formatFile)); err != nil {
		t.Fatal(err)
	}

	s, err = Open(context.Background(), dir, internalPass, quiet(), WithWorkFactor(10))
	if err != nil {
		t.Fatalf("Open after crash: %v", err)
	}
	defer s.Close()
	if !s.hasFormatMarker() {
		t.Fatal("format marker not restored")
	}
	if _, err := s.Get(context.Background(), "a", "work"); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

// withKDFCounter counts scrypt derivations performed through a Store.
func withKDFCounter(n *int) Option {
	return func(o *options) { o.onKDF = func() { *n++ } }
}

// TestOpen_RunsExactlyOneScrypt measures derivations at runtime, not just the
// on-disk layout: unlocking and listing a 100-secret vault costs one scrypt.
func TestOpen_RunsExactlyOneScrypt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := Create(dir, internalPass, quiet(), WithWorkFactor(10))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		sec := &Secret{Type: TypePassword, Name: "secret-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Value: "v"}
		if err := s.Create(context.Background(), sec); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	var derivations int
	opened, err := Open(context.Background(), dir, internalPass, quiet(), WithWorkFactor(10), withKDFCounter(&derivations))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer opened.Close()
	listed, err := opened.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 100 {
		t.Fatalf("expected 100 secrets, got %d", len(listed))
	}
	if derivations != 1 {
		t.Fatalf("expected exactly 1 scrypt derivation for unlock+list, got %d", derivations)
	}
}

// TestBeginMigration_JoinsMigrationStartedElsewhere simulates two instances
// unlocking the same legacy vault: the second must adopt the first's key, not
// replace it.
func TestBeginMigration_JoinsMigrationStartedElsewhere(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLegacy(t, dir, "a", "b")

	other, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Zero()
	wrapped, err := crypto.WrapIdentity(internalPass, other, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileExclusive(filepath.Join(dir, migratingFile), wrapped); err != nil {
		t.Fatal(err)
	}

	s, err := beginMigration(context.Background(), dir, internalPass, quiet(), options{workFactor: 10})
	if err != nil {
		t.Fatalf("beginMigration: %v", err)
	}
	defer s.Close()

	same, err := sameKey(s.id, other)
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Fatal("second migrator replaced the first one's vault key")
	}
	for _, name := range []string{"a", "b"} {
		if _, err := s.Get(context.Background(), name, "work"); err != nil {
			t.Fatalf("Get %q: %v", name, err)
		}
	}
}

func TestWriteFileExclusive_FailsIfTargetExists(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x")
	if err := writeFileExclusive(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileExclusive(path, []byte("second")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Fatalf("existing file was replaced: %q", got)
	}
}

// writeRaw stores a secret JSON document at an explicit path, as versions
// before F03 could (untrimmed names, or names whose slug is empty).
func writeRaw(t *testing.T, s *Store, path string, sec *Secret) {
	t.Helper()
	data, err := json.Marshal(sec)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := crypto.Encrypt(s.id, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ct, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLegacyNames_StayEditableAndDeletable covers secrets saved before F03's
// stricter name rules: an untrimmed name and a punctuation-only name stored
// at <group>/.age must still be readable, updatable and deletable.
func TestLegacyNames_StayEditableAndDeletable(t *testing.T) {
	t.Parallel()

	id, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := New(id, dir, quiet())
	defer s.Close()
	ctx := context.Background()

	writeRaw(t, s, filepath.Join(dir, "work", "foo.age"), &Secret{Version: 1, Type: TypePassword, Name: "foo ", Group: "work", Value: "old"})
	writeRaw(t, s, filepath.Join(dir, "work", ".age"), &Secret{Version: 1, Type: TypePassword, Name: "!!!", Group: "work", Value: "old"})

	listed, err := s.List(ctx)
	if err != nil || len(listed) != 2 {
		t.Fatalf("List: %d secrets, err %v", len(listed), err)
	}

	for _, name := range []string{"foo", "!!!"} {
		if err := s.Update(ctx, &Secret{Type: TypePassword, Name: name, Group: "work", Value: "new"}); err != nil {
			t.Fatalf("Update %q: %v", name, err)
		}
		got, err := s.Get(ctx, name, "work")
		if err != nil {
			t.Fatalf("Get %q: %v", name, err)
		}
		if got.Value != "new" || got.Name != name {
			t.Fatalf("Update %q stored %+v", name, got)
		}
		if err := s.Delete(ctx, name, "work"); err != nil {
			t.Fatalf("Delete %q: %v", name, err)
		}
	}
}

func TestCreateExclusive_RefusesExistingAndWritesNew(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.age")
	if err := createExclusive(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := createExclusive(path, []byte("second")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Fatalf("existing file replaced: %q", got)
	}
}
