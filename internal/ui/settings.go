package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// SettingsScreen displays vault configuration options.
type SettingsScreen struct {
	window  fyne.Window
	config  *Config
	content *fyne.Container
}

// NewSettingsScreen creates a settings panel.
func NewSettingsScreen(window fyne.Window, config *Config) *SettingsScreen {
	ss := &SettingsScreen{
		window: window,
		config: config,
	}

	vaultDirLabel := widget.NewLabel("Vault Directory:")
	vaultDirEntry := widget.NewEntry()
	vaultDirEntry.SetText(config.VaultDir)
	vaultDirEntry.Disable()

	lockTimeoutLabel := widget.NewLabel(fmt.Sprintf("Auto-lock timeout: %d ms", config.LockTimeoutMs))
	lockTimeoutSlider := widget.NewSlider(30000, 1800000)
	lockTimeoutSlider.SetValue(float64(config.LockTimeoutMs))
	lockTimeoutSlider.Step = 30000
	lockTimeoutSlider.OnChanged = func(v float64) {
		config.LockTimeoutMs = int(v)
		lockTimeoutLabel.SetText(fmt.Sprintf("Auto-lock timeout: %d ms (%.0f min)", config.LockTimeoutMs, v/60000))
	}

	clipTTLLabel := widget.NewLabel(fmt.Sprintf("Clipboard TTL: %d seconds", config.ClipboardTTLs))
	clipTTLSlider := widget.NewSlider(5, 120)
	clipTTLSlider.SetValue(float64(config.ClipboardTTLs))
	clipTTLSlider.Step = 5
	clipTTLSlider.OnChanged = func(v float64) {
		config.ClipboardTTLs = int(v)
		clipTTLLabel.SetText(fmt.Sprintf("Clipboard TTL: %d seconds", config.ClipboardTTLs))
	}

	ss.content = container.NewVBox(
		widget.NewLabel("Settings"),
		widget.NewSeparator(),
		vaultDirLabel,
		vaultDirEntry,
		widget.NewSeparator(),
		lockTimeoutLabel,
		lockTimeoutSlider,
		widget.NewSeparator(),
		clipTTLLabel,
		clipTTLSlider,
	)

	return ss
}

// Content returns the settings panel widget.
func (ss *SettingsScreen) Content() *fyne.Container {
	return ss.content
}
