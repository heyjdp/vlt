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
	progress   *widget.ProgressBarInfinite
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

	s.progress = widget.NewProgressBarInfinite()
	s.progress.Stop()
	s.progress.Hide()

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
		s.progress,
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

// submit hands the passphrase to onUnlock. The entry is cleared as soon as
// it is read (SetText also clears the entry's undo history), so the
// passphrase does not stay in the widget while the vault is open.
func (s *UnlockScreen) submit() {
	passphrase := s.entry.Text
	s.entry.SetText("")
	if passphrase == "" {
		s.ShowError("Passphrase cannot be empty")
		return
	}
	s.errorLabel.Hide()
	s.onUnlock(passphrase)
}

// SetBusy disables input and shows progress while the vault is being opened
// (scrypt takes about a second; migrating an old vault takes longer).
func (s *UnlockScreen) SetBusy(busy bool) {
	if busy {
		s.entry.Disable()
		s.unlockBtn.Disable()
		s.errorLabel.Hide()
		s.progress.Show()
		s.progress.Start()
		return
	}
	s.progress.Stop()
	s.progress.Hide()
	s.entry.Enable()
	s.unlockBtn.Enable()
}
