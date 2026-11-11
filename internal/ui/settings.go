package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/crypto"
	"github.com/heyjdp/vlt/internal/store"
)

// SettingsScreen displays vault configuration options.
type SettingsScreen struct {
	window    fyne.Window
	settings  *Settings
	store     *store.Store
	log       *slog.Logger
	statusBar *widget.Label
	// ops runs the passphrase change and the vault copy, asks before moving
	// or switching, and shows the folder picker.
	ops      *operations
	onRelock func()
	// checkUpdates checks for a newer release now (set by the app).
	checkUpdates func()

	content *fyne.Container
}

// NewSettingsScreen creates a settings panel. Changes go to settings, which
// saves them; a new vault location is only proposed, and the app commits it
// once a vault is opened or created there.
func NewSettingsScreen(
	window fyne.Window,
	settings *Settings,
	s *store.Store,
	statusBar *widget.Label,
	log *slog.Logger,
	ops *operations,
	onRelock func(),
) *SettingsScreen {
	ss := &SettingsScreen{
		window:    window,
		settings:  settings,
		store:     s,
		log:       log,
		statusBar: statusBar,
		ops:       ops,
		onRelock:  onRelock,

		checkUpdates: func() {},
	}

	ss.content = ss.build()
	return ss
}

// Content returns the settings panel widget.
func (ss *SettingsScreen) Content() *fyne.Container {
	return ss.content
}

func (ss *SettingsScreen) build() *fyne.Container {
	return container.NewVBox(
		heading("Settings"),
		ss.buildChangePassphrase(),
		ss.buildVaultLocation(),
		card(subheading("Auto-lock"),
			muted("Lock the vault after a period without input."),
			ss.buildLockTimeout()),
		card(subheading("Clipboard"),
			muted("Clear a copied secret from the clipboard after this long."),
			ss.buildClipboardTTL()),
		ss.buildUpdates(),
	)
}

func (ss *SettingsScreen) buildChangePassphrase() *fyne.Container {
	currentEntry := widget.NewPasswordEntry()
	currentEntry.SetPlaceHolder("Current passphrase")

	newEntry := widget.NewPasswordEntry()
	newEntry.SetPlaceHolder("New passphrase")

	confirmEntry := widget.NewPasswordEntry()
	confirmEntry.SetPlaceHolder("Confirm new passphrase")

	errorLabel := errorText()

	var changeBtn *widget.Button
	changeBtn = widget.NewButton("Change Passphrase", func() {
		errorLabel.Hide()

		// Read, then clear, the passphrase entries on every path so no
		// passphrase lingers in a widget.
		current := currentEntry.Text
		newPass := newEntry.Text
		confirm := confirmEntry.Text
		currentEntry.SetText("")
		newEntry.SetText("")
		confirmEntry.SetText("")

		if current == "" || newPass == "" || confirm == "" {
			errorLabel.SetText("All fields are required")
			errorLabel.Show()
			return
		}
		if err := validateNewPassphrase(newPass, confirm); err != nil {
			errorLabel.SetText("New " + err.Error())
			errorLabel.Show()
			return
		}
		if newPass == current {
			errorLabel.SetText("New passphrase must be different from current")
			errorLabel.Show()
			return
		}

		// Only vlt.age (the wrapped vault key) is rewritten; secrets are
		// untouched. Two scrypt derivations, so it runs in the background,
		// with the auto-lock paused so it can't cut off the write.
		start(ss.ops, &operation{
			kind:         opVault,
			progress:     "Changing passphrase…",
			control:      changeBtn,
			keepUnlocked: true,
			failed: func(err error) {
				errorLabel.SetText(passphraseChangeFailure(err))
				errorLabel.Show()
			},
			afterEnd: ss.passphraseChangeOutlived,
		},
			func(ctx context.Context) (none, error) {
				return none{}, ss.store.ChangePassphrase(ctx, current, newPass)
			},
			func(none) {
				ss.statusBar.SetText("Passphrase changed")
				ss.relockThenInform("Passphrase Changed",
					"Your vault key is now protected by the new passphrase.\nThe vault has locked; unlock it with the new passphrase.")
			})
	})
	changeBtn.Importance = widget.HighImportance

	return card(
		subheading("Change Passphrase"),
		muted("Only the vault key is re-encrypted; your secrets are not rewritten. The vault locks afterwards."),
		container.New(layout.NewFormLayout(),
			fieldLabel("Current"), currentEntry,
			fieldLabel("New"), newEntry,
			fieldLabel("Confirm"), confirmEntry,
		),
		errorLabel,
		container.NewHBox(changeBtn),
	)
}

// passphraseChangeOutlived tells the user, over the unlock screen, how a
// passphrase change ended when the vault locked (by hand, or after the
// paused auto-lock's long wait) before it could say so. Lock dismisses
// dialogs from a queued fyne.Do, so the message is queued after it.
func (ss *SettingsScreen) passphraseChangeOutlived(err error) {
	window := ss.window
	switch {
	case err == nil:
		fyne.Do(func() {
			dialog.ShowInformation("Passphrase Changed",
				"The vault locked as the passphrase change finished.\nYour vault key is now protected by the new passphrase; unlock it with the new passphrase.", window)
		})
	case errors.Is(err, context.Canceled) || errors.Is(err, crypto.ErrIdentityZeroed):
		// Stopped before vlt.age was written: the lock came first.
		fyne.Do(func() {
			dialog.ShowInformation("Passphrase Not Changed",
				"The vault locked before the change was saved, so the passphrase was not changed.\nUnlock with your current passphrase.", window)
		})
	default:
		text := passphraseChangeFailure(err)
		fyne.Do(func() { dialog.ShowError(errors.New(text), window) })
	}
}

// passphraseChangeFailure says why a passphrase change failed. When the
// server's reply was lost, vlt.age may have been renamed into place on the
// server all the same, so the user is told the change may have been saved
// and which passphrase to try.
func passphraseChangeFailure(err error) string {
	switch {
	case errors.Is(err, store.ErrWrongPassphrase):
		return "Current passphrase is incorrect"
	case isServerError(err):
		return errorMessage("Couldn't confirm the passphrase change", err) +
			". The change may or may not have been saved: the server may have made it though its reply was lost. " +
			"If your current passphrase is refused next time you unlock, try the new one."
	}
	return errorMessage("Couldn't change the passphrase", err)
}

func (ss *SettingsScreen) buildVaultLocation() *fyne.Container {
	currentLabel := widget.NewLabel("Current: " + ss.settings.Vault().String())
	currentLabel.Wrapping = fyne.TextWrapBreak

	pathEntry := widget.NewEntry()
	pathEntry.SetPlaceHolder("Folder, or user@host:/path on an SSH server")

	errorLabel := errorText()

	browseBtn := widget.NewButtonWithIcon("Browse…", theme.FolderOpenIcon(), func() {
		ss.ops.pickFolder(pathEntry.SetText)
	})

	var moveBtn *widget.Button
	moveBtn = widget.NewButton("Move Vault", func() {
		errorLabel.Hide()

		newDir, ok := ss.targetLocation(pathEntry.Text, errorLabel, "Please enter or browse to a new vault directory")
		if !ok {
			return
		}
		if newDir == ss.settings.Vault() {
			errorLabel.SetText("New location is the same as current")
			errorLabel.Show()
			return
		}

		ss.ops.confirm(&question{
			title: "Move Vault",
			message: fmt.Sprintf("Copy all secrets from:\n  %s\nto:\n  %s\n\nThe original vault will not be deleted.",
				ss.settings.Vault(), newDir),
		}, func() { ss.moveVault(newDir, moveBtn, errorLabel, currentLabel) })
	})
	moveBtn.Importance = widget.WarningImportance

	switchBtn := widget.NewButton("Switch Vault (no copy)", func() {
		errorLabel.Hide()

		newDir, ok := ss.targetLocation(pathEntry.Text, errorLabel, "Please enter or browse to a vault directory")
		if !ok {
			return
		}
		if newDir == ss.settings.Vault() {
			errorLabel.SetText("Already using that vault directory")
			errorLabel.Show()
			return
		}

		ss.ops.confirm(&question{
			title:   "Switch Vault",
			message: fmt.Sprintf("Switch to vault at:\n  %s\n\nThe app will lock. If a vault exists there you will unlock it with its passphrase; otherwise you will be asked to create one.", newDir),
		}, func() {
			ss.ops.now(&operation{kind: opVault}, func() { ss.switchVault(newDir, currentLabel) })
		})
	})

	if variable, ok := ss.settings.SetByEnv(SettingVault); ok {
		currentLabel.SetText(currentLabel.Text + " (set by " + variable + ")")
		pathEntry.Disable()
		browseBtn.Disable()
		moveBtn.Disable()
		switchBtn.Disable()
	}

	return card(
		subheading("Vault Location"),
		currentLabel,
		muted("Move copies every secret to a new folder and keeps the original. Switch opens the vault in another folder, or creates one there. "+
			"Either can be a folder on an SSH server (user@host:/path), reached with your ssh setup; secrets and the vault key are encrypted before they are sent."),
		container.NewBorder(nil, nil, nil, browseBtn, pathEntry),
		errorLabel,
		container.NewHBox(moveBtn, switchBtn),
	)
}

// targetLocation reads the location typed for Move or Switch: it must parse
// (a folder, or a folder on a server). On a problem it shows the error and
// returns false; empty shows emptyMsg.
func (ss *SettingsScreen) targetLocation(text string, errorLabel *widget.Label, emptyMsg string) (store.Location, bool) {
	if strings.TrimSpace(text) == "" {
		errorLabel.SetText(emptyMsg)
		errorLabel.Show()
		return store.Location{}, false
	}
	loc, err := store.ParseLocation(text)
	if err != nil {
		errorLabel.SetText(errorMessage("", err))
		errorLabel.Show()
		return store.Location{}, false
	}
	return loc, true
}

// moveVault copies the vault to newDir in the background, with moveBtn
// disabled meanwhile; while it runs, no other vault-wide change starts, and
// the auto-lock waits (a copy to a server can outlast its timeout).
func (ss *SettingsScreen) moveVault(newDir store.Location, moveBtn fyne.Disableable, errorLabel, currentLabel *widget.Label) {
	start(ss.ops, &operation{
		kind:         opVault,
		progress:     "Copying vault…",
		control:      moveBtn,
		keepUnlocked: true,
		failed: func(err error) {
			errorLabel.SetText(moveFailure(err))
			errorLabel.Show()
		},
	},
		func(ctx context.Context) (none, error) { return none{}, ss.store.CopyTo(ctx, newDir.String()) },
		func(none) { ss.vaultMoved(newDir, currentLabel) })
}

// moveFailure is what a failed move says under the location.
func moveFailure(err error) string {
	if errors.Is(err, store.ErrStaleCopy) {
		// A move cut short, then the vault changed: finishing that copy
		// would leave it out of date, and what is there is safe to delete.
		return "Couldn't move the vault: that folder holds an unfinished copy of this vault, made before the vault last changed. Delete that folder, then move the vault again."
	}
	return errorMessage("Couldn't move the vault", err)
}

// vaultMoved proposes newDir after a successful copy and relocks so the
// user unlocks the vault at its new location.
func (ss *SettingsScreen) vaultMoved(newDir store.Location, currentLabel *widget.Label) {
	ss.settings.ProposeVault(newDir)
	currentLabel.SetText("Current: " + newDir.String())
	ss.log.Info("vault moved", "to", newDir.String())
	ss.statusBar.SetText("Vault moved to " + newDir.String())

	ss.relockThenInform("Vault Moved",
		fmt.Sprintf("Secrets copied to:\n  %s\n\nThe vault has locked; unlock it at its new location.", newDir))
}

// relockThenInform locks the vault, then shows a message on top of the
// unlock screen. Lock dismisses open dialogs from a queued fyne.Do, so the
// message is queued after it rather than shown first.
func (ss *SettingsScreen) relockThenInform(title, message string) {
	ss.onRelock()
	window := ss.window
	fyne.Do(func() { dialog.ShowInformation(title, message, window) })
}

// switchVault proposes newDir and relocks, so its unlock or create screen
// shows.
func (ss *SettingsScreen) switchVault(newDir store.Location, currentLabel *widget.Label) {
	ss.settings.ProposeVault(newDir)
	currentLabel.SetText("Current: " + newDir.String())
	ss.log.Info("vault switched", "to", newDir.String())
	ss.statusBar.SetText("Switched to vault at " + newDir.String())
	ss.onRelock()
}

// buildLockTimeout is the auto-lock check box and timeout slider. The
// label follows the slider; the timeout is applied and saved when a drag
// ends (Fyne also ends a change on a tap or key press).
func (ss *SettingsScreen) buildLockTimeout() *fyne.Container {
	current := int(ss.settings.LockTimeout().Milliseconds())
	label := widget.NewLabel(formatLockTimeout(current))
	slider := widget.NewSlider(MinLockTimeoutMs, MaxLockTimeoutMs)
	slider.Step = 30000
	if current > 0 {
		slider.SetValue(float64(current))
	} else {
		slider.SetValue(DefaultLockTimeoutMs)
		slider.Disable()
	}
	enabled := widget.NewCheck("Auto-lock", nil)
	enabled.SetChecked(current > 0)

	apply := func(ms int) {
		ss.settings.SetLockTimeout(time.Duration(ms) * time.Millisecond)
		label.SetText(formatLockTimeout(int(ss.settings.LockTimeout().Milliseconds())))
	}
	slider.OnChanged = func(v float64) { label.SetText(formatLockTimeout(int(v))) }
	slider.OnChangeEnded = func(v float64) { apply(int(v)) }
	enabled.OnChanged = func(on bool) {
		if on {
			slider.Enable()
			apply(int(slider.Value))
		} else {
			slider.Disable()
			apply(0)
		}
	}

	if variable, ok := ss.settings.SetByEnv(SettingLockTimeout); ok {
		slider.Disable()
		enabled.Disable()
		label.SetText(label.Text + " (set by " + variable + ")")
	}
	return container.NewVBox(label, enabled, slider)
}

// buildClipboardTTL is the clipboard TTL slider, applied and saved when a
// change ends, as for the auto-lock timeout.
func (ss *SettingsScreen) buildClipboardTTL() *fyne.Container {
	ttlText := func(secs int) string { return fmt.Sprintf("Clipboard TTL: %d seconds", secs) }
	current := int(ss.settings.ClipboardTTL() / time.Second)
	label := widget.NewLabel(ttlText(current))
	slider := widget.NewSlider(MinClipboardTTLs, MaxClipboardTTLs)
	slider.SetValue(float64(current))
	slider.Step = 5
	slider.OnChanged = func(v float64) { label.SetText(ttlText(int(v))) }
	slider.OnChangeEnded = func(v float64) {
		ss.settings.SetClipboardTTL(time.Duration(v) * time.Second)
		label.SetText(ttlText(int(ss.settings.ClipboardTTL() / time.Second)))
	}

	if variable, ok := ss.settings.SetByEnv(SettingClipboardTTL); ok {
		slider.Disable()
		label.SetText(label.Text + " (set by " + variable + ")")
	}
	return container.NewVBox(label, slider)
}

// buildUpdates is the opt-in startup check for a newer release, and a
// button to check now.
func (ss *SettingsScreen) buildUpdates() *fyne.Container {
	atStart := widget.NewCheck("Check for updates when vlt starts", nil)
	atStart.SetChecked(ss.settings.CheckUpdates())
	atStart.OnChanged = ss.settings.SetCheckUpdates
	checkNow := widget.NewButton("Check Now", func() { ss.checkUpdates() })
	return card(subheading("Updates"),
		muted("When this is on, vlt asks GitHub for its latest release at start-up and, once the vault is unlocked, tells you once about each new one. "+
			"Nothing is downloaded or installed; a new version is a link to its release page."),
		atStart,
		container.NewHBox(checkNow))
}
