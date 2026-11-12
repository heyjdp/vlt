package ui

import (
	"log/slog"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
)

// mainView is what an unlocked vault shows: the secret list on the left,
// the detail panel on the right, file import and export, and a header of
// actions (Add Password, Add Record, Import File, Settings, Lock) above
// them. One is built per session, from what it needs (mainViewDeps), and
// it wires its parts itself: a save, delete or import updates the list, an
// import re-renders the form it replaced, and any input in the view counts
// as activity for the auto-lock. wipe ends it when the vault locks. Its
// methods run on the Fyne main goroutine.
type mainView struct {
	root     fyne.CanvasObject
	list     *SecretList
	detail   *DetailView
	files    *FileOps
	ops      *operations
	tracker  *activityTracker
	settings func() *SettingsScreen
}

// mainViewDeps is what a main view is built from.
type mainViewDeps struct {
	window fyne.Window
	// store is the open vault.
	store *store.Store
	// ops are the session's operations. Saves, deletes, imports and exports
	// run through them, and the list loads on their runner, so a result
	// arriving after a lock is dropped. The view owns them: wipe ends them.
	ops *operations
	// copy puts a secret value on the clipboard, which clears it later.
	copy   func(string)
	status *widget.Label
	log    *slog.Logger
	// timers and now drive a record's one-time code.
	timers timerFactory
	now    func() time.Time
	// activity is called on any input in the view (the auto-lock's reset).
	activity func()
	// footer is shown along the bottom (the app's status row).
	footer fyne.CanvasObject
	// lock is the Lock button's action; settings builds the Settings
	// screen, which the Settings button shows in the detail panel.
	lock     func()
	settings func() *SettingsScreen
}

// utcNow is the production clock for the main view.
func utcNow() time.Time { return time.Now().UTC() }

// newMainView builds the main view for the open vault d.store and starts
// loading its secrets in the background.
func newMainView(d *mainViewDeps) *mainView {
	mv := &mainView{ops: d.ops, settings: d.settings, tracker: newActivityTracker(d.activity)}
	mv.list = newSecretList(d, func(s *store.Secret) { mv.detail.ShowSecret(s) })
	mv.detail = newDetailView(d, detailHooks{
		groups:   mv.list.Groups,
		saved:    mv.list.Upsert,
		deleted:  mv.list.Remove,
		export:   func(s *store.Secret) { mv.files.ExportFile(s) },
		rendered: mv.rendered,
	})
	mv.files = newFileOps(d, mv.list.Groups, mv.imported)

	split := container.NewHSplit(mv.list.Content(), mv.detail.Panel())
	split.SetOffset(0.32)
	mv.root = container.NewBorder(mv.header(d.lock), d.footer, nil, nil, split)

	// The detail panel re-instruments its widgets each time it renders.
	mv.tracker.instrument(mv.root)
	mv.list.Refresh()
	return mv
}

// header is the row above the panels: vlt's mark and the view's actions.
func (mv *mainView) header(lock func()) fyne.CanvasObject {
	toolButton := func(label string, icon fyne.Resource, tapped func()) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, tapped)
		b.Importance = widget.LowImportance
		return b
	}
	brand := widget.NewLabel("vlt")
	brand.TextStyle = fyne.TextStyle{Bold: true}
	return panelBackground(container.NewVBox(
		padded(container.NewBorder(nil, nil,
			container.NewHBox(brandMark(22), brand),
			container.NewHBox(
				toolButton("Add Password", keyIcon, mv.detail.ShowNewPassword),
				toolButton("Add Record", theme.AccountIcon(), mv.detail.ShowNewRecord),
				toolButton("Import File", theme.UploadIcon(), mv.files.ImportFile),
				toolButton("Settings", theme.SettingsIcon(), func() { mv.openSettings() }),
				widget.NewButtonWithIcon("Lock", lockIcon, lock),
			)), 4),
		widget.NewSeparator(),
	))
}

// content is the view to show in the window.
func (mv *mainView) content() fyne.CanvasObject {
	return mv.root
}

// openSettings shows the Settings screen in the detail panel.
func (mv *mainView) openSettings() *SettingsScreen {
	settings := mv.settings()
	mv.detail.ShowContent(settings.Content())
	return settings
}

// imported shows an imported file in the list, and re-renders the form if
// it shows the secret the import replaced.
func (mv *mainView) imported(s *store.Secret) {
	mv.list.Upsert(s)
	mv.detail.RefreshIfShowing(s)
}

// rendered tracks input in a newly rendered detail panel, forgetting the
// widgets of the forms it replaced.
func (mv *mainView) rendered(panel fyne.CanvasObject) {
	mv.tracker.forget()
	mv.tracker.instrument(panel)
}

// wipe ends the view when the vault locks, releasing what it holds of the
// vault's secrets: it ends its operations (an export waiting on its save
// dialog wipes its plaintext, and later answers are dropped), stops a
// one-time code and wipes its key, blanks the detail form's entries,
// empties every loaded secret struct (so anything still pointing at one,
// such as a closure in a widget held by Fyne's renderer cache, reaches
// only empty fields) and forgets the widgets it tracked. Go cannot wipe
// the strings themselves; they become unreachable and are reclaimed by
// the garbage collector. Background work still in flight holds its own
// copy until it completes and its result is dropped.
func (mv *mainView) wipe() {
	mv.ops.end()
	mv.detail.clear()
	mv.list.clear()
	mv.tracker.forget()
}
