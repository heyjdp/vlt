package ui

import (
	"context"
	"io"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// operations runs what the user asks for, with feedback. It is the one
// place that knows how an operation behaves around the person using vlt:
//
//   - start runs work in the background, bound to the runner's session (a
//     result arriving after a lock is dropped), shows progress while it runs,
//     refuses to start an operation whose kind is already running, and
//     shows a failure in words (errorMessage) unless the caller handles it.
//   - changeVault runs a change to the whole vault (its passphrase, its
//     location): one at a time, with the auto-lock waiting for it, the
//     vault relocking once it took effect, and its outcome reported even
//     after a lock. changeVaultNow is the same for a change with nothing to
//     wait for (switching vaults).
//   - confirm asks before an action.
//   - pickFile, pickFolder and pickSave ask for a file, a folder or where to
//     save, through a picker (Fyne's dialogs, or a test's fake).
//
// Answering one of its dialogs counts as activity for the auto-lock, and so
// does input in a dialog's content: dialogs are outside the widget tree the
// app's activity tracker watches. Once the session has ended (end, or its
// context cancelled), a late answer is dropped and a chosen file closed.
//
// The app keeps one for its whole life (update checks, which also run while
// locked) and makes one per session (everything done to the open vault).
// Its methods must be called on the Fyne main goroutine.
type operations struct {
	window fyne.Window
	run    runner
	// status shows an operation's progress and a refused start; nil shows
	// neither (the app's own operations, which run behind a dialog).
	status *widget.Label
	// activity reports the user's answer to a dialog to the auto-lock.
	activity func()
	// pause pauses the auto-lock and returns what resumes it; nil where
	// there is no session to keep unlocked.
	pause func() (resume func())
	// relock locks the vault after a vault change; nil where there is no
	// session (no vault change is run there).
	relock func()
	pick   picker
	// running holds the kinds of operation in flight.
	running map[opKind]bool
	// saving holds, by request, how to cancel each save request still open:
	// end answers them as cancelled, since the lock dismisses the dialog
	// without an answer. ended is set by end.
	saving   map[int]func()
	nextSave int
	ended    bool
}

// newOperations returns operations that run work on run, show progress in
// status (which may be nil), ask for files through pick, report answers to
// activity, and pause the auto-lock through pause and lock the vault after
// a vault change through relock (both nil where there is no session).
func newOperations(window fyne.Window, run runner, status *widget.Label, activity func(), pause func() (resume func()), relock func(), pick picker) *operations {
	return &operations{
		window:   window,
		run:      run,
		status:   status,
		activity: activity,
		pause:    pause,
		relock:   relock,
		pick:     pick,
		running:  make(map[opKind]bool),
		saving:   make(map[int]func()),
	}
}

// end ends the session's operations (the app calls it when the vault
// locks): a save request still open is answered as cancelled, so whoever
// asked can wipe what it meant to write, and later answers are dropped.
// Work in flight already has its result dropped (see start).
func (o *operations) end() {
	o.ended = true
	for id, cancel := range o.saving {
		delete(o.saving, id)
		cancel()
	}
}

// over reports whether the session has ended.
func (o *operations) over() bool {
	return o.ended || o.run.context().Err() != nil
}

// opKind names operations that must not overlap: while one of a kind runs,
// another of that kind is not started. Operations without a kind may.
type opKind string

const (
	// opSecretChange is saving or deleting a secret, which must not
	// interleave.
	opSecretChange opKind = "secret change"
	// opVault is changing the whole vault: changing the passphrase, moving
	// (copying) it or switching to another. Only changeVault and
	// changeVaultNow claim it. A copy taken while vlt.age is rewritten, a
	// second copy into the same folder, or a switch (which locks) in the
	// middle of a copy would leave a half-made vault.
	opVault opKind = "vault change"
	// opUpdateCheck is a check for updates the user asked for.
	opUpdateCheck opKind = "update check"
)

// waitMessage is shown when an operation is refused because another of its
// kind is running.
const waitMessage = "Please wait for the current operation to finish"

// operation describes how one run of start behaves. Only doing is needed;
// every other field may be left out.
type operation struct {
	// kind, if set, keeps two operations of the same kind from overlapping.
	kind opKind
	// doing leads the message when the work fails ("Couldn't save the
	// secret"), unless failed is set.
	doing string
	// progress says what is happening while the work runs: in the status
	// bar, or, when title is set, in a dialog with that title.
	progress string
	title    string
	// control (the button that started it) is disabled while it runs.
	control fyne.Disableable
	// failed, if set, shows a failure instead of the error dialog.
	failed func(error)
}

// start runs work in the background and calls done with its result on the
// main goroutine. It returns false, and does nothing more, if an operation
// of the same kind is running; the status bar then asks the user to wait.
// While work runs, op's control is disabled and its progress shown; both
// are undone before done or failed is called. If the session ends first,
// the result is dropped and neither is called.
func start[T any](o *operations, op *operation, work func(context.Context) (T, error), done func(T)) bool {
	return startOwned(o, op, work, done, nil)
}

// startOwned is start for a result that must be released when it is
// dropped, such as decoded plaintext: if the session ends before the result
// is delivered, release (if not nil) is called with it, on the main
// goroutine, instead of done. Once done is called, the result is done's.
func startOwned[T any](o *operations, op *operation, work func(context.Context) (T, error), done, release func(T)) bool {
	return launch(o, op, false, work, done, func(v T, _ error) {
		if release != nil {
			release(v)
		}
	})
}

// launch is the body of start and changeVault. keepUnlocked pauses the
// auto-lock until the outcome arrives or is dropped. An outcome arriving
// after the session ended goes to dropped, on the main goroutine, instead
// of done or failed.
func launch[T any](o *operations, op *operation, keepUnlocked bool, work func(context.Context) (T, error), done func(T), dropped func(T, error)) bool {
	if !o.claim(op.kind) {
		return false
	}
	if op.control != nil {
		op.control.Disable()
	}
	hide := o.showProgress(op)
	resume := func() {}
	if keepUnlocked && o.pause != nil {
		resume = o.pause()
	}
	backgroundOwned(o.run, work, func(v T, err error) {
		resume()
		if op.kind != "" {
			delete(o.running, op.kind)
		}
		if op.control != nil {
			op.control.Enable()
		}
		hide()
		switch {
		case err == nil:
			done(v)
		case op.failed != nil:
			op.failed(err)
		default:
			showError(o.window, op.doing, err)
		}
	}, func(v T, err error) {
		resume()
		dropped(v, err)
	})
	return true
}

// vaultChange describes a change to the whole vault for changeVault.
type vaultChange struct {
	// progress says what is happening, in the status bar, while it runs.
	progress string
	// control (the button that started it) is disabled while it runs.
	control fyne.Disableable
	// failed shows a failure while the vault is still unlocked.
	failed func(error)
	// changed runs when the change took effect while the vault is
	// unlocked, before it relocks, and returns what to tell the user over
	// the unlock screen.
	changed func() notice
	// outlived, if set, is told the outcome (nil for success) when the
	// vault locked before it arrived: on the main goroutine, after the
	// lock has dismissed its dialogs, so it may show one. It must touch
	// nothing of the ended session. Without it, such an outcome says
	// nothing.
	outlived func(error)
}

// notice is what a vault change tells the user, in a dialog, once the
// vault has relocked.
type notice struct {
	title, message string
}

// changeVault runs work, a change to the whole vault (changing its
// passphrase, moving it), in the background, as start does: progress
// shown, the control disabled, a failure shown by change.failed. Beyond
// that:
//
//   - it is refused, asking the user to wait, while another vault change
//     runs (see opVault);
//   - the auto-lock waits until its outcome arrives or is dropped, since an
//     auto-lock cutting it off for want of input would leave a half-made
//     copy or an unreported change of vlt.age (locking by hand still ends
//     the session, and the work with it);
//   - once it took effect, change.changed runs, the vault relocks, and the
//     user is told change.changed's notice over the unlock screen;
//   - an outcome that arrives after the session ended goes to
//     change.outlived rather than being dropped.
func (o *operations) changeVault(change *vaultChange, work func(context.Context) error) bool {
	op := &operation{kind: opVault, progress: change.progress, control: change.control, failed: change.failed}
	return launch(o, op, true,
		func(ctx context.Context) (none, error) { return none{}, work(ctx) },
		func(none) { o.relockThenTell(change.changed()) },
		func(_ none, err error) {
			if change.outlived != nil {
				fyne.Do(func() { change.outlived(err) })
			}
		})
}

// changeVaultNow runs f at once, on the main goroutine, as a vault change
// with nothing to wait for (switching vaults), then relocks. Like
// changeVault, it is refused, asking the user to wait, while another vault
// change runs, which it must not cut into.
func (o *operations) changeVaultNow(f func()) bool {
	if !o.claim(opVault) {
		return false
	}
	defer delete(o.running, opVault)
	f()
	o.relock()
	return true
}

// relockThenTell locks the vault, then tells the user n on top of the
// unlock screen. Lock dismisses open dialogs from a queued fyne.Do, so the
// dialog is queued after it rather than shown first.
func (o *operations) relockThenTell(n notice) {
	o.relock()
	window := o.window
	fyne.Do(func() { dialog.ShowInformation(n.title, n.message, window) })
}

// claim marks kind as running, or reports false (asking the user to wait)
// if it already is. Every kind may be claimed when kind is empty.
func (o *operations) claim(kind opKind) bool {
	if kind == "" {
		return true
	}
	if o.running[kind] {
		if o.status != nil {
			o.status.SetText(waitMessage)
		}
		return false
	}
	o.running[kind] = true
	return true
}

// showProgress shows op's progress and returns what hides it again.
func (o *operations) showProgress(op *operation) (hide func()) {
	switch {
	case op.progress == "":
		return func() {}
	case op.title != "":
		d := dialog.NewCustomWithoutButtons(op.title,
			container.NewVBox(muted(op.progress), widget.NewProgressBarInfinite()), o.window)
		d.Show()
		return d.Hide
	case o.status != nil:
		o.status.SetText(op.progress)
		return func() { o.status.SetText("") }
	}
	return func() {}
}

// question is what confirm asks: a message answered Yes or No, or content
// with its own button labels.
type question struct {
	title   string
	message string
	// content, if set, replaces message; confirm and dismiss label its
	// buttons.
	content          fyne.CanvasObject
	confirm, dismiss string
}

// confirm asks q and calls yes on the main goroutine if the user agrees.
// The answer, and any input in q's content, counts as activity.
func (o *operations) confirm(q *question, yes func()) {
	answered := func(ok bool) {
		o.activity()
		if ok {
			yes()
		}
	}
	if q.content == nil {
		dialog.ShowConfirm(q.title, q.message, answered, o.window)
		return
	}
	// A tracker of its own: the dialog's widgets are not the app's to forget.
	newActivityTracker(o.activity).instrument(q.content)
	dialog.ShowCustomConfirm(q.title, q.confirm, q.dismiss, q.content, answered, o.window)
}

// pickFile asks for a file to open and calls use with it, on the main
// goroutine; use must close it. Nothing happens if the user cancels, and a
// failure is shown.
func (o *operations) pickFile(use func(fyne.URIReadCloser)) {
	o.pick.openFile(func(r fyne.URIReadCloser, err error) {
		if o.over() {
			closeChosen(r)
			return
		}
		o.activity()
		switch {
		case err != nil:
			showError(o.window, "Couldn't choose a file", err)
		case r != nil:
			use(r)
		}
	})
}

// pickFolder asks for a folder and calls use with its path, on the main
// goroutine. Nothing happens if the user cancels, and a failure is shown.
func (o *operations) pickFolder(use func(path string)) {
	o.pick.openFolder(func(uri fyne.ListableURI, err error) {
		if o.over() {
			return
		}
		o.activity()
		switch {
		case err != nil:
			showError(o.window, "Couldn't choose a folder", err)
		case uri != nil:
			// Fyne's file URIs use forward slashes on Windows too (C:/vault).
			use(filepath.FromSlash(uri.Path()))
		}
	})
}

// pickSave asks where to save a file, suggesting name, and calls use
// exactly once, on the main goroutine: with the chosen file (use must
// close it), or with nil when nothing is to be written there: the user
// cancelled, choosing failed (shown, led by doing), or the session ended
// before an answer (then a file chosen late is closed).
func (o *operations) pickSave(name, doing string, use func(fyne.URIWriteCloser)) {
	if o.over() {
		use(nil)
		return
	}
	id := o.nextSave
	o.nextSave++
	o.saving[id] = func() { use(nil) }
	o.pick.saveFile(name, func(w fyne.URIWriteCloser, err error) {
		cancel, open := o.saving[id]
		if !open {
			closeChosen(w) // answered as cancelled at the lock
			return
		}
		delete(o.saving, id)
		if o.over() {
			closeChosen(w)
			cancel()
			return
		}
		o.activity()
		switch {
		case err != nil:
			showError(o.window, doing, err)
			use(nil)
		case w == nil:
			use(nil)
		default:
			use(w)
		}
	})
}

// closeChosen closes a file chosen too late to be used. Nothing was
// written, so a failure to close loses nothing.
func closeChosen(f io.Closer) {
	if f != nil {
		_ = f.Close()
	}
}
