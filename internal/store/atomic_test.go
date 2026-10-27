package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFileAtomic_WritesContentsWithPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "secret.age")

	if err := writeFileAtomic(path, []byte("ciphertext"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "ciphertext" {
		t.Fatalf("contents mismatch: got %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("permissions: got %o, want 600", perm)
		}
	}
}

func TestWriteFileAtomic_ReplacesExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "secret.age")
	if err := writeFileAtomic(path, []byte("old-contents-that-are-longer"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("contents mismatch: got %q", got)
	}
}

func TestWriteFileAtomic_RenameFailureRemovesTempFile(t *testing.T) {
	t.Parallel()

	// A non-empty directory at the target path makes Rename fail after the
	// temp file has been created, written, and synced, exercising cleanup.
	dir := t.TempDir()
	target := filepath.Join(dir, "secret.age")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := writeFileAtomic(target, []byte("data"), 0o600); err == nil {
		t.Fatal("expected rename onto a non-empty directory to fail")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			t.Fatalf("temp file %q left behind after failed rename", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(target, "child")); err != nil {
		t.Fatalf("target directory was disturbed: %v", err)
	}
}

// TestWriteFileAtomic_FailureLeavesOriginalIntact guards the no-truncation
// property: a write that cannot complete must not touch the existing file.
func TestWriteFileAtomic_FailureLeavesOriginalIntact(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only directory semantics need a non-root Unix user")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "secret.age")
	if err := writeFileAtomic(path, []byte("original"), 0o600); err != nil {
		t.Fatalf("setup write: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := writeFileAtomic(path, []byte("replacement"), 0o600); err == nil {
		t.Fatal("expected write into read-only directory to fail")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("original file was modified: got %q", got)
	}
}

func TestWriteFileAtomic_LeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for i := range 3 {
		if err := writeFileAtomic(filepath.Join(dir, "a.age"), []byte{byte(i)}, 0o600); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one file, got %d", len(entries))
	}
}
