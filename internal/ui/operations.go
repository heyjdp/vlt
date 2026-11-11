package ui

import (
	"context"

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
//   - confirm asks before an action.
//
// Answering one of its dialogs counts as activity for the auto-lock, and so
// does input in a dialog's content: dialogs are outside the widget tree the
// app's activity tracker watches.
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
	// running holds the kinds of operation in flight.
	running map[opKind]bool
}

// newOperations returns operations that run work on run, show progress in
// status (which may be nil) and report answers to activity.
func newOperations(window fyne.Window, run runner, status *widget.Label, activity func()) *operations {
	return &operations{
		window:   window,
		run:      run,
		status:   status,
		activity: activity,
		running:  make(map[opKind]bool),
	}
}

// opKind names operations that must not overlap: while one of a kind runs,
// another of that kind is not started. Operations without a kind may.
type opKind string

const (
	// opSecretChange is saving or deleting a secret, which must not
	// interleave.
	opSecretChange opKind = "secret change"
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
	if op.kind != "" {
		if o.running[op.kind] {
			if o.status != nil {
				o.status.SetText(waitMessage)
			}
			return false
		}
		o.running[op.kind] = true
	}
	if op.control != nil {
		op.control.Disable()
	}
	hide := o.showProgress(op)
	background(o.run, work, func(v T, err error) {
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
	})
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
