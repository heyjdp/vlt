package ui

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

// productionDeps are AppDeps as main sets them, over a location that does
// not parse, so the first screen is the vault error and NewApp starts no
// background work (and, with update checks off, no network request).
func productionDeps(t *testing.T) *AppDeps {
	t.Helper()
	a := test.NewTempApp(t)
	return &AppDeps{
		FyneApp:  a,
		Window:   test.NewTempWindow(t, nil),
		Settings: settingsAt(a, "nas:"),
		Logger:   quietLog(),
		Version:  "1.2.3",
	}
}

func TestNewApp_FillsInTheProductionSeams(t *testing.T) {
	deps := productionDeps(t)

	app, err := NewApp(deps)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}

	if app.open == nil {
		t.Error("NewApp left no browser (FyneApp.OpenURL is the default)")
	}
	if app.pick == nil {
		t.Error("NewApp left no file and folder chooser (Fyne's dialogs are the default)")
	}
	if app.run.spawn == nil {
		t.Error("NewApp left no runner")
	}
	if app.newTimer == nil {
		t.Error("NewApp left no timer factory")
	}
	if app.version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", app.version)
	}
	if app.log != deps.Logger {
		t.Error("NewApp did not use the logger it was given")
	}
	if findButton(deps.Window.Content(), "Retry") == nil {
		t.Error("expected the vault error screen for a location that does not parse")
	}
}

func TestNewApp_NilLoggerUsesTheDefault(t *testing.T) {
	deps := productionDeps(t)
	deps.Logger = nil

	app, err := NewApp(deps)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}

	if app.log != slog.Default() {
		t.Error("a nil Logger should be replaced by slog.Default()")
	}
}

func TestNewApp_RefusesMissingRequiredDeps(t *testing.T) {
	tests := []struct {
		name  string
		unset func(*AppDeps) *AppDeps
		want  string
	}{
		{"no deps", func(*AppDeps) *AppDeps { return nil }, "no AppDeps"},
		{"no Fyne app", func(d *AppDeps) *AppDeps { d.FyneApp = nil; return d }, "FyneApp"},
		{"no window", func(d *AppDeps) *AppDeps { d.Window = nil; return d }, "Window"},
		{"no settings", func(d *AppDeps) *AppDeps { d.Settings = nil; return d }, "Settings"},
		{
			"all three",
			func(d *AppDeps) *AppDeps { d.FyneApp, d.Window, d.Settings = nil, nil, nil; return d },
			"FyneApp, Window, Settings",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, err := NewApp(tt.unset(productionDeps(t)))

			if !errors.Is(err, ErrMissingAppDep) {
				t.Fatalf("NewApp error = %v, want ErrMissingAppDep", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("NewApp error = %q, want it to name %q", err, tt.want)
			}
			if app != nil {
				t.Error("NewApp returned an app with its error")
			}
		})
	}
}

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
