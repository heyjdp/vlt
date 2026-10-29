package ui

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
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
	s, err := store.Create(dir, testPassphrase, quietLog(), testKDF)
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
