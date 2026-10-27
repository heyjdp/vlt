package ui

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/yourorg/vlt/internal/store"
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
	s := NewCreateScreen("/tmp/vault", func(string) { called = true })
	test.NewTempWindow(t, s.Content())

	test.Type(s.entry, "correct horse battery")
	test.Type(s.confirm, "correct horse batterx")
	test.Tap(s.createBtn)

	if called {
		t.Fatal("onCreate called despite mismatched confirmation")
	}
	if !s.errorLabel.Visible() || s.errorLabel.Text != errPassphraseMismatch.Error() {
		t.Fatalf("expected mismatch error, got visible=%v text=%q", s.errorLabel.Visible(), s.errorLabel.Text)
	}
	if s.confirm.Text != "" {
		t.Fatal("confirmation entry should be cleared after a mismatch")
	}
}

func TestCreateScreen_ErrorClearsAfterSuccessfulRetry(t *testing.T) {
	test.NewTempApp(t)

	created := false
	s := NewCreateScreen("/tmp/vault", func(string) { created = true })
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
	s := NewCreateScreen("/tmp/vault", func(p string) { got = p })
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

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newVault creates a vault in dir with a cheap work factor.
func newVault(t *testing.T, dir string) {
	t.Helper()
	s, err := store.Create(dir, "correct horse battery", quietLog(), store.WithWorkFactor(10))
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	s.Close()
}

func TestApp_MissingVaultShowsCreateScreen(t *testing.T) {
	a := test.NewTempApp(t)
	dir := filepath.Join(t.TempDir(), "vault")

	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner())
	if app.createScreen == nil || app.unlockScreen != nil {
		t.Fatal("expected the create-vault screen for a missing vault")
	}

	test.Type(app.createScreen.entry, "correct horse battery")
	test.Type(app.createScreen.confirm, "correct horse batterx")
	test.Tap(app.createScreen.createBtn)

	if exists, err := store.Exists(dir); err != nil || exists {
		t.Fatalf("a mismatched confirmation must not create a vault (exists=%v err=%v)", exists, err)
	}
	if app.createScreen == nil {
		t.Fatal("should still be on the create screen")
	}
}

func TestApp_ExistingVaultShowsUnlockScreen(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVault(t, dir)

	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner())
	if app.unlockScreen == nil || app.createScreen != nil {
		t.Fatal("expected the unlock screen for an existing vault")
	}
}

func TestApp_SwitchToEmptyDirectoryShowsCreateScreen(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVault(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner())

	// settings.switchVault sets the directory and relocks.
	app.config.VaultDir = t.TempDir()
	app.Lock()

	if app.createScreen == nil {
		t.Fatal("switching to an empty directory should show the create screen")
	}
}

func TestApp_VaultRemovedAfterScreenChosenOffersCreate(t *testing.T) {
	a := test.NewTempApp(t)
	dir := filepath.Join(t.TempDir(), "vault")
	newVault(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner())

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	test.Type(app.unlockScreen.entry, "correct horse battery")
	test.Tap(app.unlockScreen.unlockBtn)

	if app.createScreen == nil {
		t.Fatal("expected the create screen once the vault has gone")
	}
}

func TestApp_LeftoverMarkerWithoutVaultShowsError(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vlt.json"), []byte(`{"format":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner())
	if app.unlockScreen == nil {
		t.Fatal("a directory with vault files should show the unlock screen")
	}

	test.Type(app.unlockScreen.entry, "correct horse battery")
	test.Tap(app.unlockScreen.unlockBtn)

	if app.unlockScreen == nil || !app.unlockScreen.errorLabel.Visible() {
		t.Fatal("expected an error on the unlock screen, not a silent reset")
	}
}

func TestApp_UnreadableDirectoryShowsNeitherScreen(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission semantics need a non-root Unix user")
	}
	a := test.NewTempApp(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner())
	if app.createScreen != nil || app.unlockScreen != nil {
		t.Fatal("an unreadable directory must show an error, not offer to create or unlock")
	}
}
