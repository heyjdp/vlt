package ui

import (
	"errors"
	"fmt"
	"log/slog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
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
	onRelock  func()

	content *fyne.Container
}

// NewSettingsScreen creates a settings panel.
func NewSettingsScreen(
	window fyne.Window,
	config *Config,
	s *store.Store,
	statusBar *widget.Label,
	log *slog.Logger,
	onRelock func(),
) *SettingsScreen {
	ss := &SettingsScreen{
		window:    window,
		config:    config,
		store:     s,
		log:       log,
		statusBar: statusBar,
		onRelock:  onRelock,
	}

	ss.content = ss.build()
	return ss
}

// Content returns the settings panel widget.
func (ss *SettingsScreen) Content() *fyne.Container {
	return ss.content
}

func (ss *SettingsScreen) build() *fyne.Container {
	title := widget.NewLabel("Settings")
	title.TextStyle = fyne.TextStyle{Bold: true}

	// --- Change Passphrase ---
	changePassSection := ss.buildChangePassphrase()

	// --- Vault Location ---
	vaultLocSection := ss.buildVaultLocation()

	// --- Auto-lock ---
	lockSection := ss.buildLockTimeout()

	// --- Clipboard TTL ---
	clipSection := ss.buildClipboardTTL()

	return container.NewVBox(
		title,
		widget.NewSeparator(),
		changePassSection,
		widget.NewSeparator(),
		vaultLocSection,
		widget.NewSeparator(),
		lockSection,
		widget.NewSeparator(),
		clipSection,
	)
}

func (ss *SettingsScreen) buildChangePassphrase() *fyne.Container {
	sectionLabel := widget.NewLabel("Change Passphrase")
	sectionLabel.TextStyle = fyne.TextStyle{Bold: true}

	currentEntry := widget.NewPasswordEntry()
	currentEntry.SetPlaceHolder("Current passphrase")

	newEntry := widget.NewPasswordEntry()
	newEntry.SetPlaceHolder("New passphrase")

	confirmEntry := widget.NewPasswordEntry()
	confirmEntry.SetPlaceHolder("Confirm new passphrase")

	errorLabel := widget.NewLabel("")
	errorLabel.Hide()

	changeBtn := widget.NewButton("Change Passphrase", func() {
		errorLabel.Hide()

		current := currentEntry.Text
		newPass := newEntry.Text
		confirm := confirmEntry.Text

		if current == "" || newPass == "" || confirm == "" {
			errorLabel.SetText("All fields are required")
			errorLabel.Show()
			return
		}
		if newPass != confirm {
			errorLabel.SetText("New passphrase and confirmation do not match")
			errorLabel.Show()
			return
		}
		if newPass == current {
			errorLabel.SetText("New passphrase must be different from current")
			errorLabel.Show()
			return
		}

		// Only vlt.age (the wrapped vault key) is rewritten; secrets are untouched.
		if err := ss.store.ChangePassphrase(current, newPass); err != nil {
			if errors.Is(err, store.ErrWrongPassphrase) {
				errorLabel.SetText("Current passphrase is incorrect")
			} else {
				errorLabel.SetText("Passphrase change failed: " + err.Error())
			}
			errorLabel.Show()
			return
		}

		// Clear fields
		currentEntry.SetText("")
		newEntry.SetText("")
		confirmEntry.SetText("")

		ss.statusBar.SetText("Passphrase changed")

		// Lock and force re-auth with the new passphrase
		dialog.ShowInformation("Passphrase Changed",
			"Your vault key is now protected by the new passphrase.\nThe vault will lock; unlock it with the new passphrase.",
			ss.window,
		)
		ss.onRelock()
	})
	changeBtn.Importance = widget.HighImportance

	return container.NewVBox(
		sectionLabel,
		widget.NewLabel("Current Passphrase:"),
		currentEntry,
		widget.NewLabel("New Passphrase:"),
		newEntry,
		widget.NewLabel("Confirm New Passphrase:"),
		confirmEntry,
		errorLabel,
		changeBtn,
	)
}

func (ss *SettingsScreen) buildVaultLocation() *fyne.Container {
	sectionLabel := widget.NewLabel("Vault Location")
	sectionLabel.TextStyle = fyne.TextStyle{Bold: true}

	currentLabel := widget.NewLabel("Current: " + ss.config.VaultDir)

	pathEntry := widget.NewEntry()
	pathEntry.SetPlaceHolder("New vault directory path")

	errorLabel := widget.NewLabel("")
	errorLabel.Hide()

	browseBtn := widget.NewButton("Browse...", func() {
		fd := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
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
				if !confirmed {
					return
				}
				ss.switchVault(newDir, currentLabel)
			},
			ss.window,
		)
	})

	return container.NewVBox(
		sectionLabel,
		currentLabel,
		widget.NewLabel("New Location:"),
		container.NewBorder(nil, nil, nil, browseBtn, pathEntry),
		errorLabel,
		container.NewHBox(moveBtn, switchBtn),
	)
}

func (ss *SettingsScreen) moveVault(newDir string, errorLabel *widget.Label, currentLabel *widget.Label) {
	if err := ss.store.CopyTo(newDir); err != nil {
		errorLabel.SetText("Move failed: " + err.Error())
		errorLabel.Show()
		return
	}

	ss.config.VaultDir = newDir
	ss.store.SetDir(newDir)
	currentLabel.SetText("Current: " + newDir)
	ss.log.Info("vault moved", "to", newDir)
	ss.statusBar.SetText("Vault moved to " + newDir)

	dialog.ShowInformation("Vault Moved",
		fmt.Sprintf("Secrets copied to:\n  %s\n\nYou will be locked and must re-enter your passphrase.", newDir),
		ss.window,
	)
	ss.onRelock()
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
	slider := widget.NewSlider(30000, 1800000)
	slider.SetValue(float64(ss.config.LockTimeoutMs))
	slider.Step = 30000
	slider.OnChanged = func(v float64) {
		ss.config.LockTimeoutMs = int(v)
		label.SetText(formatLockTimeout(ss.config.LockTimeoutMs))
	}

	return container.NewVBox(label, slider)
}

func (ss *SettingsScreen) buildClipboardTTL() *fyne.Container {
	label := widget.NewLabel(fmt.Sprintf("Clipboard TTL: %d seconds", ss.config.ClipboardTTLs))
	slider := widget.NewSlider(5, 120)
	slider.SetValue(float64(ss.config.ClipboardTTLs))
	slider.Step = 5
	slider.OnChanged = func(v float64) {
		ss.config.ClipboardTTLs = int(v)
		label.SetText(fmt.Sprintf("Clipboard TTL: %d seconds", ss.config.ClipboardTTLs))
	}

	return container.NewVBox(label, slider)
}

func formatLockTimeout(ms int) string {
	min := float64(ms) / 60000
	return fmt.Sprintf("Auto-lock timeout: %.0f min", min)
}
