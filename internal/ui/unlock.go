package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// UnlockScreen handles passphrase entry for vault unlock.
type UnlockScreen struct {
	window     fyne.Window
	onUnlock   func(passphrase string)
	entry      *widget.Entry
	unlockBtn  *widget.Button
	errorLabel *widget.Label
	content    *fyne.Container
}

// NewUnlockScreen creates a passphrase entry screen.
func NewUnlockScreen(window fyne.Window, onUnlock func(passphrase string)) *UnlockScreen {
	s := &UnlockScreen{
		window:   window,
		onUnlock: onUnlock,
	}

	s.entry = widget.NewPasswordEntry()
	s.entry.SetPlaceHolder("Enter vault passphrase...")
	s.entry.OnSubmitted = func(_ string) {
		s.submit()
	}

	s.errorLabel = widget.NewLabel("")
	s.errorLabel.Hide()

	s.unlockBtn = widget.NewButton("Unlock", func() {
		s.submit()
	})
	s.unlockBtn.Importance = widget.HighImportance

	title := widget.NewLabel("vlt — Secret Manager")
	title.Alignment = fyne.TextAlignCenter
	title.TextStyle = fyne.TextStyle{Bold: true}

	subtitle := widget.NewLabel("Enter your passphrase to unlock the vault")
	subtitle.Alignment = fyne.TextAlignCenter

	form := container.NewVBox(
		title,
		subtitle,
		widget.NewSeparator(),
		s.entry,
		s.errorLabel,
		s.unlockBtn,
	)

	s.content = container.NewCenter(form)

	return s
}

// Content returns the unlock screen widget.
func (s *UnlockScreen) Content() *fyne.Container {
	return s.content
}

// ShowError displays an error message on the unlock screen.
func (s *UnlockScreen) ShowError(msg string) {
	s.errorLabel.SetText(msg)
	s.errorLabel.Show()
}

// PassphraseEntry returns the passphrase entry widget (for testing).
func (s *UnlockScreen) PassphraseEntry() *widget.Entry {
	return s.entry
}

// submit handles the unlock button press.
func (s *UnlockScreen) submit() {
	passphrase := s.entry.Text
	if passphrase == "" {
		s.ShowError("Passphrase cannot be empty")
		return
	}
	s.errorLabel.Hide()
	s.onUnlock(passphrase)
}
