package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
)

// DetailView displays and edits a secret's details. Its methods must be
// called on the Fyne main goroutine; store calls run in the background.
type DetailView struct {
	window fyne.Window
	store  *store.Store
	// copyValue copies a secret value to the clipboard (with auto-clear).
	copyValue func(string)
	statusBar *widget.Label
	log       *slog.Logger
	run       runner
	groups    func() []string
	onSaved   func(*store.Secret)
	onDeleted func(*store.Secret)

	content *fyne.Container
	// current is the existing secret shown in the panel, or nil when the
	// panel shows a new-secret form, a message, or settings.
	current *store.Secret
	// busy is true while a save or delete is in flight; a second one is
	// refused so they cannot interleave.
	busy bool
	// onRender, if set, is called with the panel after each render (the app
	// uses it to track user activity in newly built forms).
	onRender func(fyne.CanvasObject)
	// activity reports user input made in dialogs, which are outside the
	// instrumented widget tree.
	activity func()
	fileOps  *FileOps
}

// NewDetailView creates a new detail panel. copyValue puts a secret value on
// the clipboard; the app's clipboardClearer clears it after the TTL.
func NewDetailView(window fyne.Window, s *store.Store, copyValue func(string), statusBar *widget.Label, log *slog.Logger, run runner) *DetailView {
	dv := &DetailView{
		window:    window,
		store:     s,
		copyValue: copyValue,
		statusBar: statusBar,
		log:       log,
		run:       run,
		groups:    func() []string { return nil },
		activity:  noActivity,
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

// SetCallbacks sets the functions called after a secret is saved or deleted
// (used to update the list) and the source of group suggestions. They are
// set once, when the main view is built, so every save and delete, whether
// of a new secret or one selected from the list, reaches the list.
func (dv *DetailView) SetCallbacks(onSaved, onDeleted func(*store.Secret), groups func() []string) {
	dv.onSaved, dv.onDeleted, dv.groups = onSaved, onDeleted, groups
}

// ShowContent replaces the panel with obj (for example the settings screen).
func (dv *DetailView) ShowContent(obj fyne.CanvasObject) {
	dv.current = nil
	dv.setObjects(obj)
}

// setObjects replaces the panel's contents and reports the render.
func (dv *DetailView) setObjects(objs ...fyne.CanvasObject) {
	dv.content.Objects = objs
	dv.content.Refresh()
	if dv.onRender != nil {
		dv.onRender(dv.content)
	}
}

// RefreshIfShowing re-renders the panel when it shows the secret stored at
// the same path as secret, so a form never edits a stale copy (for example
// after an import replaced it).
func (dv *DetailView) RefreshIfShowing(secret *store.Secret) {
	if dv.current != nil && sameSecret(dv.current, secret) {
		dv.ShowSecret(secret)
	}
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
		dv.setObjects(widget.NewLabel("Unknown secret type: " + string(secret.Type)))
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
	dv.current = nil
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
	dv.current = nil
	dv.showRecordDetail(secret, true)
}

// groupSelect creates a group selector that shows existing groups as a dropdown
// and allows typing a new group name.
func (dv *DetailView) groupSelect(current string) *widget.SelectEntry {
	sel := widget.NewSelectEntry(dv.groups())
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
		dv.copyValue(secret.Value)
	})
	if isNew {
		copyBtn.Disable()
	}

	var saveBtn *widget.Button
	saveBtn = widget.NewButton("Save", func() {
		edited := *secret
		edited.Name = nameEntry.Text
		edited.Group = groupEntry.Text
		edited.Value = valueEntry.Text
		edited.Tags = parseTags(tagsEntry.Text)
		edited.Notes = notesEntry.Text
		dv.saveSecret(&edited, isNew, saveBtn, func() { *secret = edited })
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

	dv.setObjects(objects...)
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
		dv.copyValue(secret.Password)
	})
	copyUserBtn := widget.NewButton("Copy Username", func() {
		dv.copyValue(secret.Username)
	})
	if isNew {
		copyPassBtn.Disable()
		copyUserBtn.Disable()
	}

	var saveBtn *widget.Button
	saveBtn = widget.NewButton("Save", func() {
		edited := *secret
		edited.Name = nameEntry.Text
		edited.Group = groupEntry.Text
		edited.Username = usernameEntry.Text
		edited.Password = passwordEntry.Text
		edited.URL = urlEntry.Text
		edited.TOTPSecret = totpEntry.Text
		edited.Tags = parseTags(tagsEntry.Text)
		edited.Notes = notesEntry.Text
		dv.saveSecret(&edited, isNew, saveBtn, func() { *secret = edited })
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

	dv.setObjects(objects...)
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

	dv.setObjects(
		widget.NewLabel("File Secret"),
		widget.NewSeparator(),
		widget.NewLabel("Name: "+secret.Name),
		widget.NewLabel("Group: "+secret.Group),
		widget.NewLabel("Original Filename: "+secret.OriginalFilename),
		widget.NewLabel("MIME Type: "+secret.MIMEType),
		widget.NewLabel(fmt.Sprintf("Created: %s", secret.CreatedAt.Format(time.RFC3339))),
		widget.NewLabel(fmt.Sprintf("Modified: %s", secret.ModifiedAt.Format(time.RFC3339))),
		container.NewHBox(exportBtn, deleteBtn),
	)
}

// saveSecret creates (isNew) or updates a secret in the background. On
// success it calls commit on the main goroutine (callers pass an edited copy
// and commit it to the shared *Secret only then, so a failed save leaves the
// list's copy unchanged), updates the list, and for a new secret re-renders
// the form as existing so a second Save updates it. btn is disabled while the
// save is in flight so a double click cannot submit twice. Creating never
// overwrites: a name already used in the group, including one that differs
// only in case or punctuation, is reported.
func (dv *DetailView) saveSecret(secret *store.Secret, isNew bool, btn *widget.Button, commit func()) {
	if dv.busy {
		dv.statusBar.SetText("Please wait for the current operation to finish")
		return
	}
	dv.busy = true
	dv.statusBar.SetText("Saving…")
	btn.Disable()
	background(dv.run,
		func(ctx context.Context) (none, error) {
			if isNew {
				return none{}, dv.store.Create(ctx, secret)
			}
			return none{}, dv.store.Update(ctx, secret)
		},
		func(_ none, err error) {
			dv.busy = false
			btn.Enable()
			switch {
			case errors.Is(err, store.ErrSecretExists) && isNew:
				dv.statusBar.SetText("")
				dialog.ShowError(fmt.Errorf("a secret named %q (or one whose name differs only in case or punctuation) already exists in group %q; choose a different name",
					secret.Name, store.NormalizeGroup(secret.Group)), dv.window)
				return
			case errors.Is(err, store.ErrSecretExists):
				dv.statusBar.SetText("")
				dialog.ShowError(fmt.Errorf("save failed: this secret's file is now held by a different secret: %w", err), dv.window)
				return
			case err != nil:
				dv.statusBar.SetText("")
				dialog.ShowError(fmt.Errorf("save failed: %w", err), dv.window)
				return
			}

			commit()
			dv.log.Info("secret saved via UI", "name", secret.Name, "created", isNew)
			dv.statusBar.SetText("Saved: " + secret.Name)
			if dv.onSaved != nil {
				dv.onSaved(secret)
			}
			if isNew {
				dv.ShowSecret(secret)
			}
		})
}

// confirmDelete asks for confirmation, then deletes the secret.
func (dv *DetailView) confirmDelete(secret *store.Secret) {
	dialog.ShowConfirm(
		"Delete Secret",
		fmt.Sprintf("Are you sure you want to delete %q?", secret.Name),
		func(confirmed bool) {
			dv.activity()
			if !confirmed {
				return
			}
			dv.deleteSecret(secret)
		},
		dv.window,
	)
}

// deleteSecret deletes secret in the background, then removes it from the
// list and, if the panel still shows it, clears the panel. Name and group are
// snapshotted first: the list's *Secret may be rewritten by a save
// completing meanwhile.
func (dv *DetailView) deleteSecret(secret *store.Secret) {
	if dv.busy {
		dv.statusBar.SetText("Please wait for the current operation to finish")
		return
	}
	dv.busy = true
	deleted := &store.Secret{Name: secret.Name, Group: secret.Group}
	background(dv.run,
		func(ctx context.Context) (none, error) {
			return none{}, dv.store.Delete(ctx, deleted.Name, deleted.Group)
		},
		func(_ none, err error) {
			dv.busy = false
			if err != nil {
				dialog.ShowError(fmt.Errorf("delete failed: %w", err), dv.window)
				return
			}
			dv.log.Info("secret deleted via UI", "name", deleted.Name)
			dv.statusBar.SetText("Deleted: " + deleted.Name)
			// Only clear the panel if it still shows the deleted secret; the
			// user may have moved on while the delete ran.
			if dv.current != nil && sameSecret(dv.current, deleted) {
				dv.current = nil
				dv.setObjects(widget.NewLabel("Secret deleted"))
			}
			if dv.onDeleted != nil {
				dv.onDeleted(deleted)
			}
		})
}

// exportFile exports a file secret to disk via the file operations handler.
func (dv *DetailView) exportFile(secret *store.Secret) {
	if dv.fileOps == nil {
		dialog.ShowError(fmt.Errorf("file operations not available"), dv.window)
		return
	}
	dv.fileOps.ExportFile(secret)
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
