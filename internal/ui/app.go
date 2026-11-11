// Package ui implements the Fyne-based GUI for the vlt secret manager.
package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/update"
)

// App is the main application controller managing lock/unlock state.
type App struct {
	fyneApp fyne.App
	window  fyne.Window
	// version is the build's version (main.version), shown in About.
	version string
	// open opens a link in the browser (fyneApp.OpenURL; tests replace it).
	open func(*url.URL) error
	// latest fetches the newest release; NewApp sets the real checker, and
	// other constructors leave one that never reaches the network.
	latest latestRelease
	// checkingUpdates is true while a check the user asked for runs;
	// checkedManually records that one has run since start-up, so the
	// start-up check announces nothing more; pendingRelease is a release the
	// start-up check found, held until the vault is unlocked. All three are
	// used on the main goroutine only.
	checkingUpdates bool
	checkedManually bool
	pendingRelease  *update.Release
	config          Config
	log             *slog.Logger
	// session holds the open (or waiting) store and runs the auto-lock.
	session  *session[*store.Store]
	newTimer timerFactory
	// storeOpts are passed to store.Open and store.Create. Tests use them
	// to lower the scrypt work factor; production passes none.
	storeOpts []store.Option
	run       runner
	// screenGen identifies the latest showUnlockScreen call; older vault
	// checks that complete late are ignored.
	screenGen int

	// UI components
	lockWarning  *dialog.ConfirmDialog
	tracker      *activityTracker
	clipboard    *clipboardClearer
	unlockScreen *UnlockScreen
	createScreen *CreateScreen
	mainContent  *fyne.Container
	secretList   *SecretList
	detailView   *DetailView
	statusBar    *widget.Label
}

// NewApp creates the main application controller. version is the build's
// version, shown in the About dialog.
func NewApp(fyneApp fyne.App, window fyne.Window, config Config, log *slog.Logger, version string) *App {
	app := newVersionedApp(version, fyneApp, window, config, log, goroutineRunner(), realTimers)
	app.startUpdateChecks(update.NewChecker().Latest)
	return app
}

// newApp creates the controller with an explicit runner, timer factory and
// store options (tests run work inline, fire timers by hand, and use a cheap
// scrypt work factor).
func newApp(fyneApp fyne.App, window fyne.Window, config Config, log *slog.Logger, run runner, timers timerFactory, storeOpts ...store.Option) *App {
	return newVersionedApp("", fyneApp, window, config, log, run, timers, storeOpts...)
}

// newVersionedApp is newApp with the build's version, set before the first
// screen starts any background work.
func newVersionedApp(version string, fyneApp fyne.App, window fyne.Window, config Config, log *slog.Logger, run runner, timers timerFactory, storeOpts ...store.Option) *App {
	app := &App{
		version:   version,
		open:      fyneApp.OpenURL,
		latest:    noUpdateCheck,
		fyneApp:   fyneApp,
		window:    window,
		config:    config,
		log:       log,
		run:       run,
		newTimer:  timers,
		storeOpts: storeOpts,
	}

	// Quitting is a lock-equivalent for the clipboard: the system clipboard
	// outlives the process, so a copied secret is cleared first. Closing the
	// window goes through the close intercept; the Quit menu item (Cmd+Q on
	// macOS) bypasses it, so it gets its own action.
	window.SetCloseIntercept(app.quit)
	quitItem := fyne.NewMenuItem("Quit", app.quit)
	quitItem.IsQuit = true
	// An item labelled exactly "About" becomes the app menu's "About vlt"
	// on macOS (Fyne replaces the system About panel with it); elsewhere it
	// stays in the Help menu.
	window.SetMainMenu(fyne.NewMainMenu(
		fyne.NewMenu("File", quitItem),
		fyne.NewMenu("Help",
			fyne.NewMenuItem("About", app.showAbout),
			fyne.NewMenuItem("Check for Updates…", app.checkForUpdates),
			fyne.NewMenuItem("Report a Bug…", func() { app.openURL(issueURL(app.aboutDetails().String())) }),
		),
	))

	app.session = newSession[*store.Store](timers, fyne.Do, app.lockTimeout, sessionCallbacks{
		Warn: app.showLockWarning,
		// The session has already closed the store (zeroing its key);
		// Lock returns to the unlock screen.
		TimedOut: func() {
			app.log.Info("auto-lock triggered")
			app.Lock()
		},
	})
	app.statusBar = caption("Locked")
	app.clipboard = newClipboardClearer(fyneApp.Clipboard(), app.statusBar,
		func() time.Duration { return time.Duration(app.config.ClipboardTTLs) * time.Second }, timers)
	app.showUnlockScreen()

	return app
}

// showUnlockScreen displays the passphrase entry screen, or the create-vault
// screen (passphrase entered twice) if no vault exists at the configured
// location. Inspecting the directory runs in the background.
func (a *App) showUnlockScreen() {
	a.unlockScreen, a.createScreen = nil, nil
	a.setScreen(container.NewCenter(muted("Checking vault…")))

	a.screenGen++
	gen := a.screenGen
	dir := a.config.VaultDir
	background(a.run,
		func(ctx context.Context) (bool, error) { return store.Exists(ctx, dir, a.storeOpts...) },
		func(exists bool, err error) {
			if gen != a.screenGen {
				return // superseded by a later check (vault switch, relock)
			}
			switch {
			case err != nil:
				// Never offer to create a vault over a directory we cannot inspect.
				a.setScreen(a.vaultErrorContent(err))
			case exists:
				a.unlockScreen = NewUnlockScreen(a.window, dir, a.onUnlock)
				a.setScreen(a.unlockScreen.Content())
			default:
				var onOpen, onServer func()
				if !a.config.FromEnv.VaultDir { // VLT_VAULT_DIR fixes the location
					onOpen, onServer = a.chooseExistingVault, a.chooseServerVault
				}
				a.createScreen = NewCreateScreen(dir, a.onCreate, onOpen, onServer)
				a.setScreen(a.createScreen.Content())
			}
		})
}

// setScreen shows content above the status bar.
func (a *App) setScreen(content fyne.CanvasObject) {
	a.window.SetContent(container.NewBorder(nil, a.statusRow(), nil, nil, content))
}

// statusRow is the status bar along the bottom of the window, with the
// About button at its right, so About is reachable from every screen.
func (a *App) statusRow() fyne.CanvasObject {
	about := widget.NewButtonWithIcon("About", theme.InfoIcon(), a.showAbout)
	about.Importance = widget.LowImportance
	return container.NewVBox(widget.NewSeparator(),
		container.NewBorder(nil, nil, nil, about,
			container.New(layout.NewCustomPaddedLayout(0, 0, 6, 6), a.statusBar)))
}

// vaultErrorContent explains why the vault cannot be used and offers a
// retry (for example after fixing permissions, or once the server is back),
// and, for a vault elsewhere, a way back to the default local vault.
func (a *App) vaultErrorContent(err error) fyne.CanvasObject {
	heading := "Cannot read the vault directory"
	if isServerError(err) {
		heading = "Cannot reach the vault's server"
	}
	title := subheading(heading)
	title.Alignment = fyne.TextAlignCenter
	msg := muted(fmt.Sprintf("%s\n\n%s", a.config.VaultDir, errorMessage("", err)))
	retry := widget.NewButtonWithIcon("Retry", theme.ViewRefreshIcon(), a.showUnlockScreen)
	retry.Importance = widget.HighImportance
	icon := container.NewCenter(fixedHeight(fixedWidth(widget.NewIcon(theme.ErrorIcon()), 40), 40))
	objects := []fyne.CanvasObject{icon, title, msg, retry}
	if def := a.config.DefaultVaultDir; def != "" && def != a.config.VaultDir && !a.config.FromEnv.VaultDir {
		local := widget.NewButtonWithIcon("Use the Vault on This Computer", theme.ComputerIcon(), func() {
			a.log.Info("switching to the default vault location", "dir", def)
			a.config.VaultDir = def
			a.showUnlockScreen()
		})
		local.Importance = widget.LowImportance
		objects = append(objects, local)
	}
	return centeredCard(createFormWidth, objects...)
}

// openResult is the outcome of a background unlock or create.
type openResult struct {
	store *store.Store
	// inUse is set when the vault opened but another copy of vlt holds its
	// lock; the user is asked before going on.
	inUse *store.InUseError
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
			s, err := store.Open(ctx, dir, passphrase, a.log, a.storeOpts...)
			if errors.Is(err, store.ErrVaultNotFound) {
				exists, _ := store.Exists(ctx, dir, a.storeOpts...)
				return openResult{stillExists: exists}, err
			}
			if err != nil {
				return openResult{}, err
			}
			return a.lockVault(ctx, s), nil
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
			case errors.Is(err, store.ErrKeyMissing):
				screen.ShowError("This vault's key file (vlt.age) is missing, so its secrets cannot be decrypted. Restore vlt.age from a backup.")
			case isServerError(err):
				screen.ShowError(errorMessage("", err))
			case err != nil:
				screen.ShowError(errorMessage("Couldn't open the vault", err))
			default:
				a.unlockedOrAsk(r, screen.SetBusy, func() bool { return a.unlockScreen == screen })
			}
		})
}

// chooseServerVault asks for a vault location on an SSH server and switches
// to it: its unlock screen if a vault is there, otherwise the create screen
// for that location. Nothing is created until a passphrase is confirmed.
func (a *App) chooseServerVault() {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("me@server.example:/home/me/vault")
	entry.Validator = func(s string) error {
		loc, err := store.ParseLocation(s)
		if err != nil {
			return errors.New(errorMessage("", err))
		}
		if !loc.IsRemote() {
			return errors.New("enter host:/path or ssh://host/path")
		}
		return nil
	}
	help := muted("vlt connects with your ssh setup (~/.ssh/config, keys and ssh-agent). " +
		"The key must work without a password prompt, and the server must already be in known_hosts: " +
		"connect once with ssh in a terminal first. Secrets and the vault key are encrypted before they are sent.")
	form := dialog.NewForm("Vault on a Server", "Use This Location", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Location", entry),
			widget.NewFormItem("", help),
		},
		func(ok bool) {
			if !ok || a.createScreen == nil {
				return
			}
			loc, err := store.ParseLocation(entry.Text)
			if err != nil {
				return
			}
			a.log.Info("using a vault on a server", "location", loc.String())
			a.config.VaultDir = loc.String()
			a.showUnlockScreen()
		}, a.window)
	form.Resize(fyne.NewSize(520, form.MinSize().Height))
	form.Show()
	a.window.Canvas().Focus(entry)
}

// chooseExistingVault asks for a folder that already holds a vault.
func (a *App) chooseExistingVault() {
	fd := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			showError(a.window, "Couldn't choose a folder", err)
			return
		}
		if uri != nil {
			a.openExistingVault(uri.Path())
		}
	}, a.window)
	fd.Show()
}

// openExistingVault switches to the vault in dir and shows its unlock
// screen. A folder without a vault is refused on the create screen, with a
// hint if one of its subfolders holds a vault, so "Open" never creates
// anything. Like a switch in Settings, the location is saved only once the
// vault is unlocked.
func (a *App) openExistingVault(dir string) {
	screen := a.createScreen
	if screen == nil {
		return
	}
	screen.SetBusy(true)
	background(a.run,
		func(ctx context.Context) (folderCheck, error) { return checkFolder(ctx, dir) },
		func(found folderCheck, err error) {
			if a.createScreen != screen {
				return // the screen changed while the folder was checked
			}
			screen.SetBusy(false)
			switch {
			case err != nil:
				screen.ShowError(errorMessage("Couldn't look for a vault in "+dir, err))
			case found.vault:
				a.log.Info("opening existing vault", "dir", dir)
				a.config.VaultDir = dir
				a.showUnlockScreen()
			case found.inner != "":
				screen.ShowError(fmt.Sprintf("No vault was found in %s, but %s contains one. Open that folder instead.", dir, found.inner))
			default:
				screen.ShowError(fmt.Sprintf("No vault was found in %s. Choose the folder that holds the vault's vlt.age and group folders.", dir))
			}
		})
}

// folderCheck is what checkFolder found in a folder chosen to open.
type folderCheck struct {
	vault bool   // the folder holds a vault
	inner string // otherwise, a direct subfolder that holds one, if any
}

// checkFolder reports whether dir holds a vault and, if not, whether one of
// its direct subfolders does: picking the folder above a vault is an easy
// slip. Unreadable subfolders are skipped in that search.
func checkFolder(ctx context.Context, dir string) (folderCheck, error) {
	exists, err := store.Exists(ctx, dir)
	if err != nil || exists {
		return folderCheck{vault: exists}, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return folderCheck{}, nil
	}
	if err != nil {
		return folderCheck{}, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		inner := filepath.Join(dir, e.Name())
		if ok, err := store.Exists(ctx, inner); err == nil && ok {
			return folderCheck{inner: inner}, nil
		}
	}
	return folderCheck{}, nil
}

// onCreate creates a new vault with a confirmed passphrase in the background.
func (a *App) onCreate(passphrase string) {
	screen := a.createScreen
	screen.SetBusy(true)
	dir := a.config.VaultDir
	background(a.run,
		func(ctx context.Context) (openResult, error) {
			s, err := store.Create(ctx, dir, passphrase, a.log, a.storeOpts...)
			if err != nil {
				return openResult{}, err
			}
			return a.lockVault(ctx, s), nil
		},
		func(r openResult, err error) {
			if a.createScreen != screen {
				if r.store != nil {
					r.store.Close()
				}
				return
			}
			screen.SetBusy(false)
			if err != nil {
				screen.ShowError(errorMessage("Couldn't create the vault", err))
				return
			}
			a.unlockedOrAsk(r, screen.SetBusy, func() bool { return a.createScreen == screen })
		})
}

// lockVault takes the vault's lock file for the opened store s (in the
// background). If another copy of vlt holds it, the result says so and the
// user is asked. Any other failure to write the lock is logged and ignored:
// the lock only warns, and must not keep the user out of their vault.
func (a *App) lockVault(ctx context.Context, s *store.Store) openResult {
	err := s.AcquireLock(ctx, false)
	var inUse *store.InUseError
	switch {
	case errors.As(err, &inUse):
		return openResult{store: s, inUse: inUse}
	case err != nil:
		a.log.Warn("cannot record that the vault is open", "err", err)
	}
	return openResult{store: s}
}

// unlockedOrAsk goes on to the main view, unless another copy of vlt has
// the vault open: then it asks first, and on "Open Anyway" takes the lock
// over. current reports whether the screen that started the unlock is
// still shown; if it is not, or the user cancels, the store is closed.
func (a *App) unlockedOrAsk(r openResult, setBusy func(bool), current func() bool) {
	if r.inUse == nil {
		a.unlocked(r.store)
		return
	}
	s := r.store
	// No answer within the auto-lock timeout: the session closes s, as it
	// would lock an unlocked vault.
	a.session.Hold(s, func() {
		a.dismissDialogs()
		setBusy(false)
	})
	dialog.ShowCustomConfirm("Vault Already Open", "Open Anyway", "Cancel",
		widget.NewLabel(inUseMessage(r.inUse)),
		func(openAnyway bool) {
			if !a.session.Take(s) {
				return // already closed (timed out, or quitting)
			}
			if !openAnyway || !current() {
				s.Close()
				return
			}
			setBusy(true)
			background(a.run,
				func(ctx context.Context) (none, error) { return none{}, s.AcquireLock(ctx, true) },
				func(_ none, err error) {
					if !current() {
						s.Close()
						return
					}
					setBusy(false)
					if err != nil {
						a.log.Warn("cannot take over the vault lock", "err", err)
					}
					a.unlocked(s)
				})
		}, a.window)
}

// inUseMessage explains a lock held by another copy of vlt.
func inUseMessage(e *store.InUseError) string {
	who := "Another copy of vlt"
	if l := e.Lock; l.Host != "" {
		who = fmt.Sprintf("vlt on %s (user %s, process %d) has had it open since %s.\nIt", l.Host, l.User, l.PID, formatTime(l.Since))
	}
	return who + " may still be using this vault. If both save the same secret, the\n" +
		"last save wins and the other change is lost.\n\n" +
		"Open it anyway if that copy has quit or crashed, or you will not edit the same secrets."
}

// unlocked switches to the main view for an opened or newly created vault.
// It runs on the main goroutine.
func (a *App) unlocked(s *store.Store) {
	// Work started with the session's context has its result dropped once
	// the vault locks.
	ctx := a.session.Begin(s)
	a.unlockScreen, a.createScreen = nil, nil
	a.log.Info("vault unlocked", "dir", a.config.VaultDir)
	if !a.config.FromEnv.VaultDir {
		// The location is remembered once a vault has been opened or created
		// there, so a mistyped path never sticks. The default location is
		// saved too: it depends on the environment (XDG_DATA_HOME), which
		// can differ between a terminal and the desktop's app menu.
		SaveVaultDir(a.fyneApp.Preferences(), a.config.VaultDir)
	}

	a.showMainView(s, a.run.withContext(ctx))
	if loc := s.Location(); loc.IsRemote() {
		a.statusBar.SetText("Unlocked · vault on " + loc.Server())
	}

	a.announceRelease()

	if skipped := s.SkippedFiles(); len(skipped) > 0 {
		dialog.ShowInformation("Some secrets were not migrated",
			fmt.Sprintf("%d file(s) could not be decrypted with this passphrase and were left untouched:\n%s",
				len(skipped), strings.Join(skipped, "\n")),
			a.window)
	}
}

// showMainView displays the main secret browser for the unlocked store s.
// It takes s explicitly rather than calling a.session.Store(), which the
// auto-lock may clear concurrently. work is bound to the session context:
// work still in flight when the vault locks has its result dropped.
func (a *App) showMainView(s *store.Store, work runner) {
	a.detailView = NewDetailView(a.window, s, a.clipboard.Copy, a.statusBar, a.log, work)
	a.detailView.newTimer = a.newTimer
	a.secretList = NewSecretList(a.window, s, a.detailView, a.log, work)

	// Every save, delete and import updates the list in place, whether the
	// secret is new or was selected from the list.
	onSaved := a.secretList.Upsert
	a.detailView.SetCallbacks(onSaved, a.secretList.Remove, a.secretList.Groups)
	// An import may replace the secret whose form is open; re-render it.
	onImported := func(sec *store.Secret) {
		a.secretList.Upsert(sec)
		a.detailView.RefreshIfShowing(sec)
	}
	fileOps := NewFileOps(a.window, s, a.log, work, a.secretList.Groups, onImported)
	a.detailView.SetFileOps(fileOps)

	toolButton := func(label string, icon fyne.Resource, tapped func()) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, tapped)
		b.Importance = widget.LowImportance
		return b
	}
	lockBtn := widget.NewButtonWithIcon("Lock", lockIcon, func() { a.Lock() })
	brand := widget.NewLabel("vlt")
	brand.TextStyle = fyne.TextStyle{Bold: true}
	header := panelBackground(container.NewVBox(
		padded(container.NewBorder(nil, nil,
			container.NewHBox(brandMark(22), brand),
			container.NewHBox(
				toolButton("Add Password", keyIcon, a.detailView.ShowNewPassword),
				toolButton("Add Record", theme.AccountIcon(), a.detailView.ShowNewRecord),
				toolButton("Import File", theme.UploadIcon(), fileOps.ImportFile),
				toolButton("Settings", theme.SettingsIcon(), func() { a.openSettings(s, work) }),
				lockBtn,
			)), 4),
		widget.NewSeparator(),
	))

	// Split: list on left, detail on right
	split := container.NewHSplit(
		a.secretList.Content(),
		a.detailView.Panel(),
	)
	split.SetOffset(0.32)

	a.mainContent = container.NewBorder(
		header,
		a.statusRow(),
		nil,
		nil,
		split,
	)

	a.window.SetContent(a.mainContent)
	a.statusBar.SetText("Unlocked")

	// Any input in the main view resets the auto-lock countdown. The detail
	// panel re-instruments its widgets each time it renders a new form.
	a.tracker = newActivityTracker(a.resetLockTimer)
	a.tracker.instrument(a.mainContent)
	a.detailView.onRender = func(panel fyne.CanvasObject) {
		if a.tracker != nil {
			a.tracker.forget()
			a.tracker.instrument(panel)
		}
	}
	// Dialog buttons and file pickers are not part of the instrumented
	// tree; components report activity from their callbacks instead.
	a.detailView.activity = a.resetLockTimer
	fileOps.activity = a.resetLockTimer
	a.window.Canvas().SetOnTypedKey(func(*fyne.KeyEvent) { a.resetLockTimer() })
	a.window.Canvas().SetOnTypedRune(func(rune) { a.resetLockTimer() })

	// Load secrets in the background; Lock cancels the session context.
	a.secretList.Refresh()
}

// Lock zeros the vault key, cancels the session, clears any secret vlt copied
// to the clipboard, and returns to the unlock screen. It may be called from
// any goroutine; when the auto-lock calls it (on the main goroutine), the
// session has already ended.
func (a *App) Lock() {
	a.session.End()
	a.log.Info("vault locked")

	// fyne.Do can run inline; the session's lock is not held here, so
	// widget callbacks may call back into the app (resetLockTimer).
	fyne.Do(func() {
		a.hideLockWarning()
		a.dismissDialogs()
		a.clipboard.Clear()
		a.dropSession()
		a.statusBar.SetText("Locked")
		a.showUnlockScreen()
	})
}

// openSettings shows the Settings screen in the detail panel, wired to save
// changes and relock when needed.
func (a *App) openSettings(s *store.Store, work runner) *SettingsScreen {
	settings := NewSettingsScreen(a.window, &a.config, s, a.statusBar, a.log, work, a.Lock)
	settings.activity = a.resetLockTimer
	settings.save = a.saveSetting
	settings.checkUpdates = a.checkForUpdates
	a.detailView.ShowContent(settings.Content())
	return settings
}

// saveSetting persists one setting the user changed in Settings.
func (a *App) saveSetting(setting string) {
	prefs := a.fyneApp.Preferences()
	switch setting {
	case prefLockTimeout:
		SaveLockTimeout(prefs, a.config.LockTimeoutMs)
	case prefClipboardTTL:
		SaveClipboardTTL(prefs, a.config.ClipboardTTLs)
	case prefCheckUpdates:
		prefs.SetBool(prefCheckUpdates, a.config.CheckUpdates)
	}
}

// dropSession releases the references vlt holds to decrypted secrets: it
// empties every loaded secret struct (so anything still pointing at one,
// such as a closure in a widget held by Fyne's renderer cache, reaches only
// empty fields), blanks the detail form's entries, wipes a pending export
// buffer, and drops the list, detail panel, main view and activity tracker.
// Go cannot wipe the strings themselves; they become unreachable and are
// reclaimed by the garbage collector. Background work still in flight holds
// its own copy until it completes and its result is dropped. It runs on the
// main goroutine.
func (a *App) dropSession() {
	if a.detailView != nil {
		a.detailView.clear()
	}
	if a.secretList != nil {
		a.secretList.clear()
	}
	a.secretList, a.detailView, a.mainContent, a.tracker = nil, nil, nil, nil
	a.window.Canvas().SetOnTypedKey(nil)
	a.window.Canvas().SetOnTypedRune(nil)
}

// dismissDialogs removes every dialog (Fyne draws dialogs, including file
// pickers, as overlays inside the window) so none outlives the session: an
// export dialog left open must not write plaintext after a lock, and a
// passphrase form must not linger over the unlock screen. Dialog callbacks
// are not run.
func (a *App) dismissDialogs() {
	overlays := a.window.Canvas().Overlays()
	for top := overlays.Top(); top != nil; top = overlays.Top() {
		overlays.Remove(top)
	}
}

// quit clears any secret vlt put on the clipboard and exits. It runs on the
// main goroutine (window close intercept or the Quit menu item).
func (a *App) quit() {
	a.clipboard.Clear()
	if !a.session.Close(quitWait) {
		a.log.Warn("quitting before the vault released its lock")
	}
	a.fyneApp.Quit()
}

// quitWait bounds how long quitting waits for a vault on a server to
// release its lock; a dead network must not stop vlt from quitting.
const quitWait = 3 * time.Second

// lockTimeout returns the configured auto-lock timeout; 0 disables it.
func (a *App) lockTimeout() time.Duration {
	if a.config.LockTimeoutMs <= 0 {
		return 0
	}
	return time.Duration(a.config.LockTimeoutMs) * time.Millisecond
}

// resetLockTimer restarts the inactivity countdown. It is called on every
// user input (see activityTracker) on the main goroutine, and re-reads the
// timeout, so a change in Settings applies immediately.
func (a *App) resetLockTimer() {
	// Unlocked: restart, which also arms the timer if auto-lock was just
	// re-enabled in Settings, or disarms it if it was turned off.
	a.session.Activity()
	a.hideLockWarning()
}

// showLockWarning tells the user the vault is about to lock for inactivity
// (the session calls it only while its countdown is current). "Stay
// unlocked" (or any other input) restarts the countdown; "Lock now" locks
// immediately.
func (a *App) showLockWarning() {
	if a.lockWarning != nil {
		return
	}
	var d *dialog.ConfirmDialog
	d = dialog.NewConfirm("Locking soon",
		fmt.Sprintf("The vault will lock in %d seconds because of inactivity. Unsaved changes will be lost.",
			int(lockWarningLead.Seconds())),
		func(stay bool) {
			if a.lockWarning != d {
				return // hidden by hideLockWarning (activity or lock), not by the user
			}
			a.lockWarning = nil
			if stay {
				a.resetLockTimer()
			} else {
				a.Lock()
			}
		}, a.window)
	d.SetConfirmText("Stay unlocked")
	d.SetDismissText("Lock now")
	a.lockWarning = d
	d.Show()
}

// hideLockWarning dismisses the lock warning, if shown. It clears
// a.lockWarning first so the dialog's callback (which Fyne runs on Hide)
// knows the dismissal was not the user's choice.
func (a *App) hideLockWarning() {
	if d := a.lockWarning; d != nil {
		a.lockWarning = nil
		d.Hide()
	}
}
