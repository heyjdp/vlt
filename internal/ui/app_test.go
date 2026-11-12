package ui

import (
	"context"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

func TestApp_UnlockLoadsSecretsIntoList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir,
		testutil.PasswordSecret("gh", "x", "work"),
		testutil.PasswordSecret("bank", "y", "personal"),
	)
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)

	unlock(app)

	if app.main == nil {
		t.Fatal("expected the main view after unlocking")
	}
	if got := len(app.main.list.secrets); got != 2 {
		t.Fatalf("expected 2 secrets in the list, got %d", got)
	}
	app.Lock()
}

func TestApp_UnlockRunsOffTheMainGoroutine(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), q.runner(), noTimers, testKDF)
	q.flush() // the Exists check

	unlock(app)

	if app.session.Store() != nil {
		t.Fatal("unlock must not open the vault synchronously in the tap handler")
	}
	if !app.unlockScreen.unlockBtn.Disabled() || app.unlockScreen.progress.Hidden {
		t.Fatal("unlock screen should show progress and disable input while opening")
	}

	q.flush()
	if app.session.Store() == nil || app.main == nil {
		t.Fatal("expected the vault to be open once background work completes")
	}
	app.Lock()
	q.flush()
}

func TestApp_StaleUnlockIsDiscardedAfterVaultSwitch(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), q.runner(), noTimers, testKDF)
	q.flush()

	unlock(app)
	// Before the open completes, the vault directory changes and the app relocks.
	app.settings.ProposeVault(localAt(t.TempDir()))
	app.Lock()
	q.flush()

	if app.session.Store() != nil {
		t.Fatal("an unlock result for a superseded screen must be discarded")
	}
	if app.createScreen == nil {
		t.Fatal("expected the create screen for the newly selected empty directory")
	}
}

func TestApp_SaveFromListSelectionUpdatesList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "old", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	sec := app.main.list.secrets[0]
	edited := *sec
	edited.Value = "new"
	app.main.detail.saveSecret(&edited, false, saveButton(), func() { *sec = edited })

	if app.main.list.secrets[0].Value != "new" {
		t.Fatal("saving a secret selected from the list should update the list")
	}
	app.Lock()
}

func TestApp_SaveNewSecretAddsItToList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	sec := testutil.PasswordSecret("new-one", "v", "work")
	app.main.detail.saveSecret(sec, true, saveButton(), func() {})

	if len(app.main.list.secrets) != 1 || app.main.list.secrets[0].Name != "new-one" {
		t.Fatalf("a newly created secret should be added to the list, got %d entries", len(app.main.list.secrets))
	}
	app.Lock()
}

func TestApp_DeleteRemovesSecretFromList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gone", "v", "work"), testutil.PasswordSecret("kept", "v", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	var target *store.Secret
	for _, s := range app.main.list.secrets {
		if s.Name == "gone" {
			target = s
		}
	}
	app.main.detail.deleteSecret(target)

	if len(app.main.list.secrets) != 1 || app.main.list.secrets[0].Name != "kept" {
		t.Fatalf("deleted secret should leave the list, got %d entries", len(app.main.list.secrets))
	}
	app.Lock()
}

func TestApp_ImportAddsSecretToList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	app.main.files.importSecret(testutil.FileSecret("id_rsa", "id_rsa", "eA==", "keys"), importedFile{size: 1})

	if len(app.main.list.secrets) != 1 || app.main.list.secrets[0].Name != "id_rsa" {
		t.Fatal("an imported file should be added to the list")
	}
	app.Lock()
}

func TestApp_SaveCompletingAfterLockIsDropped(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), q.runner(), noTimers, testKDF)
	q.flush()
	unlockQueued(t, app, q)
	q.flush() // initial list load

	app.main.detail.saveSecret(testutil.PasswordSecret("late", "v", "work"), true, saveButton(), func() {})
	app.Lock()
	q.flush()

	if got := app.statusBar.Text; got != "Locked" {
		t.Fatalf("a save finishing after lock must not touch the UI; status bar says %q", got)
	}
}

func TestApp_LockCancelsInitialListLoad(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "x", "work"))
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), q.runner(), noTimers, testKDF)
	q.flush()
	unlockQueued(t, app, q)
	list := app.main.list

	app.Lock()
	q.flush()

	if len(list.secrets) != 0 {
		t.Fatal("a list load cancelled by Lock must not populate the list")
	}
}

func TestApp_CreateRunsOffTheMainGoroutine(t *testing.T) {
	a := test.NewTempApp(t)
	dir := filepath.Join(t.TempDir(), "vault")
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), q.runner(), noTimers, testKDF)
	q.flush()

	test.Type(app.createScreen.entry, "correct horse battery")
	test.Type(app.createScreen.confirm, "correct horse battery")
	test.Tap(app.createScreen.createBtn)

	if exists, _ := store.Exists(context.Background(), dir); exists {
		t.Fatal("create must not run synchronously in the tap handler")
	}
	if !app.createScreen.createBtn.Disabled() || app.createScreen.progress.Hidden {
		t.Fatal("create screen should show progress and disable input while creating")
	}
	q.flush()
	if app.main == nil {
		t.Fatal("expected the main view once the vault is created")
	}
	app.Lock()
	q.flush()
}

func TestSecretList_SaveDuringFirstLoadSurvivesLoad(t *testing.T) {
	test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("on-disk", "v", "work"))
	s, err := store.Open(context.Background(), dir, "correct horse battery", quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	q := &queue{}
	sl := listOver(t, s, q.runner())

	sl.Refresh() // queued: as if the load read the vault before the save below
	sl.Upsert(testutil.PasswordSecret("saved-meanwhile", "v", "work"))
	q.flush()

	names := map[string]bool{}
	for _, sec := range sl.secrets {
		names[sec.Name] = true
	}
	if !names["on-disk"] || !names["saved-meanwhile"] {
		t.Fatalf("expected both the loaded and the concurrently saved secret, got %v", names)
	}
}
