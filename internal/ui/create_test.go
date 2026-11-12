package ui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/heyjdp/vlt/internal/store"
)

func TestValidateNewPassphrase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		passphrase string
		confirm    string
		want       error
	}{
		{"matching and long enough", "correct horse battery", "correct horse battery", nil},
		{"mismatch", "correct horse battery", "correct horse batterx", errPassphraseMismatch},
		{"too short", "short", "short", errPassphraseTooShort},
		{"empty", "", "", errPassphraseTooShort},
		{"length counts runes not bytes", strings.Repeat("é", 11), strings.Repeat("é", 11), errPassphraseTooShort},
		{"exactly the minimum", strings.Repeat("a", minPassphraseLen), strings.Repeat("a", minPassphraseLen), nil},
		{"only spaces", strings.Repeat(" ", 20), strings.Repeat(" ", 20), errPassphraseTooShort},
		{"padding does not count", "  short pass  ", "  short pass  ", errPassphraseTooShort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := validateNewPassphrase(tt.passphrase, tt.confirm); !errors.Is(got, tt.want) {
				t.Fatalf("validateNewPassphrase = %v, want %v", got, tt.want)
			}
		})
	}
}

// Fyne's test app is process-global, so the widget tests below do not run in
// parallel with each other.

func TestCreateScreen_MismatchShowsErrorAndDoesNotCreate(t *testing.T) {
	test.NewTempApp(t)

	called := false
	s := NewCreateScreen("/tmp/vault", func(string) { called = true }, nil, nil)
	test.NewTempWindow(t, s.Content())

	test.Type(s.entry, "correct horse battery")
	test.Type(s.confirm, "correct horse batterx")
	test.Tap(s.createBtn)

	if called {
		t.Fatal("onCreate called despite mismatched confirmation")
	}
	if !s.errorLabel.Visible() || s.errorLabel.Text != "Passphrases do not match" {
		t.Fatalf("expected mismatch error, got visible=%v text=%q", s.errorLabel.Visible(), s.errorLabel.Text)
	}
	if s.confirm.Text != "" {
		t.Fatal("confirmation entry should be cleared after a mismatch")
	}
}

func TestCreateScreen_ErrorClearsAfterSuccessfulRetry(t *testing.T) {
	test.NewTempApp(t)

	created := false
	s := NewCreateScreen("/tmp/vault", func(string) { created = true }, nil, nil)
	test.NewTempWindow(t, s.Content())

	test.Type(s.entry, "correct horse battery")
	test.Type(s.confirm, "wrong")
	test.Tap(s.createBtn)
	test.Type(s.confirm, "correct horse battery")
	test.Tap(s.createBtn)

	if !created {
		t.Fatal("retry with matching confirmation should create")
	}
	if s.errorLabel.Visible() {
		t.Fatal("error label should be hidden after a successful retry")
	}
}

func TestCreateScreen_MatchingPassphraseCreatesAndClearsEntries(t *testing.T) {
	test.NewTempApp(t)

	var got string
	s := NewCreateScreen("/tmp/vault", func(p string) { got = p }, nil, nil)
	test.NewTempWindow(t, s.Content())

	test.Type(s.entry, "correct horse battery")
	test.Type(s.confirm, "correct horse battery")
	test.Tap(s.createBtn)

	if got != "correct horse battery" {
		t.Fatalf("onCreate got %q", got)
	}
	if s.entry.Text != "" || s.confirm.Text != "" {
		t.Fatal("passphrase entries should be cleared after submit")
	}
}

func TestApp_MissingVaultShowsCreateScreen(t *testing.T) {
	r := newAppRig(t, withoutVault())
	if r.shownScreen() != screenCreate {
		t.Fatal("expected the create-vault screen for a missing vault")
	}

	r.createConfirming("correct horse batterx")

	if exists, err := store.Exists(t.Context(), r.dir); err != nil || exists {
		t.Fatalf("a mismatched confirmation must not create a vault (exists=%v err=%v)", exists, err)
	}
	if r.shownScreen() != screenCreate {
		t.Fatal("should still be on the create screen")
	}
}

func TestApp_ExistingVaultShowsUnlockScreen(t *testing.T) {
	r := newAppRig(t)
	if r.shownScreen() != screenUnlock {
		t.Fatal("expected the unlock screen for an existing vault")
	}
}

func TestApp_SwitchToEmptyDirectoryShowsCreateScreen(t *testing.T) {
	r := newAppRig(t)

	// settings.switchVault sets the directory and relocks.
	r.settings.ProposeVault(localAt(t.TempDir()))
	r.lock()

	if r.shownScreen() != screenCreate {
		t.Fatal("switching to an empty directory should show the create screen")
	}
}

func TestApp_VaultRemovedAfterScreenChosenOffersCreate(t *testing.T) {
	r := newAppRig(t)

	if err := os.RemoveAll(r.dir); err != nil {
		t.Fatal(err)
	}
	r.unlock()

	if r.shownScreen() != screenCreate {
		t.Fatal("expected the create screen once the vault has gone")
	}
}

func TestApp_LeftoverMarkerWithoutVaultShowsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vlt.json"), []byte(`{"format":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newAppRig(t, atLocation(dir))
	if r.shownScreen() != screenUnlock {
		t.Fatal("a directory with vault files should show the unlock screen")
	}

	r.unlock()

	if r.shownScreen() != screenUnlock || r.screenError() == "" {
		t.Fatal("expected an error on the unlock screen, not a silent reset")
	}
}

func TestApp_UnreadableDirectoryShowsNeitherScreen(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission semantics need a non-root Unix user")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	r := newAppRig(t, atLocation(dir))
	if shown := r.shownScreen(); shown == screenCreate || shown == screenUnlock {
		t.Fatal("an unreadable directory must show an error, not offer to create or unlock")
	}
}

func TestApp_OpenExistingVaultUnlocksAndRemembersIt(t *testing.T) {
	r := newAppRig(t, withoutVault())
	other := t.TempDir()
	newVaultWith(t, other)

	r.app.openExistingVault(other)

	if r.shownScreen() != screenUnlock || r.settings.Vault() != localAt(other) {
		t.Fatal("opening a folder with a vault should show its unlock screen")
	}
	if r.prefs().String(prefVaultDir) != "" {
		t.Fatal("the location must not be saved before the vault is unlocked")
	}
	r.unlock()
	if r.locked() {
		t.Fatal("the opened vault should unlock with its passphrase")
	}
	if got := r.prefs().String(prefVaultDir); got != other {
		t.Fatalf("saved location = %q, want %q", got, other)
	}
	r.lock()
}

func TestApp_OpenFolderWithoutVaultIsRefused(t *testing.T) {
	r := newAppRig(t, withoutVault())
	screen, dir := r.createScreen(), r.settings.Vault()
	empty := t.TempDir()

	r.app.openExistingVault(empty)

	if r.createScreen() != screen || r.settings.Vault() != dir {
		t.Fatal("a folder without a vault must leave the create screen and location unchanged")
	}
	if text := r.screenError(); !strings.Contains(text, "No vault was found") {
		t.Fatalf("expected a no-vault error, got %q", text)
	}
	if exists, err := store.Exists(t.Context(), empty); err != nil || exists {
		t.Fatalf("opening must never create a vault (exists=%v err=%v)", exists, err)
	}
}

func TestApp_OpenExistingIsNotOfferedWhenTheLocationIsFromTheEnvironment(t *testing.T) {
	if newAppRig(t, withoutVault()).createScreen().openBtn == nil {
		t.Fatal("the create screen should offer to open an existing vault")
	}
	fromEnv := newAppRig(t, withoutVault(), withSettings(vaultFromEnv))
	if fromEnv.createScreen().openBtn != nil {
		t.Fatal("VLT_VAULT_DIR fixes the location, so opening another folder must not be offered")
	}
}

func TestCreateScreen_BusyDisablesOpen(t *testing.T) {
	test.NewTempApp(t)
	s := NewCreateScreen("/tmp/vault", func(string) {}, func() {}, func() {})

	s.SetBusy(true)
	if !s.openBtn.Disabled() {
		t.Fatal("Open Existing Vault must be disabled while a vault is created")
	}
	s.SetBusy(false)
	if s.openBtn.Disabled() {
		t.Fatal("Open Existing Vault should be enabled again")
	}
}

func TestApp_OpenFolderAboveAVaultPointsToIt(t *testing.T) {
	r := newAppRig(t, withoutVault())
	parent := t.TempDir()
	inner := filepath.Join(parent, "vault")
	newVaultWith(t, inner)

	r.app.openExistingVault(parent)

	if r.shownScreen() == screenUnlock || r.settings.Vault() == localAt(parent) {
		t.Fatal("the folder above a vault must not be opened as one")
	}
	if text := r.screenError(); !strings.Contains(text, inner) {
		t.Fatalf("the error should name the folder that holds the vault, got %q", text)
	}
}

func TestApp_OpenUnreadableFolderShowsTheError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not restrict this user")
	}
	r := newAppRig(t, withoutVault())
	locked := t.TempDir()
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	r.app.openExistingVault(locked)

	if text := r.screenError(); !strings.Contains(text, "Couldn't look for a vault in "+locked+": can't look at "+filepath.Join(locked, "vlt.age")+": permission denied") {
		t.Fatalf("expected a read error, got %q", text)
	}
	if !r.usable() {
		t.Fatal("the screen should be usable again after the error")
	}
}

func TestApp_OpenCheckDisablesTheScreenUntilItFinishes(t *testing.T) {
	r := newAppRig(t, withQueuedRunner(), withoutVault())
	other := t.TempDir()
	newVaultWith(t, other)

	r.app.openExistingVault(other) // the folder check waits in the queue
	if !r.busy() {
		t.Fatal("Create and Open must be disabled while the folder is checked")
	}
	r.flush()
	if r.shownScreen() != screenUnlock {
		t.Fatal("expected the unlock screen once the check finds the vault")
	}
}

func TestApp_StaleOpenResultIsDropped(t *testing.T) {
	r := newAppRig(t, withQueuedRunner(), withoutVault())
	other := t.TempDir()
	newVaultWith(t, other)

	r.app.openExistingVault(other) // the folder check waits in the queue
	r.app.showUnlockScreen()       // meanwhile the screen is rebuilt
	r.flush()

	if r.settings.Vault() != localAt(r.dir) || r.shownScreen() == screenUnlock {
		t.Fatalf("a result for a replaced screen must not switch vaults: dir=%q", r.settings.Vault())
	}
	if r.shownScreen() != screenCreate || !r.usable() {
		t.Fatal("the new create screen should be shown and usable")
	}
}

func TestCreateScreen_OpenButtonShowsAFolderDialog(t *testing.T) {
	r := newAppRig(t, withoutVault(), withFyneDialogs())
	if r.window.Canvas().Overlays().Top() != nil {
		t.Fatal("no dialog expected yet")
	}
	test.Tap(r.createScreen().openBtn)
	if r.window.Canvas().Overlays().Top() == nil {
		t.Fatal("Open Existing Vault should show a folder dialog")
	}
}
