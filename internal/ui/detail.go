package ui

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
)

// DetailView displays and edits a secret's details.
type DetailView struct {
	window       fyne.Window
	store        *store.Store
	clipboardTTL int
	statusBar    *widget.Label
	log          *slog.Logger
	onSave       func()

	content *fyne.Container
	current *store.Secret
	fileOps *FileOps
}

// NewDetailView creates a new detail panel.
func NewDetailView(window fyne.Window, s *store.Store, clipboardTTL int, statusBar *widget.Label, log *slog.Logger) *DetailView {
	dv := &DetailView{
		window:       window,
		store:        s,
		clipboardTTL: clipboardTTL,
		statusBar:    statusBar,
		log:          log,
	}

	dv.content = container.NewVBox(
		widget.NewLabel("Select a secret to view details"),
	)

	return dv
}

// Content returns the detail panel widget.
func (dv *DetailView) Content() *fyne.Container {
	return dv.content
}

// SetOnSave sets a callback invoked after a successful save.
func (dv *DetailView) SetOnSave(fn func()) {
	dv.onSave = fn
}

// SetFileOps sets the file operations handler for import/export.
func (dv *DetailView) SetFileOps(fo *FileOps) {
	dv.fileOps = fo
}

// ShowSecret renders the detail view for a secret.
func (dv *DetailView) ShowSecret(secret *store.Secret) {
	dv.current = secret

	switch secret.Type {
	case store.TypePassword:
		dv.showPasswordDetail(secret, false)
	case store.TypeRecord:
		dv.showRecordDetail(secret, false)
	case store.TypeFile:
		dv.showFileDetail(secret)
	default:
		dv.content.Objects = []fyne.CanvasObject{
			widget.NewLabel("Unknown secret type: " + string(secret.Type)),
		}
		dv.content.Refresh()
	}
}

// ShowNewPassword shows the form for creating a new password secret.
func (dv *DetailView) ShowNewPassword() {
	now := time.Now().UTC()
	secret := &store.Secret{
		Version:    1,
		Type:       store.TypePassword,
		CreatedAt:  now,
		ModifiedAt: now,
	}
	dv.showPasswordDetail(secret, true)
}

// ShowNewRecord shows the form for creating a new record secret.
func (dv *DetailView) ShowNewRecord() {
	now := time.Now().UTC()
	secret := &store.Secret{
		Version:    1,
		Type:       store.TypeRecord,
		CreatedAt:  now,
		ModifiedAt: now,
	}
	dv.showRecordDetail(secret, true)
}

// groupSelect creates a group selector that shows existing groups as a dropdown
// and allows typing a new group name.
func (dv *DetailView) groupSelect(current string) *widget.SelectEntry {
	groups := dv.store.Groups()
	sel := widget.NewSelectEntry(groups)
	sel.SetPlaceHolder("Group (e.g. work, personal)")
	if current != "" {
		sel.SetText(current)
	}
	return sel
}

// showPasswordDetail renders a password secret form.
func (dv *DetailView) showPasswordDetail(secret *store.Secret, isNew bool) {
	nameEntry := widget.NewEntry()
	nameEntry.SetText(secret.Name)
	if !isNew {
		nameEntry.Disable()
	}

	groupEntry := dv.groupSelect(secret.Group)
	if !isNew {
		groupEntry.Disable()
	}

	valueEntry := widget.NewPasswordEntry()
	valueEntry.SetText(secret.Value)

	tagsEntry := widget.NewEntry()
	tagsEntry.SetPlaceHolder("comma-separated tags")
	if len(secret.Tags) > 0 {
		tagsEntry.SetText(joinTags(secret.Tags))
	}

	notesEntry := widget.NewMultiLineEntry()
	notesEntry.SetText(secret.Notes)
	notesEntry.SetMinRowsVisible(3)

	copyBtn := widget.NewButton("Copy Password", func() {
		dv.copyToClipboard(secret.Value)
	})
	if isNew {
		copyBtn.Disable()
	}

	saveBtn := widget.NewButton("Save", func() {
		secret.Name = nameEntry.Text
		secret.Group = groupEntry.Text
		secret.Value = valueEntry.Text
		secret.Tags = parseTags(tagsEntry.Text)
		secret.Notes = notesEntry.Text
		dv.saveSecret(secret)
	})
	saveBtn.Importance = widget.HighImportance

	var deleteBtn *widget.Button
	if !isNew {
		deleteBtn = widget.NewButton("Delete", func() {
			dv.confirmDelete(secret)
		})
		deleteBtn.Importance = widget.DangerImportance
	}

	objects := []fyne.CanvasObject{
		widget.NewLabel("Password Secret"),
		widget.NewSeparator(),
		widget.NewLabel("Name:"),
		nameEntry,
		widget.NewLabel("Group:"),
		groupEntry,
		widget.NewLabel("Password / Value:"),
		valueEntry,
		widget.NewLabel("Tags:"),
		tagsEntry,
		widget.NewLabel("Notes:"),
		notesEntry,
		container.NewHBox(saveBtn, copyBtn),
	}
	if deleteBtn != nil {
		objects = append(objects, deleteBtn)
	}
	if !isNew {
		objects = append(objects,
			widget.NewSeparator(),
			widget.NewLabel(fmt.Sprintf("Created: %s", secret.CreatedAt.Format(time.RFC3339))),
			widget.NewLabel(fmt.Sprintf("Modified: %s", secret.ModifiedAt.Format(time.RFC3339))),
		)
	}

	dv.content.Objects = objects
	dv.content.Refresh()
}

// showRecordDetail renders a record secret form.
func (dv *DetailView) showRecordDetail(secret *store.Secret, isNew bool) {
	nameEntry := widget.NewEntry()
	nameEntry.SetText(secret.Name)
	if !isNew {
		nameEntry.Disable()
	}

	groupEntry := dv.groupSelect(secret.Group)
	if !isNew {
		groupEntry.Disable()
	}

	usernameEntry := widget.NewEntry()
	usernameEntry.SetText(secret.Username)

	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetText(secret.Password)

	urlEntry := widget.NewEntry()
	urlEntry.SetText(secret.URL)

	totpEntry := widget.NewEntry()
	totpEntry.SetText(secret.TOTPSecret)
	totpEntry.SetPlaceHolder("TOTP secret (optional)")

	tagsEntry := widget.NewEntry()
	tagsEntry.SetPlaceHolder("comma-separated tags")
	if len(secret.Tags) > 0 {
		tagsEntry.SetText(joinTags(secret.Tags))
	}

	notesEntry := widget.NewMultiLineEntry()
	notesEntry.SetText(secret.Notes)
	notesEntry.SetMinRowsVisible(3)

	copyPassBtn := widget.NewButton("Copy Password", func() {
		dv.copyToClipboard(secret.Password)
	})
	copyUserBtn := widget.NewButton("Copy Username", func() {
		dv.copyToClipboard(secret.Username)
	})
	if isNew {
		copyPassBtn.Disable()
		copyUserBtn.Disable()
	}

	saveBtn := widget.NewButton("Save", func() {
		secret.Name = nameEntry.Text
		secret.Group = groupEntry.Text
		secret.Username = usernameEntry.Text
		secret.Password = passwordEntry.Text
		secret.URL = urlEntry.Text
		secret.TOTPSecret = totpEntry.Text
		secret.Tags = parseTags(tagsEntry.Text)
		secret.Notes = notesEntry.Text
		dv.saveSecret(secret)
	})
	saveBtn.Importance = widget.HighImportance

	var deleteBtn *widget.Button
	if !isNew {
		deleteBtn = widget.NewButton("Delete", func() {
			dv.confirmDelete(secret)
		})
		deleteBtn.Importance = widget.DangerImportance
	}

	objects := []fyne.CanvasObject{
		widget.NewLabel("Record Secret"),
		widget.NewSeparator(),
		widget.NewLabel("Name:"),
		nameEntry,
		widget.NewLabel("Group:"),
		groupEntry,
		widget.NewLabel("URL:"),
		urlEntry,
		widget.NewLabel("Username:"),
		usernameEntry,
		widget.NewLabel("Password:"),
		passwordEntry,
		widget.NewLabel("TOTP Secret:"),
		totpEntry,
		widget.NewLabel("Tags:"),
		tagsEntry,
		widget.NewLabel("Notes:"),
		notesEntry,
		container.NewHBox(saveBtn, copyPassBtn, copyUserBtn),
	}
	if deleteBtn != nil {
		objects = append(objects, deleteBtn)
	}
	if !isNew {
		objects = append(objects,
			widget.NewSeparator(),
			widget.NewLabel(fmt.Sprintf("Created: %s", secret.CreatedAt.Format(time.RFC3339))),
			widget.NewLabel(fmt.Sprintf("Modified: %s", secret.ModifiedAt.Format(time.RFC3339))),
		)
	}

	dv.content.Objects = objects
	dv.content.Refresh()
}

// showFileDetail renders a file secret detail view.
func (dv *DetailView) showFileDetail(secret *store.Secret) {
	exportBtn := widget.NewButton("Export File", func() {
		dv.exportFile(secret)
	})

	deleteBtn := widget.NewButton("Delete", func() {
		dv.confirmDelete(secret)
	})
	deleteBtn.Importance = widget.DangerImportance

	dv.content.Objects = []fyne.CanvasObject{
		widget.NewLabel("File Secret"),
		widget.NewSeparator(),
		widget.NewLabel("Name: " + secret.Name),
		widget.NewLabel("Group: " + secret.Group),
		widget.NewLabel("Original Filename: " + secret.OriginalFilename),
		widget.NewLabel("MIME Type: " + secret.MIMEType),
		widget.NewLabel(fmt.Sprintf("Created: %s", secret.CreatedAt.Format(time.RFC3339))),
		widget.NewLabel(fmt.Sprintf("Modified: %s", secret.ModifiedAt.Format(time.RFC3339))),
		container.NewHBox(exportBtn, deleteBtn),
	}
	dv.content.Refresh()
}

// saveSecret encrypts and saves a secret, then refreshes the UI.
func (dv *DetailView) saveSecret(secret *store.Secret) {
	ctx := context.Background()

	if err := dv.store.Save(ctx, secret); err != nil {
		dialog.ShowError(fmt.Errorf("save failed: %w", err), dv.window)
		return
	}

	dv.log.Info("secret saved via UI", "name", secret.Name)
	dv.statusBar.SetText("Saved: " + secret.Name)

	if dv.onSave != nil {
		dv.onSave()
	}
}

// confirmDelete shows a confirmation dialog before deleting a secret.
func (dv *DetailView) confirmDelete(secret *store.Secret) {
	dialog.ShowConfirm(
		"Delete Secret",
		fmt.Sprintf("Are you sure you want to delete %q?", secret.Name),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			ctx := context.Background()
			if err := dv.store.Delete(ctx, secret.Name, secret.Group); err != nil {
				dialog.ShowError(fmt.Errorf("delete failed: %w", err), dv.window)
				return
			}
			dv.log.Info("secret deleted via UI", "name", secret.Name)
			dv.statusBar.SetText("Deleted: " + secret.Name)
			dv.content.Objects = []fyne.CanvasObject{
				widget.NewLabel("Secret deleted"),
			}
			dv.content.Refresh()
			if dv.onSave != nil {
				dv.onSave()
			}
		},
		dv.window,
	)
}

// exportFile exports a file secret to disk via the file operations handler.
func (dv *DetailView) exportFile(secret *store.Secret) {
	if dv.fileOps == nil {
		dialog.ShowError(fmt.Errorf("file operations not available"), dv.window)
		return
	}
	dv.fileOps.ExportFile(secret)
}

// copyToClipboard copies a value to the clipboard with TTL countdown.
func (dv *DetailView) copyToClipboard(value string) {
	dv.window.Clipboard().SetContent(value)
	ttl := dv.clipboardTTL

	dv.statusBar.SetText(fmt.Sprintf("Copied to clipboard (clearing in %ds)", ttl))

	go func() {
		for i := ttl; i > 0; i-- {
			time.Sleep(1 * time.Second)
			fyne.Do(func() {
				dv.statusBar.SetText(fmt.Sprintf("Clipboard clears in %ds", i-1))
			})
		}
		fyne.Do(func() {
			dv.window.Clipboard().SetContent("")
			dv.statusBar.SetText("Clipboard cleared")
		})
	}()
}

// parseTags splits a comma-separated string into tags.
func parseTags(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	for _, t := range splitAndTrim(s, ",") {
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// joinTags joins tags with ", ".
func joinTags(tags []string) string {
	result := ""
	for i, t := range tags {
		if i > 0 {
			result += ", "
		}
		result += t
	}
	return result
}

// splitAndTrim splits a string by sep and trims each part.
func splitAndTrim(s, sep string) []string {
	parts := make([]string, 0)
	for _, p := range split(s, sep) {
		p = trimSpace(p)
		parts = append(parts, p)
	}
	return parts
}

// split is a simple string split.
func split(s, sep string) []string {
	var result []string
	for {
		i := indexOf(s, sep)
		if i < 0 {
			result = append(result, s)
			break
		}
		result = append(result, s[:i])
		s = s[i+len(sep):]
	}
	return result
}

// indexOf returns the index of sep in s, or -1.
func indexOf(s, sep string) int {
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

// trimSpace removes leading and trailing whitespace.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
