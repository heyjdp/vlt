package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// lockWarningLead is how long before an auto-lock the user is warned. The
// warning is only shown when the timeout is long enough to leave room for it.
const lockWarningLead = 30 * time.Second

// stoppableTimer is the part of *time.Timer the auto-lock uses, so tests can
// substitute timers they fire by hand.
type stoppableTimer interface {
	Stop() bool
	Reset(d time.Duration) bool
}

// timerFactory starts a timer that calls f after d (time.AfterFunc in
// production). f runs on another goroutine.
type timerFactory func(d time.Duration, f func()) stoppableTimer

// realTimers is the production timerFactory.
func realTimers(d time.Duration, f func()) stoppableTimer {
	return time.AfterFunc(d, f)
}

// activityTracker reports user input so the auto-lock measures real
// inactivity. Fyne has no global input hook (canvas key handlers fire only
// when no widget has focus), so it wraps the callbacks of the interactive
// widgets in a view: typing in an entry or select entry, pressing a button,
// selecting a list row, moving a slider or toggling a check all count. The
// widget's own callback runs first, so a changed setting (such as the
// auto-lock timeout) is in effect when activity is reported.
type activityTracker struct {
	onActivity func()
	// seen records instrumented widgets so callbacks are wrapped once.
	seen map[fyne.CanvasObject]bool
}

// newActivityTracker returns a tracker that calls onActivity on input.
func newActivityTracker(onActivity func()) *activityTracker {
	return &activityTracker{onActivity: onActivity, seen: make(map[fyne.CanvasObject]bool)}
}

// forget drops the record of instrumented widgets so that replaced forms
// (and the secret values in their entries) are not kept alive. Call it
// before instrumenting a freshly rendered panel.
func (t *activityTracker) forget() {
	t.seen = make(map[fyne.CanvasObject]bool)
}

// instrument wraps the input callbacks of every interactive widget in obj's
// tree. Containers are walked on every call because their children change;
// widgets are wrapped only once.
func (t *activityTracker) instrument(obj fyne.CanvasObject) {
	if obj == nil || t.seen[obj] {
		return
	}
	switch w := obj.(type) {
	case *fyne.Container:
		for _, child := range w.Objects {
			t.instrument(child)
		}
		return
	case *container.Split:
		t.instrument(w.Leading)
		t.instrument(w.Trailing)
		return
	case *container.Scroll:
		t.instrument(w.Content)
		return
	case *widget.Entry:
		prev := w.OnChanged
		w.OnChanged = func(s string) {
			if prev != nil {
				prev(s)
			}
			t.onActivity()
		}
	case *widget.SelectEntry:
		prev := w.OnChanged
		w.OnChanged = func(s string) {
			if prev != nil {
				prev(s)
			}
			t.onActivity()
		}
	case *widget.Button:
		prev := w.OnTapped
		w.OnTapped = func() {
			if prev != nil {
				prev()
			}
			t.onActivity()
		}
	case *widget.List:
		prev := w.OnSelected
		w.OnSelected = func(id widget.ListItemID) {
			if prev != nil {
				prev(id)
			}
			t.onActivity()
		}
	case *widget.Slider:
		prev := w.OnChanged
		w.OnChanged = func(v float64) {
			if prev != nil {
				prev(v)
			}
			t.onActivity()
		}
	case *widget.Check:
		prev := w.OnChanged
		w.OnChanged = func(b bool) {
			if prev != nil {
				prev(b)
			}
			t.onActivity()
		}
	}
	t.seen[obj] = true
}

// noActivity is the default activity hook (auto-lock disabled or unset).
func noActivity() {}
