package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
)

// SettingsScreen displays vault configuration options.
type SettingsScreen struct {
	window    fyne.Window
	config    *Config
	store     *store.Store
	log       *slog.Logger
	statusBar *widget.Label
	run       runner
	onRelock  func()
	// activity reports user input in the folder picker and confirmation
	// dialogs, which are outside the instrumented widget tree.
	activity func()
	// save persists one setting (prefLockTimeout or prefClipboardTTL) after
	// the user changes it; it is set by the app. A new vault location is
	// saved by the app once a vault is opened or created there.
	save func(setting string)

	content *fyne.Container
}

// NewSettingsScreen creates a settings panel.
func NewSettingsScreen(
	window fyne.Window,
	config *Config,
	s *store.Store,
	statusBar *widget.Label,
	log *slog.Logger,
	run runner,
	onRelock func(),
) *SettingsScreen {
	ss := &SettingsScreen{
		window:    window,
		config:    config,
		store:     s,
		log:       log,
		statusBar: statusBar,
		run:       run,
		onRelock:  onRelock,
		activity:  noActivity,
		save:      func(string) {},
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
		// untouched. Two scrypt derivations, so it runs in the background.
		changeBtn.Disable()
		ss.statusBar.SetText("Changing passphrase…")
		background(ss.run,
			func(ctx context.Context) (none, error) {
				return none{}, ss.store.ChangePassphrase(ctx, current, newPass)
			},
			func(_ none, err error) {
				changeBtn.Enable()
				if err != nil {
					ss.statusBar.SetText("")
					if errors.Is(err, store.ErrWrongPassphrase) {
						errorLabel.SetText("Current passphrase is incorrect")
					} else {
						errorLabel.SetText("Passphrase change failed: " + err.Error())
					}
					errorLabel.Show()
					return
				}

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

func (ss *SettingsScreen) buildVaultLocation() *fyne.Container {
	currentLabel := widget.NewLabel("Current: " + ss.config.VaultDir)
	currentLabel.Wrapping = fyne.TextWrapBreak

	pathEntry := widget.NewEntry()
	pathEntry.SetPlaceHolder("New vault directory path")

	errorLabel := errorText()

	browseBtn := widget.NewButtonWithIcon("Browse…", theme.FolderOpenIcon(), func() {
		fd := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			ss.activity()
			if err != nil {
				dialog.ShowError(err, ss.window)
				return
			}
			if uri == nil {
				return
			}
			pathEntry.SetText(uri.Path())
		}, ss.window)
		fd.Show()
	})

	moveBtn := widget.NewButton("Move Vault", func() {
		errorLabel.Hide()

		newDir := pathEntry.Text
		if newDir == "" {
			errorLabel.SetText("Please enter or browse to a new vault directory")
			errorLabel.Show()
			return
		}
		if newDir == ss.config.VaultDir {
			errorLabel.SetText("New location is the same as current")
			errorLabel.Show()
			return
		}

		dialog.ShowConfirm("Move Vault",
			fmt.Sprintf("Copy all secrets from:\n  %s\nto:\n  %s\n\nThe original vault will not be deleted.",
				ss.config.VaultDir, newDir),
			func(confirmed bool) {
				ss.activity()
				if !confirmed {
					return
				}
				ss.moveVault(newDir, errorLabel, currentLabel)
			},
			ss.window,
		)
	})
	moveBtn.Importance = widget.WarningImportance

	switchBtn := widget.NewButton("Switch Vault (no copy)", func() {
		errorLabel.Hide()

		newDir := pathEntry.Text
		if newDir == "" {
			errorLabel.SetText("Please enter or browse to a vault directory")
			errorLabel.Show()
			return
		}
		if newDir == ss.config.VaultDir {
			errorLabel.SetText("Already using that vault directory")
			errorLabel.Show()
			return
		}

		dialog.ShowConfirm("Switch Vault",
			fmt.Sprintf("Switch to vault at:\n  %s\n\nThe app will lock. If a vault exists there you will unlock it with its passphrase; otherwise you will be asked to create one.", newDir),
			func(confirmed bool) {
				ss.activity()
				if !confirmed {
					return
				}
				ss.switchVault(newDir, currentLabel)
			},
			ss.window,
		)
	})

	if ss.config.FromEnv.VaultDir {
		currentLabel.SetText(currentLabel.Text + " (set by " + envVaultDir + ")")
		pathEntry.Disable()
		browseBtn.Disable()
		moveBtn.Disable()
		switchBtn.Disable()
	}

	return card(
		subheading("Vault Location"),
		currentLabel,
		muted("Move copies every secret to a new folder and keeps the original. Switch opens the vault in another folder, or creates one there."),
		container.NewBorder(nil, nil, nil, browseBtn, pathEntry),
		errorLabel,
		container.NewHBox(moveBtn, switchBtn),
	)
}

func (ss *SettingsScreen) moveVault(newDir string, errorLabel, currentLabel *widget.Label) {
	ss.statusBar.SetText("Copying vault…")
	background(ss.run,
		func(ctx context.Context) (none, error) { return none{}, ss.store.CopyTo(ctx, newDir) },
		func(_ none, err error) {
			if err != nil {
				ss.statusBar.SetText("")
				errorLabel.SetText("Move failed: " + err.Error())
				errorLabel.Show()
				return
			}
			ss.vaultMoved(newDir, currentLabel)
		})
}

// vaultMoved switches the config to newDir after a successful copy and
// relocks so the user unlocks the vault at its new location.
func (ss *SettingsScreen) vaultMoved(newDir string, currentLabel *widget.Label) {
	ss.config.VaultDir = newDir
	currentLabel.SetText("Current: " + newDir)
	ss.log.Info("vault moved", "to", newDir)
	ss.statusBar.SetText("Vault moved to " + newDir)

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

func (ss *SettingsScreen) switchVault(newDir string, currentLabel *widget.Label) {
	ss.config.VaultDir = newDir
	currentLabel.SetText("Current: " + newDir)
	ss.log.Info("vault switched", "to", newDir)
	ss.statusBar.SetText("Switched to vault at " + newDir)
	ss.onRelock()
}

func (ss *SettingsScreen) buildLockTimeout() *fyne.Container {
	label := widget.NewLabel(formatLockTimeout(ss.config.LockTimeoutMs))
	slider := widget.NewSlider(MinLockTimeoutMs, MaxLockTimeoutMs)
	slider.Step = 30000
	if ss.config.LockTimeoutMs > 0 {
		slider.SetValue(float64(ss.config.LockTimeoutMs))
	} else {
		slider.SetValue(DefaultLockTimeoutMs)
		slider.Disable()
	}
	enabled := widget.NewCheck("Auto-lock", nil)
	enabled.SetChecked(ss.config.LockTimeoutMs > 0)

	apply := func(ms int) {
		ss.config.LockTimeoutMs = ms
		label.SetText(formatLockTimeout(ms))
	}
	slider.OnChanged = func(v float64) { apply(int(v)) }
	slider.OnChangeEnded = func(float64) { ss.save(prefLockTimeout) }
	enabled.OnChanged = func(on bool) {
		if on {
			slider.Enable()
			apply(int(slider.Value))
		} else {
			slider.Disable()
			apply(0)
		}
		ss.save(prefLockTimeout)
	}

	if ss.config.FromEnv.LockTimeout {
		slider.Disable()
		enabled.Disable()
		label.SetText(label.Text + " (set by " + envLockTimeout + ")")
	}
	return container.NewVBox(label, enabled, slider)
}

func (ss *SettingsScreen) buildClipboardTTL() *fyne.Container {
	label := widget.NewLabel(fmt.Sprintf("Clipboard TTL: %d seconds", ss.config.ClipboardTTLs))
	slider := widget.NewSlider(MinClipboardTTLs, MaxClipboardTTLs)
	slider.SetValue(float64(ss.config.ClipboardTTLs))
	slider.Step = 5
	slider.OnChanged = func(v float64) {
		ss.config.ClipboardTTLs = int(v)
		label.SetText(fmt.Sprintf("Clipboard TTL: %d seconds", ss.config.ClipboardTTLs))
	}
	slider.OnChangeEnded = func(float64) { ss.save(prefClipboardTTL) }

	if ss.config.FromEnv.ClipboardTTL {
		slider.Disable()
		label.SetText(label.Text + " (set by " + envClipboardTTL + ")")
	}
	return container.NewVBox(label, slider)
}
