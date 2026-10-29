package ui

import (
	"errors"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
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
	openBtn    *widget.Button // nil when opening another folder isn't offered
	serverBtn  *widget.Button // nil when choosing a server isn't offered
	progress   *widget.ProgressBarInfinite
	errorLabel *widget.Label
	content    *fyne.Container
}

// NewCreateScreen builds the create-vault screen for vaultDir. onCreate is
// called with the passphrase once both entries match and pass validation.
// If onOpen is not nil, an "Open Existing Vault…" button calls it, so a vault
// kept in another folder can be found again (for example after the saved
// location was lost). If onServer is not nil, a "Use a Vault on a Server…"
// button calls it.
func NewCreateScreen(vaultDir string, onCreate func(passphrase string), onOpen, onServer func()) *CreateScreen {
	s := &CreateScreen{onCreate: onCreate}

	s.entry = widget.NewPasswordEntry()
	s.entry.SetPlaceHolder("New passphrase")
	s.confirm = widget.NewPasswordEntry()
	s.confirm.SetPlaceHolder("Confirm passphrase")
	s.confirm.OnSubmitted = func(string) { s.submit() }

	s.errorLabel = errorText()

	s.progress = widget.NewProgressBarInfinite()
	s.progress.Stop()
	s.progress.Hide()

	s.createBtn = widget.NewButtonWithIcon("Create Vault", theme.ConfirmIcon(), s.submit)
	s.createBtn.Importance = widget.HighImportance

	title := heading("Create a new vault")
	title.Alignment = fyne.TextAlignCenter

	location := muted("No vault was found at:\n" + vaultDir)
	location.Alignment = fyne.TextAlignCenter

	guidance := muted("Choose a passphrase of five or more random words. " +
		"It cannot be recovered: if you forget it, the vault cannot be opened.")

	objects := []fyne.CanvasObject{
		container.NewCenter(brandMark(64)),
		title,
		location,
		widget.NewSeparator(),
		guidance,
		s.entry,
		s.confirm,
		s.errorLabel,
		s.progress,
		s.createBtn,
	}
	if onOpen != nil || onServer != nil {
		hint := caption("Already have a vault in another folder or on a server?")
		hint.Alignment = fyne.TextAlignCenter
		objects = append(objects, widget.NewSeparator(), hint)
	}
	if onOpen != nil {
		s.openBtn = widget.NewButtonWithIcon("Open Existing Vault…", theme.FolderOpenIcon(), onOpen)
		s.openBtn.Importance = widget.LowImportance
		objects = append(objects, s.openBtn)
	}
	if onServer != nil {
		s.serverBtn = widget.NewButtonWithIcon("Use a Vault on a Server…", theme.StorageIcon(), onServer)
		s.serverBtn.Importance = widget.LowImportance
		objects = append(objects, s.serverBtn)
	}
	s.content = centeredCard(createFormWidth, objects...)
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
		for _, b := range []*widget.Button{s.openBtn, s.serverBtn} {
			if b != nil {
				b.Disable()
			}
		}
		s.progress.Show()
		s.progress.Start()
		return
	}
	s.progress.Stop()
	s.progress.Hide()
	s.entry.Enable()
	s.confirm.Enable()
	s.createBtn.Enable()
	for _, b := range []*widget.Button{s.openBtn, s.serverBtn} {
		if b != nil {
			b.Enable()
		}
	}
}
