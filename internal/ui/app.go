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
	mu        sync.Mutex
	fyneApp   fyne.App
	window    fyne.Window
	config    Config
	log       *slog.Logger
	store     *store.Store
	lockTimer *time.Timer
	cancelCtx context.CancelFunc
	run       runner
	// screenGen identifies the latest showUnlockScreen call; older vault
	// checks that complete late are ignored.
	screenGen int

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
	return newApp(fyneApp, window, config, log, goroutineRunner())
}

// newApp creates the controller with an explicit runner (tests run work
// inline).
func newApp(fyneApp fyne.App, window fyne.Window, config Config, log *slog.Logger, run runner) *App {
	app := &App{
		fyneApp: fyneApp,
		window:  window,
		config:  config,
		log:     log,
		run:     run,
	}

	app.statusBar = widget.NewLabel("Locked")
	app.showUnlockScreen()

	return app
}

// showUnlockScreen displays the passphrase entry screen, or the create-vault
// screen (passphrase entered twice) if no vault exists at the configured
// location. Inspecting the directory runs in the background.
func (a *App) showUnlockScreen() {
	a.unlockScreen, a.createScreen = nil, nil
	a.setScreen(container.NewCenter(widget.NewLabel("Checking vault…")))

	a.screenGen++
	gen := a.screenGen
	dir := a.config.VaultDir
	background(a.run,
		func(context.Context) (bool, error) { return store.Exists(dir) },
		func(exists bool, err error) {
			if gen != a.screenGen {
				return // superseded by a later check (vault switch, relock)
			}
			switch {
			case err != nil:
				// Never offer to create a vault over a directory we cannot inspect.
				a.setScreen(a.vaultErrorContent(err))
			case exists:
				a.unlockScreen = NewUnlockScreen(a.window, a.onUnlock)
				a.setScreen(a.unlockScreen.Content())
			default:
				a.createScreen = NewCreateScreen(dir, a.onCreate)
				a.setScreen(a.createScreen.Content())
			}
		})
}

// setScreen shows content above the status bar.
func (a *App) setScreen(content fyne.CanvasObject) {
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

// openResult is the outcome of a background unlock attempt.
type openResult struct {
	store *store.Store
	// stillExists reports, after ErrVaultNotFound, whether leftover vault
	// files remain (so the error is shown rather than offering to create).
	stillExists bool
}

// onUnlock opens the existing vault in the background (one scrypt, or a
// migration) while the unlock screen shows progress.
func (a *App) onUnlock(passphrase string) {
	screen := a.unlockScreen
	screen.SetBusy(true)
	dir := a.config.VaultDir
	background(a.run,
		func(ctx context.Context) (openResult, error) {
			s, err := store.Open(ctx, dir, passphrase, a.log)
			if errors.Is(err, store.ErrVaultNotFound) {
				exists, _ := store.Exists(dir)
				return openResult{stillExists: exists}, err
			}
			return openResult{store: s}, err
		},
		func(r openResult, err error) {
			if a.unlockScreen != screen {
				// The screen was replaced (e.g. vault switched) while scrypt ran.
				if r.store != nil {
					r.store.Close()
				}
				return
			}
			screen.SetBusy(false)
			switch {
			case errors.Is(err, store.ErrWrongPassphrase):
				screen.ShowError("Incorrect passphrase")
			case errors.Is(err, store.ErrVaultNotFound) && r.stillExists:
				// Leftovers (e.g. a stale vlt.json) but nothing to unlock.
				screen.ShowError("No vault key or secrets found in " + dir)
			case errors.Is(err, store.ErrVaultNotFound):
				// The vault disappeared since the screen was chosen; offer to create one.
				a.showUnlockScreen()
			case err != nil:
				screen.ShowError("Failed to open vault: " + err.Error())
			default:
				a.unlocked(r.store)
			}
		})
}

// onCreate creates a new vault with a confirmed passphrase in the background.
func (a *App) onCreate(passphrase string) {
	screen := a.createScreen
	screen.SetBusy(true)
	dir := a.config.VaultDir
	background(a.run,
		func(context.Context) (*store.Store, error) { return store.Create(dir, passphrase, a.log) },
		func(s *store.Store, err error) {
			if a.createScreen != screen {
				if s != nil {
					s.Close()
				}
				return
			}
			screen.SetBusy(false)
			if err != nil {
				screen.ShowError("Failed to create vault: " + err.Error())
				return
			}
			a.unlocked(s)
		})
}

// unlocked switches to the main view for an opened or newly created vault.
// It runs on the main goroutine.
func (a *App) unlocked(s *store.Store) {
	// The session context is registered before the auto-lock timer starts,
	// so a Lock firing at any point after this cancels the whole session.
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.store = s
	a.cancelCtx = cancel
	a.startLockTimer()
	a.mu.Unlock()
	a.unlockScreen, a.createScreen = nil, nil
	a.log.Info("vault unlocked", "dir", a.config.VaultDir)

	a.showMainView(s, a.run.withContext(ctx))

	if skipped := s.SkippedFiles(); len(skipped) > 0 {
		dialog.ShowInformation("Some secrets were not migrated",
			fmt.Sprintf("%d file(s) could not be decrypted with this passphrase and were left untouched:\n%s",
				len(skipped), strings.Join(skipped, "\n")),
			a.window)
	}
}

// showMainView displays the main secret browser for the unlocked store s.
// It takes s explicitly rather than reading a.store, which the auto-lock
// timer may clear concurrently. session is bound to the session context:
// work still in flight when the vault locks has its result dropped.
func (a *App) showMainView(s *store.Store, session runner) {
	a.detailView = NewDetailView(a.window, s, a.config.ClipboardTTLs, a.statusBar, a.log, session)
	a.secretList = NewSecretList(a.window, s, a.detailView, a.log, session)

	// Every save, delete and import updates the list in place, whether the
	// secret is new or was selected from the list.
	onSaved := a.secretList.Upsert
	a.detailView.SetCallbacks(onSaved, a.secretList.Remove, a.secretList.Groups)
	// An import may replace the secret whose form is open; re-render it.
	onImported := func(sec *store.Secret) {
		a.secretList.Upsert(sec)
		a.detailView.RefreshIfShowing(sec)
	}
	fileOps := NewFileOps(a.window, s, a.log, session, a.secretList.Groups, onImported)
	a.detailView.SetFileOps(fileOps)

	// Toolbar with actions
	toolbar := container.NewHBox(
		widget.NewButton("Add Password", func() {
			a.resetLockTimer()
			a.detailView.ShowNewPassword()
		}),
		widget.NewButton("Add Record", func() {
			a.resetLockTimer()
			a.detailView.ShowNewRecord()
		}),
		widget.NewButton("Import File", func() {
			a.resetLockTimer()
			fileOps.ImportFile()
		}),
		widget.NewButton("Settings", func() {
			a.resetLockTimer()
			settings := NewSettingsScreen(a.window, &a.config, s, a.statusBar, a.log, session, func() {
				a.Lock()
			})
			a.detailView.ShowContent(settings.Content())
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

	// Load secrets in the background; Lock cancels the session context.
	a.secretList.Refresh()
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

// startLockTimer starts the auto-lock inactivity timer. The caller holds a.mu.
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
