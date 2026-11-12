// Package ui implements the Fyne-based GUI for the vlt secret manager.
package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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
	// checkedManually records that a check the user asked for has run since
	// start-up, so the start-up check announces nothing more;
	// pendingRelease is a release the start-up check found, held until the
	// vault is unlocked. Both are used on the main goroutine only.
	checkedManually bool
	pendingRelease  *update.Release
	settings        *Settings
	log             *slog.Logger
	// session holds the open store and runs the auto-lock.
	session  *session[*store.Store]
	newTimer timerFactory
	// opener takes the vault at the chosen location to a session; it holds
	// the Waiting vault meanwhile.
	opener *opener
	run    runner
	// pick shows the file and folder choosers (Fyne's dialogs; tests
	// answer them).
	pick picker
	// ops runs what is asked of the app itself (update checks) and
	// sessionOps what is done to the open vault (nil while locked).
	ops        *operations
	sessionOps *operations

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
func NewApp(fyneApp fyne.App, window fyne.Window, settings *Settings, log *slog.Logger, version string) *App {
	app := newVersionedApp(version, fyneApp, window, settings, log, goroutineRunner(), realTimers)
	app.startUpdateChecks(update.NewChecker().Latest)
	return app
}

// newApp creates the controller with an explicit runner, timer factory and
// store options (tests run work inline, fire timers by hand, and use a cheap
// scrypt work factor).
func newApp(fyneApp fyne.App, window fyne.Window, settings *Settings, log *slog.Logger, run runner, timers timerFactory, storeOpts ...store.Option) *App {
	return newVersionedApp("", fyneApp, window, settings, log, run, timers, storeOpts...)
}

// newVersionedApp is newApp with the build's version, set before the first
// screen starts any background work.
func newVersionedApp(version string, fyneApp fyne.App, window fyne.Window, settings *Settings, log *slog.Logger, run runner, timers timerFactory, storeOpts ...store.Option) *App {
	app := &App{
		version:  version,
		open:     fyneApp.OpenURL,
		latest:   noUpdateCheck,
		fyneApp:  fyneApp,
		window:   window,
		settings: settings,
		log:      log,
		run:      run,
		newTimer: timers,
		pick:     fynePicker{window: window},
	}
	app.ops = newOperations(window, run, nil, app.resetLockTimer, nil, app.pick)

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

	app.session = newSession[*store.Store](timers, fyne.Do, run.spawn, settings.LockTimeout, sessionCallbacks{
		Warn: app.showLockWarning,
		// The session has already closed the store (zeroing its key);
		// Lock returns to the unlock screen.
		TimedOut: func() {
			app.log.Info("auto-lock triggered")
			app.Lock()
		},
	})
	// storeOpts lower the scrypt work factor in tests; production passes none.
	app.opener = newOpener(app.session, run, fyne.Do, timers, settings.LockTimeout, log, storeOpts...)
	app.statusBar = caption("Locked")
	app.clipboard = newClipboardClearer(fyneApp.Clipboard(), app.statusBar, settings.ClipboardTTL, timers)
	app.showUnlockScreen()

	return app
}

// showUnlockScreen displays the passphrase entry screen, or the create-vault
// screen (passphrase entered twice) if no vault exists at the configured
// location. Inspecting the directory runs in the background. Whatever the
// screen shown before had started is abandoned.
func (a *App) showUnlockScreen() {
	a.opener.Abandon()
	a.unlockScreen, a.createScreen = nil, nil
	// A location that does not parse is shown as given, with the reason.
	if text, unusable := a.settings.UnusableVault(); unusable != nil {
		a.setScreen(a.vaultErrorContent(text, unusable))
		return
	}
	a.setScreen(container.NewCenter(muted("Checking vault…")))
	dir := a.settings.Vault().String()
	a.opener.Look(dir, func(exists bool, err error) {
		switch {
		case err != nil:
			// Never offer to create a vault over a directory we cannot inspect.
			a.setScreen(a.vaultErrorContent(dir, err))
		case exists:
			a.unlockScreen = NewUnlockScreen(a.window, dir, a.onUnlock)
			a.setScreen(a.unlockScreen.Content())
		default:
			var onOpen, onServer func()
			if _, fixed := a.settings.SetByEnv(SettingVault); !fixed {
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

// vaultErrorContent explains why the vault at dir cannot be used and offers
// a retry (for example after fixing permissions, or once the server is
// back), and, for a vault elsewhere, a way back to the default local vault.
func (a *App) vaultErrorContent(dir string, err error) fyne.CanvasObject {
	heading := "Cannot read the vault directory"
	if isServerError(err) {
		heading = "Cannot reach the vault's server"
	}
	title := subheading(heading)
	title.Alignment = fyne.TextAlignCenter
	msg := muted(fmt.Sprintf("%s\n\n%s", dir, errorMessage("", err)))
	retry := widget.NewButtonWithIcon("Retry", theme.ViewRefreshIcon(), a.showUnlockScreen)
	retry.Importance = widget.HighImportance
	icon := container.NewCenter(fixedHeight(fixedWidth(widget.NewIcon(theme.ErrorIcon()), 40), 40))
	objects := []fyne.CanvasObject{icon, title, msg, retry}
	if def, ok := a.settings.DefaultVault(); ok {
		local := widget.NewButtonWithIcon("Use the Vault on This Computer", theme.ComputerIcon(), func() {
			a.log.Info("switching to the default vault location", "dir", def.String())
			a.settings.ProposeVault(def)
			a.showUnlockScreen()
		})
		local.Importance = widget.LowImportance
		objects = append(objects, local)
	}
	return centeredCard(createFormWidth, objects...)
}

// onUnlock unlocks the vault shown on the unlock screen with passphrase.
func (a *App) onUnlock(passphrase string) {
	screen := a.unlockScreen
	a.opener.Unlock(a.settings.Vault().String(), passphrase, screen.SetBusy,
		func(o openOutcome) { a.showOutcome(o, screen.ShowError) })
}

// onCreate creates a new vault with a confirmed passphrase.
func (a *App) onCreate(passphrase string) {
	screen := a.createScreen
	a.opener.Create(a.settings.Vault().String(), passphrase, screen.SetBusy,
		func(o openOutcome) { a.showOutcome(o, screen.ShowError) })
}

// showOutcome shows what came of unlocking or creating a vault; showError
// shows a problem on the screen the attempt started from.
func (a *App) showOutcome(o openOutcome, showError func(string)) {
	switch o.kind {
	case vaultOpened:
		a.unlocked(o.ctx, o.store)
	case vaultWaiting:
		a.askOpenAnyway(o.holder)
	case waitTimedOut:
		a.dismissDialogs()
	case vaultGone:
		// The vault disappeared since the screen was chosen; offer to create one.
		a.showUnlockScreen()
	default:
		showError(o.problem())
	}
}

// askOpenAnyway asks whether to open the Waiting vault although another
// copy of vlt, holder, has it open, and passes the answer to the opener.
func (a *App) askOpenAnyway(holder *store.LockInfo) {
	dialog.ShowCustomConfirm("Vault Already Open", "Open Anyway", "Cancel",
		widget.NewLabel(inUseMessage(holder)),
		func(openAnyway bool) {
			if openAnyway {
				a.opener.OpenAnyway()
			} else {
				a.opener.Cancel()
			}
		}, a.window)
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
			a.settings.ProposeVault(loc)
			a.showUnlockScreen()
		}, a.window)
	form.Resize(fyne.NewSize(520, form.MinSize().Height))
	form.Show()
	a.window.Canvas().Focus(entry)
}

// chooseExistingVault asks for a folder that already holds a vault.
func (a *App) chooseExistingVault() {
	a.ops.pickFolder(a.openExistingVault)
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
	a.opener.CheckFolder(dir, screen.SetBusy, func(found folderCheck, err error) {
		switch {
		case err != nil:
			screen.ShowError(errorMessage("Couldn't look for a vault in "+dir, err))
		case found.vault:
			a.log.Info("opening existing vault", "dir", dir)
			a.settings.ProposeVault(store.Location{Path: dir})
			a.showUnlockScreen()
		case found.inner != "":
			screen.ShowError(fmt.Sprintf("No vault was found in %s, but %s contains one. Open that folder instead.", dir, found.inner))
		default:
			screen.ShowError(fmt.Sprintf("No vault was found in %s. Choose the folder that holds the vault's vlt.age and group folders.", dir))
		}
	})
}

// inUseMessage explains a lock held by another copy of vlt; l is zero when
// the lock could not be read.
func inUseMessage(l *store.LockInfo) string {
	who := "Another copy of vlt"
	if l.Host != "" {
		who = fmt.Sprintf("vlt on %s (user %s, process %d) has had it open since %s.\nIt", l.Host, l.User, l.PID, formatTime(l.Since))
	}
	return who + " may still be using this vault. If both save the same secret, the\n" +
		"last save wins and the other change is lost.\n\n" +
		"Open it anyway if that copy has quit or crashed, or you will not edit the same secrets."
}

// unlocked switches to the main view for the vault s, opened or newly
// created, on which the session has begun with context ctx. It runs on the
// main goroutine.
func (a *App) unlocked(ctx context.Context, s *store.Store) {
	a.unlockScreen, a.createScreen = nil, nil
	a.log.Info("vault unlocked", "dir", a.settings.Vault().String())
	a.settings.CommitVault()

	// Work started with the session's context has its result dropped once
	// the vault locks.
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
	a.sessionOps = newOperations(a.window, work, a.statusBar, a.resetLockTimer, a.session.Pause, a.pick)
	ops := a.sessionOps
	a.detailView = NewDetailView(a.window, s, a.clipboard.Copy, a.statusBar, a.log, ops)
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
	fileOps := NewFileOps(a.window, s, a.log, ops, a.secretList.Groups, onImported)
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
				toolButton("Settings", theme.SettingsIcon(), func() { a.openSettings(s, ops) }),
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
	// Dialogs and file pickers are not part of the instrumented tree; the
	// session's operations report the answers to them.
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

// openSettings shows the Settings screen in the detail panel, wired to
// relock when needed.
func (a *App) openSettings(s *store.Store, ops *operations) *SettingsScreen {
	settings := NewSettingsScreen(a.window, a.settings, s, a.statusBar, a.log, ops, a.Lock)
	settings.checkUpdates = a.checkForUpdates
	a.detailView.ShowContent(settings.Content())
	return settings
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
	if a.sessionOps != nil {
		a.sessionOps.end() // wipes an export waiting on its save dialog
	}
	if a.detailView != nil {
		a.detailView.clear()
	}
	if a.secretList != nil {
		a.secretList.clear()
	}
	a.secretList, a.detailView, a.mainContent, a.tracker, a.sessionOps = nil, nil, nil, nil, nil
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
	if !a.closeVaults(quitWait) {
		a.log.Warn("quitting before the vault released its lock")
	}
	a.fyneApp.Quit()
}

// closeVaults lets go of the open vault and the Waiting vault, if any,
// and waits up to wait for every vault let go of to release its lock. It
// reports whether they all did in time.
func (a *App) closeVaults(wait time.Duration) bool {
	a.opener.Abandon()
	return a.session.Close(wait)
}

// quitWait bounds how long quitting waits for a vault on a server to
// release its lock; a dead network must not stop vlt from quitting.
const quitWait = 3 * time.Second

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
