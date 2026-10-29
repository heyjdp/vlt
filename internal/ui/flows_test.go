package ui

import (
	"context"
	"io"
	"path/filepath"
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

	if app.store != nil {
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
	if _, err := app.store.Get(context.Background(), "gh", "work"); err == nil {
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

	got, err := app.store.Get(context.Background(), "id_rsa", "keys")
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
	if got, _ := app.store.Get(context.Background(), "id_rsa", "keys"); got.Value != "djE=" {
		t.Fatalf("answering No must keep the original file, got %q", got.Value)
	}

	fo.processImport(&fakeURIReader{Reader: strings.NewReader("v2"), uri: storage.NewFileURI("/tmp/id_rsa")})
	test.Type(dialogEntry(t, w), "keys")
	test.Tap(dialogButton(t, w, "Import"))
	test.Tap(dialogButton(t, w, "Yes")) // replace

	got, err = app.store.Get(context.Background(), "id_rsa", "keys")
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

	got, err := app.store.Get(context.Background(), "id_ed25519", "ssh-keys")
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
	ss := app.openSettings(app.store, app.run)
	box := ss.buildChangePassphrase()
	current := entryWithPlaceholder(t, box, "Current passphrase")
	newPass := entryWithPlaceholder(t, box, "New passphrase")
	confirm := entryWithPlaceholder(t, box, "Confirm new passphrase")

	test.Type(current, "correct horse battery")
	test.Type(newPass, "an even better passphrase")
	test.Type(confirm, "an even better passphrase")
	test.Tap(findButton(box, "Change Passphrase"))

	if app.store != nil || app.unlockScreen == nil {
		t.Fatal("a passphrase change should relock the vault")
	}
	test.Tap(dialogButton(t, w, "OK")) // "Passphrase Changed"

	test.Type(app.unlockScreen.entry, "correct horse battery")
	test.Tap(app.unlockScreen.unlockBtn)
	if app.store != nil {
		t.Fatal("the old passphrase must no longer unlock the vault")
	}
	test.Type(app.unlockScreen.entry, "an even better passphrase")
	test.Tap(app.unlockScreen.unlockBtn)
	if app.store == nil {
		t.Fatal("the new passphrase should unlock the vault")
	}
	app.Lock()
}

func TestFlow_SwitchVaultThroughSettings(t *testing.T) {
	app, w := newFlowApp(t)
	unlock(app)
	ss := app.openSettings(app.store, app.run)
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
