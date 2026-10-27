// Package ui implements the Fyne-based GUI for the vlt secret manager.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"log/slog"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
)

// Config holds UI-related configuration.
type Config struct {
	VaultDir      string
	LockTimeoutMs int
	ClipboardTTLs int
}

// App is the main application controller managing lock/unlock state.
type App struct {
	mu          sync.Mutex
	fyneApp     fyne.App
	window      fyne.Window
	config      Config
	log         *slog.Logger
	store       *store.Store
	lockTimer   *time.Timer
	cancelCtx   context.CancelFunc

	// UI components
	unlockScreen *UnlockScreen
	createScreen *CreateScreen
	mainContent  *fyne.Container
	secretList   *SecretList
	detailView   *DetailView
	statusBar    *widget.Label
}

// NewApp creates the main application controller.
func NewApp(fyneApp fyne.App, window fyne.Window, config Config, log *slog.Logger) *App {
	app := &App{
		fyneApp: fyneApp,
		window:  window,
		config:  config,
		log:     log,
	}

	app.statusBar = widget.NewLabel("Locked")
	app.showUnlockScreen()

	return app
}

// showUnlockScreen displays the passphrase entry screen, or the create-vault
// screen (passphrase entered twice) if no vault exists at the configured
// location.
func (a *App) showUnlockScreen() {
	a.unlockScreen, a.createScreen = nil, nil

	var content fyne.CanvasObject
	exists, err := store.Exists(a.config.VaultDir)
	switch {
	case err != nil:
		// Never offer to create a vault over a directory we cannot inspect.
		content = a.vaultErrorContent(err)
	case exists:
		a.unlockScreen = NewUnlockScreen(a.window, a.onUnlock)
		content = a.unlockScreen.Content()
	default:
		a.createScreen = NewCreateScreen(a.config.VaultDir, a.onCreate)
		content = a.createScreen.Content()
	}
	a.window.SetContent(container.NewBorder(nil, a.statusBar, nil, nil, content))
}

// vaultErrorContent explains why the vault directory cannot be used and
// offers a retry (for example after fixing permissions).
func (a *App) vaultErrorContent(err error) fyne.CanvasObject {
	msg := widget.NewLabel(fmt.Sprintf("Cannot read the vault directory:\n%s\n\n%v", a.config.VaultDir, err))
	msg.Wrapping = fyne.TextWrapWord
	retry := widget.NewButton("Retry", a.showUnlockScreen)
	width := canvas.NewRectangle(color.Transparent)
	width.SetMinSize(fyne.NewSize(createFormWidth, 0))
	return container.NewCenter(container.NewStack(width, container.NewVBox(msg, retry)))
}

// onUnlock is called when the user enters a passphrase for an existing vault.
func (a *App) onUnlock(passphrase string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	s, err := store.Open(context.Background(), a.config.VaultDir, passphrase, a.log)
	switch {
	case errors.Is(err, store.ErrWrongPassphrase):
		a.unlockScreen.ShowError("Incorrect passphrase")
		return
	case errors.Is(err, store.ErrVaultNotFound):
		if exists, _ := store.Exists(a.config.VaultDir); exists {
			// Leftovers (e.g. a stale vlt.json) but nothing to unlock.
			a.unlockScreen.ShowError("No vault key or secrets found in " + a.config.VaultDir)
			return
		}
		// The vault disappeared since the screen was chosen; offer to create one.
		a.showUnlockScreen()
		return
	case err != nil:
		a.unlockScreen.ShowError("Failed to open vault: " + err.Error())
		return
	}
	a.unlocked(s)
}

// onCreate is called with a confirmed passphrase for a new vault.
func (a *App) onCreate(passphrase string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	s, err := store.Create(a.config.VaultDir, passphrase, a.log)
	if err != nil {
		a.createScreen.ShowError("Failed to create vault: " + err.Error())
		return
	}
	a.unlocked(s)
}

// unlocked switches to the main view for an opened or newly created vault.
// The caller holds a.mu.
func (a *App) unlocked(s *store.Store) {
	a.store = s
	a.unlockScreen, a.createScreen = nil, nil
	a.log.Info("vault unlocked", "dir", a.config.VaultDir)

	a.showMainView()
	a.startLockTimer()

	if skipped := s.SkippedFiles(); len(skipped) > 0 {
		dialog.ShowInformation("Some secrets were not migrated",
			fmt.Sprintf("%d file(s) could not be decrypted with this passphrase and were left untouched:\n%s",
				len(skipped), strings.Join(skipped, "\n")),
			a.window)
	}
}

// showMainView displays the main secret browser.
func (a *App) showMainView() {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelCtx = cancel

	a.detailView = NewDetailView(a.window, a.store, a.config.ClipboardTTLs, a.statusBar, a.log)
	a.secretList = NewSecretList(a.window, a.store, a.detailView, a.log)

	// File operations handler
	fileOps := NewFileOps(a.window, a.store, a.log)
	fileOps.SetOnSave(func() {
		a.secretList.Refresh(ctx)
	})
	a.detailView.SetFileOps(fileOps)

	// Toolbar with actions
	toolbar := container.NewHBox(
		widget.NewButton("Add Password", func() {
			a.resetLockTimer()
			a.detailView.ShowNewPassword()
			a.detailView.SetOnSave(func() {
				a.secretList.Refresh(ctx)
			})
		}),
		widget.NewButton("Add Record", func() {
			a.resetLockTimer()
			a.detailView.ShowNewRecord()
			a.detailView.SetOnSave(func() {
				a.secretList.Refresh(ctx)
			})
		}),
		widget.NewButton("Import File", func() {
			a.resetLockTimer()
			fileOps.ImportFile()
		}),
		widget.NewButton("Settings", func() {
			a.resetLockTimer()
			settings := NewSettingsScreen(a.window, &a.config, a.store, a.statusBar, a.log, func() {
				a.Lock()
			})
			a.detailView.Content().Objects = []fyne.CanvasObject{settings.Content()}
			a.detailView.Content().Refresh()
		}),
		widget.NewButton("Lock", func() {
			a.Lock()
		}),
	)

	// Split: list on left, detail on right
	split := container.NewHSplit(
		a.secretList.Content(),
		a.detailView.Content(),
	)
	split.SetOffset(0.3)

	a.mainContent = container.NewBorder(
		toolbar,
		a.statusBar,
		nil,
		nil,
		split,
	)

	a.window.SetContent(a.mainContent)
	a.statusBar.SetText("Unlocked")

	// Load secrets in background
	go func() {
		a.secretList.Refresh(ctx)
	}()

	_ = ctx
}

// Lock zeros the identity and returns to the unlock screen.
func (a *App) Lock() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.store != nil {
		a.store.Close()
	}
	if a.cancelCtx != nil {
		a.cancelCtx()
		a.cancelCtx = nil
	}
	if a.lockTimer != nil {
		a.lockTimer.Stop()
		a.lockTimer = nil
	}

	a.store = nil
	a.log.Info("vault locked")

	fyne.Do(func() {
		a.statusBar.SetText("Locked")
		a.showUnlockScreen()
	})
}

// startLockTimer starts the auto-lock inactivity timer.
func (a *App) startLockTimer() {
	if a.config.LockTimeoutMs <= 0 {
		return
	}

	d := time.Duration(a.config.LockTimeoutMs) * time.Millisecond
	a.lockTimer = time.AfterFunc(d, func() {
		a.log.Info("auto-lock triggered")
		a.Lock()
	})
}

// resetLockTimer resets the inactivity timer.
func (a *App) resetLockTimer() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.lockTimer != nil && a.config.LockTimeoutMs > 0 {
		d := time.Duration(a.config.LockTimeoutMs) * time.Millisecond
		a.lockTimer.Reset(d)
	}
}

// ShowError displays an error dialog.
func (a *App) ShowError(err error) {
	dialog.ShowError(err, a.window)
}
