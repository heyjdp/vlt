package ui

import (
	"bytes"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

// F11: passphrases and decrypted secrets must not stay reachable from
// widgets or the app once they are no longer needed.

func TestUnlockScreen_EntryIsClearedAfterSubmit(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	q := &queue{}
	app := newApp(&AppDeps{FyneApp: a, Window: test.NewTempWindow(t, nil), Settings: settingsAt(a, dir), Log: quietLog(), run: q.runner(), timers: noTimers, storeOpts: []store.Option{testKDF}})
	q.flush()
	screen := app.unlockScreen

	test.Type(screen.entry, "wrong passphrase")
	test.Tap(screen.unlockBtn)
	if screen.entry.Text != "" {
		t.Fatal("the passphrase must be cleared from the entry as soon as it is read")
	}
	q.flush()
	if screen.entry.Text != "" {
		t.Fatal("a failed unlock must not restore the passphrase")
	}

	test.Type(screen.entry, "correct horse battery")
	test.Tap(screen.unlockBtn)
	if screen.entry.Text != "" {
		t.Fatal("the passphrase must be cleared before the vault opens")
	}
	q.flush()
	app.Lock()
	q.flush()
}

func TestApp_LockDropsAllReferencesToSecrets(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "ghp_secret", "work"))
	app := newApp(&AppDeps{FyneApp: a, Window: test.NewTempWindow(t, nil), Settings: settingsAt(a, dir), Log: quietLog(), run: inlineRunner(), timers: noTimers, storeOpts: []store.Option{testKDF}})
	unlock(app)
	list, detail := app.main.list, app.main.detail
	list.list.Select(0) // render a form holding the secret

	app.Lock()

	if app.main != nil {
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
	app, ss := settingsApp(t, noTimers)
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
	app.Lock()
}

func TestApp_LockEmptiesSecretsAndBlanksFormsStillReachableElsewhere(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "ghp_secret", "work"))
	w := test.NewTempWindow(t, nil)
	app := newApp(&AppDeps{FyneApp: a, Window: w, Settings: settingsAt(a, dir), Log: quietLog(), run: inlineRunner(), timers: noTimers, storeOpts: []store.Option{testKDF}})
	unlock(app)

	loaded := app.main.list.secrets[0]
	app.main.list.list.Select(0)
	formEntries := containedEntries(app.main.detail.Content())
	if len(formEntries) == 0 {
		t.Fatal("expected entries in the secret form")
	}
	// Every entry in the form, looking inside widgets too, must be one that
	// blankEntries can reach (it only walks containers): an entry wrapped in
	// a widget would keep its text after the lock.
	if all := deepEntries(app.main.detail.Content()); len(all) != len(formEntries) {
		t.Fatalf("%d entries in the form, but only %d are in plain containers", len(all), len(formEntries))
	}
	group := findType[*widget.SelectEntry](app.main.detail.Content())
	if group == nil || group.Text == "" {
		t.Fatal("expected the group selector to show the group")
	}

	app.Lock()

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
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(&AppDeps{FyneApp: a, Window: test.NewTempWindow(t, nil), Settings: settingsAt(a, dir), Log: quietLog(), run: inlineRunner(), timers: noTimers, storeOpts: []store.Option{testKDF}})
	pickerOf(app) // its save dialog is never answered
	unlock(app)

	data := []byte("decoded file content")
	app.main.files.exportDecoded("n", "out.txt", data) // save dialog left open
	app.Lock()                                         // dismisses it without an answer

	if !bytes.Equal(data, make([]byte, len(data))) {
		t.Fatal("decoded export content must be wiped when the lock dismisses its dialog")
	}
}
