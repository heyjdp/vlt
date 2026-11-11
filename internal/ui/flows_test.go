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

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

// F15: end-to-end flows through the real widgets and dialogs, headless.

func newFlowApp(t *testing.T, secrets ...*store.Secret) (*App, fyne.Window) {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, secrets...)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	return app, w
}

func TestFlow_WrongPassphraseShowsError(t *testing.T) {
	app, _ := newFlowApp(t)

	test.Type(app.unlockScreen.entry, "not the passphrase")
	test.Tap(app.unlockScreen.unlockBtn)

	if app.session.Store() != nil {
		t.Fatal("a wrong passphrase must not unlock the vault")
	}
	if !app.unlockScreen.errorLabel.Visible() || app.unlockScreen.errorLabel.Text != "Incorrect passphrase" {
		t.Fatalf("expected \"Incorrect passphrase\", got %q", app.unlockScreen.errorLabel.Text)
	}
	if app.unlockScreen.entry.Text != "" {
		t.Fatal("the entry must be cleared after a failed attempt")
	}
}

func TestFlow_DeleteAsksForConfirmation(t *testing.T) {
	app, w := newFlowApp(t, testutil.PasswordSecret("gh", "x", "work"))
	unlock(app)
	app.secretList.list.Select(0)

	test.Tap(findButton(app.detailView.Content(), "Delete"))
	test.Tap(dialogButton(t, w, "No"))
	if len(app.secretList.secrets) != 1 {
		t.Fatal("answering No must not delete the secret")
	}

	test.Tap(findButton(app.detailView.Content(), "Delete"))
	test.Tap(dialogButton(t, w, "Yes"))
	if len(app.secretList.secrets) != 0 {
		t.Fatal("answering Yes should delete the secret")
	}
	if _, err := app.session.Store().Get(context.Background(), "gh", "work"); err == nil {
		t.Fatal("the secret should be gone from the vault")
	}
	app.Lock()
}

func TestFlow_ImportAsksForGroupThenConfirmsReplace(t *testing.T) {
	app, w := newFlowApp(t)
	unlock(app)
	fo := app.detailView.fileOps

	fo.processImport(&fakeURIReader{Reader: strings.NewReader("v1"), uri: storage.NewFileURI("/tmp/id_rsa")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "OK")) // the completion message

	got, err := app.session.Store().Get(context.Background(), "id_rsa", "keys")
	if err != nil {
		t.Fatalf("imported file not saved in the chosen group: %v", err)
	}
	if got.Value != "djE=" { // base64("v1")
		t.Fatalf("unexpected content %q", got.Value)
	}

	// Importing a file with the same name asks before replacing it; "No"
	// keeps the original.
	fo.processImport(&fakeURIReader{Reader: strings.NewReader("v2"), uri: storage.NewFileURI("/tmp/id_rsa")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "No"))
	if got, _ := app.session.Store().Get(context.Background(), "id_rsa", "keys"); got.Value != "djE=" {
		t.Fatalf("answering No must keep the original file, got %q", got.Value)
	}

	fo.processImport(&fakeURIReader{Reader: strings.NewReader("v2"), uri: storage.NewFileURI("/tmp/id_rsa")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "Yes")) // replace

	got, err = app.session.Store().Get(context.Background(), "id_rsa", "keys")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "djI=" { // base64("v2")
		t.Fatalf("confirmed replace did not store the new content: %q", got.Value)
	}
	app.Lock()
}

func TestFlow_ImportUsesTheExistingGroupSpelling(t *testing.T) {
	app, w := newFlowApp(t, testutil.PasswordSecret("gh", "x", "SSH Keys"))
	unlock(app)

	app.detailView.fileOps.processImport(&fakeURIReader{Reader: strings.NewReader("k"), uri: storage.NewFileURI("/tmp/id_ed25519")})
	test.Type(dialogEntry(t, w), "ssh keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "OK")) // the completion message

	got, err := app.session.Store().Get(context.Background(), "id_ed25519", "ssh-keys")
	if err != nil {
		t.Fatal(err)
	}
	if got.Group != "SSH Keys" {
		t.Fatalf("imported into group %q, want the existing spelling", got.Group)
	}
	app.Lock()
}

func TestFlow_ChangePassphraseThroughSettings(t *testing.T) {
	app, w := newFlowApp(t, testutil.PasswordSecret("gh", "x", "work"))
	unlock(app)
	ss := app.openSettings(app.session.Store(), app.run)
	box := ss.buildChangePassphrase()
	current := entryWithPlaceholder(t, box, "Current passphrase")
	newPass := entryWithPlaceholder(t, box, "New passphrase")
	confirm := entryWithPlaceholder(t, box, "Confirm new passphrase")

	test.Type(current, "correct horse battery")
	test.Type(newPass, "an even better passphrase")
	test.Type(confirm, "an even better passphrase")
	test.Tap(findButton(box, "Change Passphrase"))

	if app.session.Store() != nil || app.unlockScreen == nil {
		t.Fatal("a passphrase change should relock the vault")
	}
	test.Tap(dialogButton(t, w, "OK")) // "Passphrase Changed"

	test.Type(app.unlockScreen.entry, "correct horse battery")
	test.Tap(app.unlockScreen.unlockBtn)
	if app.session.Store() != nil {
		t.Fatal("the old passphrase must no longer unlock the vault")
	}
	test.Type(app.unlockScreen.entry, "an even better passphrase")
	test.Tap(app.unlockScreen.unlockBtn)
	if app.session.Store() == nil {
		t.Fatal("the new passphrase should unlock the vault")
	}
	app.Lock()
}

func TestFlow_SwitchVaultThroughSettings(t *testing.T) {
	app, w := newFlowApp(t)
	unlock(app)
	ss := app.openSettings(app.session.Store(), app.run)
	box := ss.buildVaultLocation()
	path := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server")
	newDir := filepath.Join(t.TempDir(), "other")

	path.SetText(newDir)
	test.Tap(findButton(box, "Switch Vault (no copy)"))
	test.Tap(dialogButton(t, w, "Yes"))

	if app.config.VaultDir != newDir || app.createScreen == nil {
		t.Fatal("switching to an empty folder should relock into the create screen for it")
	}
	app.Lock()
}

func TestFlow_ExportWritesTheFile(t *testing.T) {
	app, _ := newFlowApp(t, testutil.FileSecret("notes", "notes.txt", "aGVsbG8=", "docs")) // "hello"
	unlock(app)
	fo := app.detailView.fileOps

	fo.ExportFile(app.secretList.secrets[0]) // decodes, then shows the save dialog
	if fo.pending == nil {
		t.Fatal("decoded content should be pending until a path is chosen")
	}
	out := storage.NewFileURI(filepath.Join(t.TempDir(), "notes.txt"))
	writer, err := storage.Writer(out)
	if err != nil {
		t.Fatal(err)
	}
	// What the save dialog's callback does once the user picks a path.
	fo.saveExport(writer, nil, "notes", "notes.txt", fo.pending)
	if fo.pending != nil {
		t.Fatal("saving should consume the pending buffer")
	}

	r, err := storage.Reader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("exported file holds %q", got)
	}
	app.Lock()
}

// exportTo runs the export of the app's first secret to path, as the save
// dialog does once a path is picked: Fyne has already created (or
// truncated) the file with os.Create.
func exportTo(t *testing.T, app *App, path string) {
	t.Helper()
	fo := app.detailView.fileOps
	fo.ExportFile(app.secretList.secrets[0])
	writer, err := storage.Writer(storage.NewFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	fo.saveExport(writer, nil, "notes", filepath.Base(path), fo.pending)
}

func TestFlow_ExportIsReadableOnlyByTheUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix file modes")
	}
	app, _ := newFlowApp(t, testutil.FileSecret("key", "id_ed25519", "aGVsbG8=", "ssh")) // "hello"
	unlock(app)
	path := filepath.Join(t.TempDir(), "id_ed25519")

	exportTo(t, app, path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("exported file has mode %v; want 0600, like the vault's files", mode)
	}
	app.Lock()
}

// Fyne creates the file before vlt sees it, so another user could open it
// then and keep reading through that handle; vlt writes a new file instead.
func TestFlow_ExportReplacesAFileSomeoneElseHasOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows writes in place; its folder's ACL decides who can read")
	}
	app, _ := newFlowApp(t, testutil.FileSecret("key", "id_ed25519", "aGVsbG8=", "ssh")) // "hello"
	unlock(app)
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	snoop, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer snoop.Close()

	exportTo(t, app, path)

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
	app.Lock()
}

// A file planted at the path, say a symlink in a shared folder, is
// replaced; the export never writes through it. (The save dialog itself has
// already emptied the link's target: Fyne creates the file with os.Create.)
func TestFlow_ExportReplacesASymlinkRatherThanFollowingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	app, _ := newFlowApp(t, testutil.FileSecret("key", "id_ed25519", "aGVsbG8=", "ssh")) // "hello"
	unlock(app)
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_ed25519")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	exportTo(t, app, path)

	if got, err := os.ReadFile(target); err != nil || bytes.Contains(got, []byte("hello")) {
		t.Fatalf("the symlink's target holds %q, %v; the export must not write through it", got, err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("path is %v, %v; want a regular 0600 file", info.Mode(), err)
	}
	app.Lock()
}

// In a folder that takes no new files, the file the dialog created (and
// emptied) is made private and written in place, rather than lost.
func TestFlow_ExportIntoAReadOnlyFolderWritesInPlace(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix folder permissions that apply to this user")
	}
	app, _ := newFlowApp(t, testutil.FileSecret("key", "id_ed25519", "aGVsbG8=", "ssh")) // "hello"
	unlock(app)
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	exportTo(t, app, path)

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
	app.Lock()
}
