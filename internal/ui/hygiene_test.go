package ui

import (
	"bytes"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/testutil"
)

// F11: passphrases and decrypted secrets must not stay reachable from
// widgets or the app once they are no longer needed.

func TestUnlockScreen_EntryIsClearedAfterSubmit(t *testing.T) {
	r := newAppRig(t, withQueuedRunner())

	r.unlockWith("wrong passphrase")
	if r.passphraseLeft() != "" {
		t.Fatal("the passphrase must be cleared from the entry as soon as it is read")
	}
	r.flush()
	if r.passphraseLeft() != "" {
		t.Fatal("a failed unlock must not restore the passphrase")
	}

	r.unlockWith("correct horse battery")
	if r.passphraseLeft() != "" {
		t.Fatal("the passphrase must be cleared before the vault opens")
	}
	r.flush()
	r.lock()
	r.flush()
}

func TestApp_LockDropsAllReferencesToSecrets(t *testing.T) {
	r := newAppRig(t, withSecrets(testutil.PasswordSecret("gh", "ghp_secret", "work")))
	r.unlock()
	list, detail := r.mainView().list, r.mainView().detail
	list.list.Select(0) // render a form holding the secret

	r.lock()

	if r.shownScreen() == screenMain {
		t.Fatal("after lock the app must not reference the main view (list, detail panel, tracker)")
	}
	if list.secrets != nil || list.filtered != nil || list.selected != nil {
		t.Fatal("the old list must drop its decrypted secrets")
	}
	if detail.current != nil || len(detail.content.Objects) != 0 {
		t.Fatal("the old detail panel must drop the shown secret and its form")
	}
}

func TestSettings_PassphraseEntriesAreClearedOnEveryPath(t *testing.T) {
	r, ss := settingsApp(t)
	box := ss.buildChangePassphrase()
	current := entryWithPlaceholder(t, box, "Current passphrase")
	newPass := entryWithPlaceholder(t, box, "New passphrase")
	confirm := entryWithPlaceholder(t, box, "Confirm new passphrase")
	change := findButton(box, "Change Passphrase")

	// A validation failure (mismatched confirmation).
	test.Type(current, "correct horse battery")
	test.Type(newPass, "a brand new passphrase")
	test.Type(confirm, "a different passphrase")
	test.Tap(change)

	for i, e := range []*widget.Entry{current, newPass, confirm} {
		if e.Text != "" {
			t.Fatalf("entry %d still holds a passphrase after a failed change", i)
		}
	}
	r.lock()
}

func TestApp_LockEmptiesSecretsAndBlanksFormsStillReachableElsewhere(t *testing.T) {
	r := newAppRig(t, withSecrets(testutil.PasswordSecret("gh", "ghp_secret", "work")))
	w := r.window
	r.unlock()

	loaded := r.listed()[0]
	r.selectSecret(0)
	formEntries := containedEntries(r.detail())
	if len(formEntries) == 0 {
		t.Fatal("expected entries in the secret form")
	}
	// Every entry in the form, looking inside widgets too, must be one that
	// blankEntries can reach (it only walks containers): an entry wrapped in
	// a widget would keep its text after the lock.
	if all := deepEntries(r.detail()); len(all) != len(formEntries) {
		t.Fatalf("%d entries in the form, but only %d are in plain containers", len(all), len(formEntries))
	}
	group := findType[*widget.SelectEntry](r.detail())
	if group == nil || group.Text == "" {
		t.Fatal("expected the group selector to show the group")
	}

	r.lock()

	// Fyne's renderer cache, or a closure in a cached widget, may still
	// point at these; they must no longer hold the secret.
	if loaded.Value != "" || loaded.Name != "" {
		t.Fatalf("a loaded secret still holds data after lock: %+v", *loaded)
	}
	for i, e := range formEntries {
		if e.Text != "" {
			t.Fatalf("form entry %d still shows %q after lock", i, e.Text)
		}
	}
	if group.Text != "" {
		t.Fatalf("the group selector still shows %q after lock", group.Text)
	}
	if w.Canvas().OnTypedKey() != nil || w.Canvas().OnTypedRune() != nil {
		t.Fatal("the session's key handlers must be removed on lock")
	}
}

func TestApp_LockWipesPendingExport(t *testing.T) {
	r := newAppRig(t) // the scripted save dialog is never answered
	r.unlock()

	data := []byte("decoded file content")
	r.mainView().files.exportDecoded("n", "out.txt", data) // save dialog left open
	r.lock()                                               // dismisses it without an answer

	if !bytes.Equal(data, make([]byte, len(data))) {
		t.Fatal("decoded export content must be wiped when the lock dismisses its dialog")
	}
}
