package ui

import (
	"errors"
	"image/color"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// minPassphraseLen is the shortest passphrase accepted for a new vault,
// counted in characters (runes) after ignoring leading and trailing spaces.
// It is a floor against accidents, not a strength guarantee; the screen
// recommends five or more random words.
const minPassphraseLen = 12

// createFormWidth is the create screen's width; its height follows the
// form's minimum size so wrapped labels and the error line never clip.
const createFormWidth = 420

// Validation errors for a new vault passphrase.
var (
	errPassphraseTooShort = errors.New("passphrase must be at least 12 characters")
	errPassphraseMismatch = errors.New("passphrases do not match")
)

// validateNewPassphrase checks a new-vault passphrase and its confirmation.
func validateNewPassphrase(passphrase, confirm string) error {
	if utf8.RuneCountInString(strings.TrimSpace(passphrase)) < minPassphraseLen {
		return errPassphraseTooShort
	}
	if passphrase != confirm {
		return errPassphraseMismatch
	}
	return nil
}

// CreateScreen asks for a new vault's passphrase twice. It is shown instead of
// the unlock screen when no vault exists at the configured location, so a
// typo can never become the vault's passphrase.
type CreateScreen struct {
	onCreate   func(passphrase string)
	entry      *widget.Entry
	confirm    *widget.Entry
	createBtn  *widget.Button
	progress   *widget.ProgressBarInfinite
	errorLabel *widget.Label
	content    *fyne.Container
}

// NewCreateScreen builds the create-vault screen for vaultDir. onCreate is
// called with the passphrase once both entries match and pass validation.
func NewCreateScreen(vaultDir string, onCreate func(passphrase string)) *CreateScreen {
	s := &CreateScreen{onCreate: onCreate}

	s.entry = widget.NewPasswordEntry()
	s.entry.SetPlaceHolder("New passphrase")
	s.confirm = widget.NewPasswordEntry()
	s.confirm.SetPlaceHolder("Confirm passphrase")
	s.confirm.OnSubmitted = func(string) { s.submit() }

	s.errorLabel = widget.NewLabel("")
	s.errorLabel.Wrapping = fyne.TextWrapWord
	s.errorLabel.Hide()

	s.progress = widget.NewProgressBarInfinite()
	s.progress.Stop()
	s.progress.Hide()

	s.createBtn = widget.NewButton("Create Vault", s.submit)
	s.createBtn.Importance = widget.HighImportance

	title := widget.NewLabel("Create a new vault")
	title.Alignment = fyne.TextAlignCenter
	title.TextStyle = fyne.TextStyle{Bold: true}

	location := widget.NewLabel("No vault was found at:\n" + vaultDir)
	location.Alignment = fyne.TextAlignCenter
	location.Wrapping = fyne.TextWrapWord

	guidance := widget.NewLabel("Choose a passphrase of five or more random words. " +
		"It cannot be recovered: if you forget it, the vault cannot be opened.")
	guidance.Wrapping = fyne.TextWrapWord

	form := container.NewVBox(
		title,
		location,
		widget.NewSeparator(),
		guidance,
		s.entry,
		s.confirm,
		s.errorLabel,
		s.progress,
		s.createBtn,
	)
	width := canvas.NewRectangle(color.Transparent)
	width.SetMinSize(fyne.NewSize(createFormWidth, 0))
	s.content = container.NewCenter(container.NewStack(width, form))
	return s
}

// Content returns the create-vault screen widget.
func (s *CreateScreen) Content() *fyne.Container {
	return s.content
}

// ShowError displays an error message on the screen.
func (s *CreateScreen) ShowError(msg string) {
	s.errorLabel.SetText(msg)
	s.errorLabel.Show()
}

// submit validates the entries and hands the passphrase to onCreate. Both
// entries are cleared once read so the passphrase does not linger in them.
func (s *CreateScreen) submit() {
	passphrase, confirm := s.entry.Text, s.confirm.Text
	if err := validateNewPassphrase(passphrase, confirm); err != nil {
		s.confirm.SetText("")
		s.ShowError(err.Error())
		return
	}
	s.entry.SetText("")
	s.confirm.SetText("")
	s.errorLabel.Hide()
	s.onCreate(passphrase)
}

// SetBusy disables input and shows progress while the vault is created.
func (s *CreateScreen) SetBusy(busy bool) {
	if busy {
		s.entry.Disable()
		s.confirm.Disable()
		s.createBtn.Disable()
		s.progress.Show()
		s.progress.Start()
		return
	}
	s.progress.Stop()
	s.progress.Hide()
	s.entry.Enable()
	s.confirm.Enable()
	s.createBtn.Enable()
}
