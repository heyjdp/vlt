package ui

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"

	"github.com/heyjdp/vlt/internal/testutil"
)

// F15: end-to-end flows through the real widgets and dialogs, headless.

func TestFlow_WrongPassphraseShowsError(t *testing.T) {
	r := newAppRig(t)

	r.unlockWith("not the passphrase")

	if !r.locked() {
		t.Fatal("a wrong passphrase must not unlock the vault")
	}
	if got := r.screenError(); got != "Incorrect passphrase" {
		t.Fatalf("expected \"Incorrect passphrase\", got %q", got)
	}
	if r.passphraseLeft() != "" {
		t.Fatal("the entry must be cleared after a failed attempt")
	}
}

// A vault without vlt.age may be a move cut short; the unlock screen says
// how to finish it, and that a vault changed since needs the folder gone.
func TestFlow_AVaultWithoutItsKeyFileSaysHowToFinishAMove(t *testing.T) {
	r := newAppRig(t, withSecrets(testutil.PasswordSecret("gh", "x", "work")))
	if err := os.Remove(filepath.Join(r.dir, "vlt.age")); err != nil {
		t.Fatal(err)
	}

	r.unlock()

	text := r.screenError()
	for _, want := range []string{"switch back to the original vault and move it here again", "delete this folder first", "restore vlt.age from a backup"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the unlock screen says %q; want it to say %q", text, want)
		}
	}
}

func TestFlow_DeleteAsksForConfirmation(t *testing.T) {
	r := newAppRig(t, withSecrets(testutil.PasswordSecret("gh", "x", "work")))
	w := r.window
	r.unlock()
	r.selectSecret(0)

	test.Tap(findButton(r.detail(), "Delete"))
	test.Tap(dialogButton(t, w, "No"))
	if len(r.listed()) != 1 {
		t.Fatal("answering No must not delete the secret")
	}

	test.Tap(findButton(r.detail(), "Delete"))
	test.Tap(dialogButton(t, w, "Yes"))
	if len(r.listed()) != 0 {
		t.Fatal("answering Yes should delete the secret")
	}
	if _, err := r.store().Get(context.Background(), "gh", "work"); err == nil {
		t.Fatal("the secret should be gone from the vault")
	}
	r.lock()
}

func TestFlow_ImportAsksForGroupThenConfirmsReplace(t *testing.T) {
	r := newAppRig(t)
	w := r.window
	r.unlock()

	r.importFile(&fakeURIReader{Reader: strings.NewReader("v1"), uri: storage.NewFileURI("/tmp/id_rsa")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "OK")) // the completion message

	got, err := r.store().Get(context.Background(), "id_rsa", "keys")
	if err != nil {
		t.Fatalf("imported file not saved in the chosen group: %v", err)
	}
	if got.Value != "djE=" { // base64("v1")
		t.Fatalf("unexpected content %q", got.Value)
	}

	// Importing a file with the same name asks before replacing it; "No"
	// keeps the original.
	r.importFile(&fakeURIReader{Reader: strings.NewReader("v2"), uri: storage.NewFileURI("/tmp/id_rsa")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "No"))
	if got, _ := r.store().Get(context.Background(), "id_rsa", "keys"); got.Value != "djE=" {
		t.Fatalf("answering No must keep the original file, got %q", got.Value)
	}

	r.importFile(&fakeURIReader{Reader: strings.NewReader("v2"), uri: storage.NewFileURI("/tmp/id_rsa")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "Yes")) // replace

	got, err = r.store().Get(context.Background(), "id_rsa", "keys")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "djI=" { // base64("v2")
		t.Fatalf("confirmed replace did not store the new content: %q", got.Value)
	}
	r.lock()
}

// An import whose file name is taken by a differently named secret (one
// that differs only in case or punctuation) is refused, not replaced.
func TestFlow_ImportOverADifferentlyNamedSecretIsRefused(t *testing.T) {
	r := newAppRig(t, withSecrets(testutil.PasswordSecret("GitHub Key", "pw", "keys")))
	w := r.window
	r.unlock()

	r.importFile(&fakeURIReader{Reader: strings.NewReader("k"), uri: storage.NewFileURI("/tmp/github-key")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))

	if top := w.Canvas().Overlays().Top(); top == nil || !visibleText(top, "already used by \"GitHub Key\"") {
		t.Fatal("the import should be refused, naming the secret that holds the file")
	}
	if got, err := r.store().Get(context.Background(), "GitHub Key", "keys"); err != nil || got.Value != "pw" {
		t.Fatalf("the existing secret must be kept: %+v, %v", got, err)
	}
	r.lock()
}

// An import whose file is taken by a file that can't be read says so, and
// leaves that file alone.
func TestFlow_ImportOverAnUnreadableFileIsRefused(t *testing.T) {
	r := newAppRig(t)
	w := r.window
	r.unlock()
	taken := filepath.Join(r.store().Dir(), "keys", "id-rsa.age")
	if err := os.MkdirAll(filepath.Dir(taken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taken, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}

	r.importFile(&fakeURIReader{Reader: strings.NewReader("k"), uri: storage.NewFileURI("/tmp/id_rsa")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))

	if top := w.Canvas().Overlays().Top(); top == nil || !visibleText(top, "can't be read") {
		t.Fatal("the import should be refused, saying the file there can't be read")
	}
	if data, _ := os.ReadFile(taken); string(data) != "damaged" {
		t.Fatal("the unreadable file must be left alone")
	}
	r.lock()
}

func TestFlow_ImportUsesTheExistingGroupSpelling(t *testing.T) {
	r := newAppRig(t, withSecrets(testutil.PasswordSecret("gh", "x", "SSH Keys")))
	w := r.window
	r.unlock()

	r.importFile(&fakeURIReader{Reader: strings.NewReader("k"), uri: storage.NewFileURI("/tmp/id_ed25519")})
	test.Type(dialogEntry(t, w), "ssh keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "OK")) // the completion message

	got, err := r.store().Get(context.Background(), "id_ed25519", "ssh-keys")
	if err != nil {
		t.Fatal(err)
	}
	if got.Group != "SSH Keys" {
		t.Fatalf("imported into group %q, want the existing spelling", got.Group)
	}
	r.lock()
}

func TestFlow_ChangePassphraseThroughSettings(t *testing.T) {
	r := newAppRig(t, withSecrets(testutil.PasswordSecret("gh", "x", "work")))
	w := r.window
	r.unlock()
	ss := r.openSettings()
	box := ss.buildChangePassphrase()
	current := entryWithPlaceholder(t, box, "Current passphrase")
	newPass := entryWithPlaceholder(t, box, "New passphrase")
	confirm := entryWithPlaceholder(t, box, "Confirm new passphrase")

	test.Type(current, "correct horse battery")
	test.Type(newPass, "an even better passphrase")
	test.Type(confirm, "an even better passphrase")
	test.Tap(findButton(box, "Change Passphrase"))

	if !r.locked() || r.shownScreen() != screenUnlock {
		t.Fatal("a passphrase change should relock the vault")
	}
	test.Tap(dialogButton(t, w, "OK")) // "Passphrase Changed"

	r.unlockWith("correct horse battery")
	if !r.locked() {
		t.Fatal("the old passphrase must no longer unlock the vault")
	}
	r.unlockWith("an even better passphrase")
	if r.locked() {
		t.Fatal("the new passphrase should unlock the vault")
	}
	r.lock()
}

func TestFlow_SwitchVaultThroughSettings(t *testing.T) {
	r := newAppRig(t)
	w := r.window
	r.unlock()
	ss := r.openSettings()
	box := ss.buildVaultLocation()
	path := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server")
	newDir := filepath.Join(t.TempDir(), "other")

	path.SetText(newDir)
	test.Tap(findButton(box, "Switch Vault (no copy)"))
	test.Tap(dialogButton(t, w, "Yes"))

	if r.settings.Vault() != localAt(newDir) || r.shownScreen() != screenCreate {
		t.Fatal("switching to an empty folder should relock into the create screen for it")
	}
	r.lock()
}

func TestFlow_ExportWritesTheFile(t *testing.T) {
	r := newAppRig(t, withSecrets(testutil.FileSecret("notes", "notes.txt", "aGVsbG8=", "docs"))) // "hello"
	r.unlock()
	out := storage.NewFileURI(filepath.Join(t.TempDir(), "notes.txt"))

	exportTo(t, r, out.Path())

	f, err := storage.Reader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("exported file holds %q", got)
	}
	r.lock()
}

// exportTo exports the app's first secret to path through Export File and
// the save dialog. As Fyne's dialog does once a path is picked, the file
// has already been created (or truncated) with os.Create.
func exportTo(t *testing.T, r *appRig, path string) {
	t.Helper()
	r.selectSecret(0)
	test.Tap(findButton(r.detail(), "Export File"))
	writer, err := storage.Writer(storage.NewFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	r.pick.answerSave(t, writer, nil)
}

func TestFlow_ExportIsReadableOnlyByTheUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix file modes")
	}
	r := newAppRig(t, withSecrets(testutil.FileSecret("key", "id_ed25519", "aGVsbG8=", "ssh"))) // "hello"
	r.unlock()
	path := filepath.Join(t.TempDir(), "id_ed25519")

	exportTo(t, r, path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("exported file has mode %v; want 0600, like the vault's files", mode)
	}
	r.lock()
}

// Fyne creates the file before vlt sees it, so another user could open it
// then and keep reading through that handle; vlt writes a new file instead.
func TestFlow_ExportReplacesAFileSomeoneElseHasOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows writes in place; its folder's ACL decides who can read")
	}
	r := newAppRig(t, withSecrets(testutil.FileSecret("key", "id_ed25519", "aGVsbG8=", "ssh"))) // "hello"
	r.unlock()
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	snoop, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer snoop.Close()

	exportTo(t, r, path)

	seen, err := io.ReadAll(snoop)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(seen, []byte("hello")) {
		t.Fatal("a handle opened before the export can read the exported secret")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("exported file holds %q, %v", got, err)
	}
	r.lock()
}

// A file planted at the path, say a symlink in a shared folder, is
// replaced; the export never writes through it. (The save dialog itself has
// already emptied the link's target: Fyne creates the file with os.Create.)
func TestFlow_ExportReplacesASymlinkRatherThanFollowingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	r := newAppRig(t, withSecrets(testutil.FileSecret("key", "id_ed25519", "aGVsbG8=", "ssh"))) // "hello"
	r.unlock()
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_ed25519")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	exportTo(t, r, path)

	if got, err := os.ReadFile(target); err != nil || bytes.Contains(got, []byte("hello")) {
		t.Fatalf("the symlink's target holds %q, %v; the export must not write through it", got, err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("path is %v, %v; want a regular 0600 file", info.Mode(), err)
	}
	r.lock()
}

// In a folder that takes no new files, the file the dialog created (and
// emptied) is made private and written in place, rather than lost.
func TestFlow_ExportIntoAReadOnlyFolderWritesInPlace(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix folder permissions that apply to this user")
	}
	r := newAppRig(t, withSecrets(testutil.FileSecret("key", "id_ed25519", "aGVsbG8=", "ssh"))) // "hello"
	r.unlock()
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	exportTo(t, r, path)

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("exported file holds %q, %v", got, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("exported file is %v, %v; want 0600", info.Mode(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("folder holds %v, %v; want only the export", entries, err)
	}
	r.lock()
}
