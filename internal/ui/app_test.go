package ui

import (
	"context"
	"testing"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

func TestApp_UnlockLoadsSecretsIntoList(t *testing.T) {
	r := newAppRig(t, withSecrets(
		testutil.PasswordSecret("gh", "x", "work"),
		testutil.PasswordSecret("bank", "y", "personal"),
	))

	r.unlock()

	if r.shownScreen() != screenMain {
		t.Fatal("expected the main view after unlocking")
	}
	if got := len(r.listed()); got != 2 {
		t.Fatalf("expected 2 secrets in the list, got %d", got)
	}
	r.lock()
}

func TestApp_UnlockRunsOffTheMainGoroutine(t *testing.T) {
	r := newAppRig(t, withQueuedRunner())

	r.unlock()

	if !r.locked() {
		t.Fatal("unlock must not open the vault synchronously in the tap handler")
	}
	if !r.busy() {
		t.Fatal("unlock screen should show progress and disable input while opening")
	}

	r.flush()
	if r.locked() || r.shownScreen() != screenMain {
		t.Fatal("expected the vault to be open once background work completes")
	}
	r.lock()
	r.flush()
}

func TestApp_StaleUnlockIsDiscardedAfterVaultSwitch(t *testing.T) {
	r := newAppRig(t, withQueuedRunner())

	r.unlock()
	// Before the open completes, the vault directory changes and the app relocks.
	r.settings.ProposeVault(localAt(t.TempDir()))
	r.lock()
	r.flush()

	if !r.locked() {
		t.Fatal("an unlock result for a superseded screen must be discarded")
	}
	if r.shownScreen() != screenCreate {
		t.Fatal("expected the create screen for the newly selected empty directory")
	}
}

func TestApp_SaveCompletingAfterLockIsDropped(t *testing.T) {
	r := newAppRig(t, withQueuedRunner())
	r.unlockOpening()
	r.flush() // initial list load

	r.mainView().detail.saveSecret(testutil.PasswordSecret("late", "v", "work"), true, saveButton(), func() {})
	r.lock()
	r.flush()

	if got := r.status(); got != "Locked" {
		t.Fatalf("a save finishing after lock must not touch the UI; status bar says %q", got)
	}
}

func TestApp_LockCancelsInitialListLoad(t *testing.T) {
	r := newAppRig(t, withQueuedRunner(), withSecrets(testutil.PasswordSecret("gh", "x", "work")))
	r.unlockOpening()
	list := r.mainView().list

	r.lock()
	r.flush()

	if len(list.secrets) != 0 {
		t.Fatal("a list load cancelled by Lock must not populate the list")
	}
}

func TestApp_CreateRunsOffTheMainGoroutine(t *testing.T) {
	r := newAppRig(t, withQueuedRunner(), withoutVault())

	r.createConfirming(testPassphrase)

	if exists, _ := store.Exists(context.Background(), r.dir); exists {
		t.Fatal("create must not run synchronously in the tap handler")
	}
	if !r.busy() {
		t.Fatal("create screen should show progress and disable input while creating")
	}
	r.flush()
	if r.shownScreen() != screenMain {
		t.Fatal("expected the main view once the vault is created")
	}
	r.lock()
	r.flush()
}
