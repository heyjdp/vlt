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
	// panel is content in a scroll container, as placed in the window.
	panel *container.Scroll
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

	dv.content = container.NewVBox(nothingSelected())
	dv.panel = container.NewVScroll(padded(dv.content, 16))

	return dv
}

// Content returns the container holding the panel's current contents.
func (dv *DetailView) Content() *fyne.Container {
	return dv.content
}

// Panel returns the detail panel as placed in the window: Content in a
// vertical scroll, so long forms never clip.
func (dv *DetailView) Panel() fyne.CanvasObject {
	return dv.panel
}

// nothingSelected is the panel's contents before a secret is chosen.
func nothingSelected() fyne.CanvasObject {
	return emptyState(lockIcon, "Select a secret",
		"Choose a secret from the list, or add one with the buttons above.")
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
	dv.panel.ScrollToTop()
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
		dv.setObjects(emptyState(theme.QuestionIcon(), "Unknown secret type", string(secret.Type)))
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

// formField is a type-specific field in a secret form: its label, its
// entry, and how its text is stored on the secret when saving.
type formField struct {
	label string
	entry *widget.Entry
	store func(edited *store.Secret, text string)
}

// copyAction is a "Copy …" button that copies one of the secret's values.
type copyAction struct {
	label string
	value func() string
}

// newEntry returns an entry holding text; password entries mask it.
func newEntry(text string, password bool) *widget.Entry {
	var e *widget.Entry
	if password {
		e = widget.NewPasswordEntry()
	} else {
		e = widget.NewEntry()
	}
	e.SetText(text)
	return e
}

// showPasswordDetail renders a password secret form.
func (dv *DetailView) showPasswordDetail(secret *store.Secret, isNew bool) {
	dv.showForm(secret, isNew,
		[]formField{
			{"Password", newEntry(secret.Value, true), func(s *store.Secret, t string) { s.Value = t }},
		},
		[]copyAction{
			{"Copy Password", func() string { return secret.Value }},
		})
}

// showRecordDetail renders a record secret form.
func (dv *DetailView) showRecordDetail(secret *store.Secret, isNew bool) {
	totp := newEntry(secret.TOTPSecret, true)
	totp.SetPlaceHolder("TOTP secret (optional)")
	dv.showForm(secret, isNew,
		[]formField{
			{"URL", newEntry(secret.URL, false), func(s *store.Secret, t string) { s.URL = t }},
			{"Username", newEntry(secret.Username, false), func(s *store.Secret, t string) { s.Username = t }},
			{"Password", newEntry(secret.Password, true), func(s *store.Secret, t string) { s.Password = t }},
			{"TOTP secret", totp, func(s *store.Secret, t string) { s.TOTPSecret = t }},
		},
		[]copyAction{
			{"Copy Password", func() string { return secret.Password }},
			{"Copy Username", func() string { return secret.Username }},
		})
}

// showForm renders a secret form: name and group (read-only for existing
// secrets), the type-specific fields, tags and notes, Save and copy buttons,
// Delete and timestamps for existing secrets.
func (dv *DetailView) showForm(secret *store.Secret, isNew bool, fields []formField, copies []copyAction) {
	nameEntry := newEntry(secret.Name, false)
	groupEntry := dv.groupSelect(secret.Group)
	if !isNew {
		nameEntry.Disable()
		groupEntry.Disable()
	}
	tagsEntry := newEntry(strings.Join(secret.Tags, ", "), false)
	tagsEntry.SetPlaceHolder("comma-separated tags")
	notesEntry := widget.NewMultiLineEntry()
	notesEntry.SetText(secret.Notes)
	notesEntry.SetMinRowsVisible(3)

	var saveBtn *widget.Button
	saveBtn = widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), func() {
		edited := *secret
		edited.Name = nameEntry.Text
		edited.Group = groupEntry.Text
		if isNew {
			edited.Group = matchGroup(edited.Group, dv.groups())
		}
		for _, f := range fields {
			f.store(&edited, f.entry.Text)
		}
		edited.Tags = parseTags(tagsEntry.Text)
		edited.Notes = notesEntry.Text
		dv.saveSecret(&edited, isNew, saveBtn, func() { *secret = edited })
	})
	saveBtn.Importance = widget.HighImportance

	form := container.New(layout.NewFormLayout(),
		fieldLabel("Name"), nameEntry,
		fieldLabel("Group"), groupEntry,
	)
	for _, f := range fields {
		form.Add(fieldLabel(f.label))
		form.Add(f.entry)
	}
	form.Add(fieldLabel("Tags"))
	form.Add(tagsEntry)
	form.Add(fieldLabel("Notes"))
	form.Add(notesEntry)

	objects := []fyne.CanvasObject{
		secretHeader(secret, isNew),
		card(form),
		dv.formButtons(saveBtn, copies, isNew, secret),
	}
	if !isNew {
		objects = append(objects, timestamps(secret))
	}
	dv.setObjects(objects...)
}

// secretHeader shows the secret's type icon, its name (or "New …" while it
// is being created) and its type and group.
func secretHeader(secret *store.Secret, isNew bool) fyne.CanvasObject {
	title := secret.Name
	sub := typeName(secret.Type)
	if secret.Group != "" {
		sub += " · " + secret.Group
	}
	if isNew {
		title = "New " + strings.ToLower(typeName(secret.Type))
		sub = "Fill in the details, then Save."
	}
	icon := fixedHeight(fixedWidth(widget.NewIcon(typeIcon(secret.Type)), 40), 40)
	text := container.New(layout.NewCustomPaddedVBoxLayout(-10), heading(title), caption(sub))
	return container.NewBorder(nil, nil, container.NewCenter(icon), nil, text)
}

// timestamps shows when a saved secret was created and last modified.
func timestamps(secret *store.Secret) fyne.CanvasObject {
	return caption(fmt.Sprintf("Created %s  ·  Modified %s",
		formatTime(secret.CreatedAt), formatTime(secret.ModifiedAt)))
}

// formatTime formats a stored (UTC) time for display.
func formatTime(t time.Time) string {
	return t.UTC().Format("2 Jan 2006 15:04 UTC")
}

// formButtons returns the action row: Save and the copy buttons (disabled
// for a secret that has not been saved yet) on the left, and Delete on the
// right for a saved secret.
func (dv *DetailView) formButtons(save *widget.Button, copies []copyAction, isNew bool, secret *store.Secret) *fyne.Container {
	left := container.NewHBox(save)
	for _, c := range copies {
		btn := widget.NewButtonWithIcon(c.label, theme.ContentCopyIcon(), func() { dv.copyValue(c.value()) })
		if isNew {
			btn.Disable()
		}
		left.Add(btn)
	}
	var right fyne.CanvasObject
	if !isNew {
		right = dv.deleteButton(secret)
	}
	return container.NewBorder(nil, nil, left, right)
}

// deleteButton asks to delete secret.
func (dv *DetailView) deleteButton(secret *store.Secret) *widget.Button {
	btn := widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() { dv.confirmDelete(secret) })
	btn.Importance = widget.DangerImportance
	return btn
}

// showFileDetail renders a file secret detail view.
func (dv *DetailView) showFileDetail(secret *store.Secret) {
	exportBtn := widget.NewButtonWithIcon("Export File", theme.DownloadIcon(), func() {
		dv.exportFile(secret)
	})
	exportBtn.Importance = widget.HighImportance

	value := func(text string) *widget.Label {
		l := widget.NewLabel(text)
		l.Wrapping = fyne.TextWrapBreak
		return l
	}
	dv.setObjects(
		secretHeader(secret, false),
		card(container.New(layout.NewFormLayout(),
			fieldLabel("Name"), value(secret.Name),
			fieldLabel("Group"), value(secret.Group),
			fieldLabel("Original file"), value(secret.OriginalFilename),
			fieldLabel("Type"), value(secret.MIMEType),
		)),
		container.NewBorder(nil, nil, exportBtn, dv.deleteButton(secret)),
		timestamps(secret),
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
					secret.Name, secret.Group), dv.window)
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
				dv.setObjects(emptyState(theme.DeleteIcon(), "Secret deleted",
					"Choose another secret from the list."))
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

// parseTags splits a comma-separated string into tags, trimming whitespace
// (including Unicode spaces) and dropping empty entries.
func parseTags(s string) []string {
	var tags []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// clear drops the shown secret and its form (used when the vault locks). The
// form's entries are blanked first: Fyne's renderer cache can keep the
// widgets reachable for a while after they leave the window.
func (dv *DetailView) clear() {
	dv.onRender = nil // blanking is not a render the tracker should see
	for _, obj := range dv.content.Objects {
		blankEntries(obj)
	}
	if dv.current != nil {
		*dv.current = store.Secret{}
	}
	if dv.fileOps != nil {
		dv.fileOps.dropPending()
	}
	dv.current = nil
	dv.content.Objects = nil
	dv.content.Refresh()
	dv.onSaved, dv.onDeleted, dv.onRender = nil, nil, nil
}

// blankEntries empties every entry in obj's tree without firing callbacks.
func blankEntries(obj fyne.CanvasObject) {
	switch w := obj.(type) {
	case *fyne.Container:
		for _, child := range w.Objects {
			blankEntries(child)
		}
	case *widget.Entry:
		w.OnChanged = nil
		w.SetText("")
	case *widget.SelectEntry:
		w.OnChanged = nil
		w.SetText("")
	}
}
