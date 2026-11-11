package ui

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
)

// Shared helpers for the ui tests.
//
// Most ui tests do not call t.Parallel: fyne/test's NewTempApp replaces the
// process-global current app, so widget tests must run one at a time. Pure
// functions (parsing, validation, formatting) are tested in parallel.

// testKDF keeps scrypt cheap (2^10) wherever a test creates, opens or
// re-wraps a vault through App; the production default (2^18) would cost
// gigabytes of memory under -race.
var testKDF = store.WithWorkFactor(10)

// testPassphrase is the passphrase of every vault the helpers create.
const testPassphrase = "correct horse battery"

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// inlineRunner runs background work immediately on the caller's goroutine.
// With fyne/test's inline fyne.Do, app tests are deterministic.
func inlineRunner() runner {
	return runner{spawn: func(f func()) { f() }}
}

// testOps returns operations on run for a component built without an App:
// progress goes to a label nobody reads, and answers report no activity.
func testOps(t *testing.T, run runner) *operations {
	return newOperations(test.NewTempWindow(t, nil), run, widget.NewLabel(""), func() {}, &scriptedPicker{})
}

// queue is a runner that holds work until flush, so tests can observe the
// UI while work is "in flight" and prove it is not done on the caller.
type queue struct{ pending []func() }

func (q *queue) runner() runner {
	return runner{spawn: func(f func()) { q.pending = append(q.pending, f) }}
}

func (q *queue) flush() {
	for len(q.pending) > 0 {
		f := q.pending[0]
		q.pending = q.pending[1:]
		f()
	}
}

// fakeTimer is a stoppableTimer that only fires when a test says so.
type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool                 { t.stopped = true; return true }
func (t *fakeTimer) Reset(d time.Duration) bool { t.d, t.stopped = d, false; return true }

// fakeClock records the timers an App starts.
type fakeClock struct{ timers []*fakeTimer }

func (c *fakeClock) factory(d time.Duration, f func()) stoppableTimer {
	t := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, t)
	return t
}

// active returns the running timer with duration d, or nil.
func (c *fakeClock) active(d time.Duration) *fakeTimer {
	for i := len(c.timers) - 1; i >= 0; i-- {
		if t := c.timers[i]; !t.stopped && t.d == d {
			return t
		}
	}
	return nil
}

// noTimers is a timer factory whose timers never fire.
func noTimers(d time.Duration, f func()) stoppableTimer { return &fakeTimer{d: d, f: f} }

// newVaultWith creates a vault in dir holding secrets, with a cheap work
// factor and testPassphrase.
func newVaultWith(t *testing.T, dir string, secrets ...*store.Secret) {
	t.Helper()
	s, err := store.Create(context.Background(), dir, testPassphrase, quietLog(), testKDF)
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	defer s.Close()
	for _, sec := range secrets {
		if err := s.Create(context.Background(), sec); err != nil {
			t.Fatalf("Create %q: %v", sec.Name, err)
		}
	}
}

// unlock types testPassphrase into the unlock screen and submits it.
func unlock(app *App) {
	test.Type(app.unlockScreen.entry, testPassphrase)
	test.Tap(app.unlockScreen.unlockBtn)
}

// unlockQueued unlocks with a queued runner and runs the open, leaving the
// initial list load queued.
func unlockQueued(t *testing.T, app *App, q *queue) {
	t.Helper()
	unlock(app)
	open := q.pending[0]
	q.pending = q.pending[1:]
	open()
	if app.secretList == nil {
		t.Fatal("expected the main view after the open completes")
	}
}

// saveButton is a stand-in for the form's Save button, which saveSecret
// disables while a save is in flight.
func saveButton() *widget.Button { return widget.NewButton("Save", nil) }

// findDeep returns the first object in obj's tree, including inside widget
// renderers (so inside dialogs), for which match returns true.
func findDeep(obj fyne.CanvasObject, match func(fyne.CanvasObject) bool) fyne.CanvasObject {
	if obj == nil {
		return nil
	}
	if match(obj) {
		return obj
	}
	var children []fyne.CanvasObject
	switch w := obj.(type) {
	case *fyne.Container:
		children = w.Objects
	case fyne.Widget:
		children = test.WidgetRenderer(w).Objects()
	}
	for _, child := range children {
		if found := findDeep(child, match); found != nil {
			return found
		}
	}
	return nil
}

// containedEntries returns the entries in obj's tree of containers, in
// order. Like blankEntries it does not look inside widgets, so a form whose
// entries it finds is one that blankEntries can reach.
func containedEntries(obj fyne.CanvasObject) []*widget.Entry {
	var entries []*widget.Entry
	switch w := obj.(type) {
	case *fyne.Container:
		for _, child := range w.Objects {
			entries = append(entries, containedEntries(child)...)
		}
	case *widget.Entry:
		entries = append(entries, w)
	}
	return entries
}

// deepEntries returns every plain or password entry in obj's tree,
// including inside other widgets' renderers.
func deepEntries(obj fyne.CanvasObject) []*widget.Entry {
	var entries []*widget.Entry
	findDeep(obj, func(o fyne.CanvasObject) bool {
		if e, ok := o.(*widget.Entry); ok {
			entries = append(entries, e)
		}
		return false
	})
	return entries
}

// findType returns the first object of type T in obj's tree, or the zero T.
func findType[T fyne.CanvasObject](obj fyne.CanvasObject) T {
	var zero T
	found := findDeep(obj, func(o fyne.CanvasObject) bool { _, ok := o.(T); return ok })
	if found == nil {
		return zero
	}
	return found.(T)
}

// findShown is findDeep over what is on screen: hidden objects, and
// everything inside them, are skipped.
func findShown(obj fyne.CanvasObject, match func(fyne.CanvasObject) bool) fyne.CanvasObject {
	if obj == nil || !obj.Visible() {
		return nil
	}
	if match(obj) {
		return obj
	}
	var children []fyne.CanvasObject
	switch w := obj.(type) {
	case *fyne.Container:
		children = w.Objects
	case fyne.Widget:
		children = test.WidgetRenderer(w).Objects()
	}
	for _, child := range children {
		if found := findShown(child, match); found != nil {
			return found
		}
	}
	return nil
}

// visibleText reports whether a label on screen in obj contains text.
func visibleText(obj fyne.CanvasObject, text string) bool {
	return findShown(obj, func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && strings.Contains(l.Text, text)
	}) != nil
}

// shownButton returns the button labelled text on screen in obj, or nil.
func shownButton(obj fyne.CanvasObject, text string) *widget.Button {
	b, _ := findShown(obj, func(o fyne.CanvasObject) bool {
		b, ok := o.(*widget.Button)
		return ok && b.Text == text
	}).(*widget.Button)
	return b
}

// visibleButton returns the button labelled text on screen in obj.
func visibleButton(t *testing.T, obj fyne.CanvasObject, text string) *widget.Button {
	t.Helper()
	b := shownButton(obj, text)
	if b == nil {
		t.Fatalf("no visible %q button", text)
	}
	return b
}

// findButton returns the button labelled text in obj's tree, or nil.
func findButton(obj fyne.CanvasObject, text string) *widget.Button {
	found := findDeep(obj, func(o fyne.CanvasObject) bool {
		b, ok := o.(*widget.Button)
		return ok && b.Text == text
	})
	if found == nil {
		return nil
	}
	return found.(*widget.Button)
}

// entryWithPlaceholder returns the entry whose placeholder is text.
func entryWithPlaceholder(t *testing.T, obj fyne.CanvasObject, text string) *widget.Entry {
	t.Helper()
	found := findDeep(obj, func(o fyne.CanvasObject) bool {
		e, ok := o.(*widget.Entry)
		return ok && e.PlaceHolder == text
	})
	if found == nil {
		t.Fatalf("no entry with placeholder %q", text)
	}
	return found.(*widget.Entry)
}

// firstEditableEntry finds the first enabled entry in obj's tree.
func firstEditableEntry(obj fyne.CanvasObject) *widget.Entry {
	found := findDeep(obj, func(o fyne.CanvasObject) bool {
		e, ok := o.(*widget.Entry)
		return ok && !e.Disabled()
	})
	if found == nil {
		return nil
	}
	return found.(*widget.Entry)
}

// dialogButton returns the button labelled text in the topmost dialog.
// It relies on Fyne's default (English) dialog button labels.
func dialogButton(t *testing.T, w fyne.Window, text string) *widget.Button {
	t.Helper()
	top := w.Canvas().Overlays().Top()
	if top == nil {
		t.Fatalf("expected a dialog with a %q button, but none is open", text)
	}
	b := findButton(top, text)
	if b == nil {
		t.Fatalf("the open dialog has no %q button", text)
	}
	return b
}

// dialogEntry returns the select entry in the topmost dialog.
func dialogEntry(t *testing.T, w fyne.Window) *widget.SelectEntry {
	t.Helper()
	e := findType[*widget.SelectEntry](w.Canvas().Overlays().Top())
	if e == nil {
		t.Fatal("the open dialog has no entry")
	}
	return e
}

// scriptedPicker is the tests' picker: it records each request, and the
// test answers it as the user would, or leaves it open, as a lock does.
type scriptedPicker struct {
	files   []func(fyne.URIReadCloser, error)
	folders []func(fyne.ListableURI, error)
	saves   []saveRequest
}

// saveRequest is an open request for where to save a file.
type saveRequest struct {
	name   string
	answer func(fyne.URIWriteCloser, error)
}

var _ picker = (*scriptedPicker)(nil)

func (p *scriptedPicker) openFile(answer func(fyne.URIReadCloser, error)) {
	p.files = append(p.files, answer)
}

func (p *scriptedPicker) openFolder(answer func(fyne.ListableURI, error)) {
	p.folders = append(p.folders, answer)
}

func (p *scriptedPicker) saveFile(name string, answer func(fyne.URIWriteCloser, error)) {
	p.saves = append(p.saves, saveRequest{name: name, answer: answer})
}

// answerFile answers the oldest open request for a file (r nil: cancel).
func (p *scriptedPicker) answerFile(t *testing.T, r fyne.URIReadCloser, err error) {
	t.Helper()
	if len(p.files) == 0 {
		t.Fatal("no file chooser is open")
	}
	answer := p.files[0]
	p.files = p.files[1:]
	answer(r, err)
}

// answerFolder answers the oldest open request for a folder (uri nil:
// cancel).
func (p *scriptedPicker) answerFolder(t *testing.T, uri fyne.ListableURI, err error) {
	t.Helper()
	if len(p.folders) == 0 {
		t.Fatal("no folder chooser is open")
	}
	answer := p.folders[0]
	p.folders = p.folders[1:]
	answer(uri, err)
}

// answerSave answers the oldest open save request (w nil: cancel) and
// returns the file name it suggested.
func (p *scriptedPicker) answerSave(t *testing.T, w fyne.URIWriteCloser, err error) string {
	t.Helper()
	if len(p.saves) == 0 {
		t.Fatal("no save dialog is open")
	}
	req := p.saves[0]
	p.saves = p.saves[1:]
	req.answer(w, err)
	return req.name
}

// pickerOf returns app's scripted picker, giving it one (for the app and
// any open session) the first time.
func pickerOf(app *App) *scriptedPicker {
	if p, ok := app.pick.(*scriptedPicker); ok {
		return p
	}
	p := &scriptedPicker{}
	app.pick, app.ops.pick = p, p
	if app.sessionOps != nil {
		app.sessionOps.pick = p
	}
	return p
}

// importFile imports r through the main view's Import File button and the
// file chooser.
func importFile(t *testing.T, app *App, r fyne.URIReadCloser) {
	t.Helper()
	p := pickerOf(app)
	test.Tap(findButton(app.mainContent, "Import File"))
	p.answerFile(t, r, nil)
}
