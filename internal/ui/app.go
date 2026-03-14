// Package ui implements the Fyne-based GUI for the vlt secret manager.
package ui

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/crypto"
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
	identity    *crypto.Identity
	store       *store.Store
	lockTimer   *time.Timer
	cancelCtx   context.CancelFunc

	// UI components
	unlockScreen *UnlockScreen
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

// showUnlockScreen displays the passphrase entry screen.
func (a *App) showUnlockScreen() {
	a.unlockScreen = NewUnlockScreen(a.window, a.onUnlock)
	a.window.SetContent(container.NewBorder(
		nil,
		a.statusBar,
		nil,
		nil,
		a.unlockScreen.Content(),
	))
}

// onUnlock is called when the user enters a passphrase.
func (a *App) onUnlock(passphrase string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	id, err := crypto.NewIdentity(passphrase)
	if err != nil {
		a.unlockScreen.ShowError("Failed to create identity: " + err.Error())
		return
	}

	s := store.New(id, a.config.VaultDir, a.log)
	if err := s.Init(); err != nil {
		id.Zero()
		a.unlockScreen.ShowError("Failed to initialize vault: " + err.Error())
		return
	}

	a.identity = id
	a.store = s
	a.log.Info("vault unlocked", "dir", a.config.VaultDir)

	a.showMainView()
	a.startLockTimer()
}

// showMainView displays the main secret browser.
func (a *App) showMainView() {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelCtx = cancel

	a.detailView = NewDetailView(a.window, a.store, a.config.ClipboardTTLs, a.statusBar, a.log)
	a.secretList = NewSecretList(a.window, a.store, a.detailView, a.log)

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

	if a.identity != nil {
		a.identity.Zero()
		a.identity = nil
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
