package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// opsHarness is a session's operations on a queued runner, with a status
// bar, a counted activity hook and a cancellable session context.
type opsHarness struct {
	ops      *operations
	q        *queue
	w        fyne.Window
	status   *widget.Label
	activity int
	end      context.CancelFunc
}

func newOpsHarness(t *testing.T) *opsHarness {
	t.Helper()
	test.NewTempApp(t)
	h := &opsHarness{q: &queue{}, w: test.NewTempWindow(t, nil), status: widget.NewLabel("")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.end = cancel
	h.ops = newOperations(h.w, h.q.runner().withContext(ctx), h.status, func() { h.activity++ })
	return h
}

// succeed is work that returns v.
func succeed[T any](v T) func(context.Context) (T, error) {
	return func(context.Context) (T, error) { return v, nil }
}

// fail is work that fails with err.
func fail(err error) func(context.Context) (none, error) {
	return func(context.Context) (none, error) { return none{}, err }
}

func TestOperations_Start_ShowsProgressThenDeliversTheResult(t *testing.T) {
	h := newOpsHarness(t)
	btn := widget.NewButton("Go", nil)
	got := 0

	ok := start(h.ops, &operation{progress: "Working…", control: btn}, succeed(7), func(v int) { got = v })

	if !ok || h.status.Text != "Working…" || !btn.Disabled() || got != 0 {
		t.Fatalf("while running: started %v, status %q, disabled %v, result %d", ok, h.status.Text, btn.Disabled(), got)
	}
	h.q.flush()
	if got != 7 || h.status.Text != "" || btn.Disabled() {
		t.Fatalf("after: result %d, status %q, disabled %v", got, h.status.Text, btn.Disabled())
	}
}

func TestOperations_Start_RefusesASecondOfTheSameKindUntilTheFirstEnds(t *testing.T) {
	h := newOpsHarness(t)
	runs := 0
	count := func(none) { runs++ }

	start(h.ops, &operation{kind: opSecretChange}, succeed(none{}), count)
	second := start(h.ops, &operation{kind: opSecretChange}, succeed(none{}), count)
	other := start(h.ops, &operation{kind: opUpdateCheck}, succeed(none{}), count)

	if second || len(h.q.pending) != 2 || h.status.Text != waitMessage {
		t.Fatalf("second start = %v with %d queued and status %q; want refused, 2 queued, asked to wait",
			second, len(h.q.pending), h.status.Text)
	}
	if !other {
		t.Fatal("an operation of another kind should start")
	}
	h.q.flush()
	if !start(h.ops, &operation{kind: opSecretChange}, succeed(none{}), count) {
		t.Fatal("once the first has finished, the kind should start again")
	}
	h.q.flush()
	if runs != 3 {
		t.Fatalf("%d results delivered, want 3", runs)
	}
}

func TestOperations_Start_FreesTheKindWhenTheWorkFails(t *testing.T) {
	h := newOpsHarness(t)
	start(h.ops, &operation{kind: opSecretChange, failed: func(error) {}}, fail(errors.New("boom")), func(none) {})
	h.q.flush()
	if !start(h.ops, &operation{kind: opSecretChange}, succeed(none{}), func(none) {}) {
		t.Fatal("a failed operation must not keep its kind busy")
	}
}

func TestOperations_Start_ShowsAFailureInWords(t *testing.T) {
	h := newOpsHarness(t)
	btn := widget.NewButton("Go", nil)

	start(h.ops, &operation{doing: "Couldn't do it", progress: "Working…", control: btn},
		fail(fmt.Errorf("store.Thing %q: %w", "x", errors.New("disk full"))), func(none) { t.Fatal("done must not run on failure") })
	h.q.flush()

	if text := dialogText(t, h.w); !strings.Contains(text, "Couldn't do it: disk full") || strings.Contains(text, "store.") {
		t.Fatalf("error dialog says %q", text)
	}
	if h.status.Text != "" || btn.Disabled() {
		t.Fatal("a failure must clear the progress and enable the control again")
	}
}

func TestOperations_Start_LetsTheCallerShowAFailure(t *testing.T) {
	h := newOpsHarness(t)
	var got error
	boom := errors.New("boom")

	start(h.ops, &operation{doing: "Couldn't", failed: func(err error) { got = err }}, fail(boom), func(none) {})
	h.q.flush()

	if !errors.Is(got, boom) || h.w.Canvas().Overlays().Top() != nil {
		t.Fatalf("failed got %v and a dialog is shown: %v; want only the caller's handling", got, h.w.Canvas().Overlays().Top() != nil)
	}
}

func TestOperations_Start_DropsAResultArrivingAfterTheSessionEnded(t *testing.T) {
	h := newOpsHarness(t)
	start(h.ops, &operation{doing: "Couldn't"}, succeed(1), func(int) { t.Fatal("a result after the lock must be dropped") })
	start(h.ops, &operation{doing: "Couldn't"}, fail(errors.New("x")), func(none) {})

	h.end()
	h.q.flush()

	if h.w.Canvas().Overlays().Top() != nil {
		t.Fatal("a failure after the lock must not be shown")
	}
}

func TestOperations_Start_ShowsProgressInADialogWhenTitled(t *testing.T) {
	h := newOpsHarness(t)
	start(h.ops, &operation{title: "Checking", progress: "Asking…"}, succeed(none{}), func(none) {})

	if text := dialogText(t, h.w); !strings.Contains(text, "Asking…") {
		t.Fatalf("progress dialog says %q", text)
	}
	h.q.flush()
	if h.w.Canvas().Overlays().Top() != nil || h.status.Text != "" {
		t.Fatal("the progress dialog should close when the work ends, and the status bar stay untouched")
	}
}

func TestOperations_Confirm_RunsTheActionOnlyOnYesAndCountsTheAnswer(t *testing.T) {
	for _, answer := range []string{"Yes", "No"} {
		t.Run(answer, func(t *testing.T) {
			h := newOpsHarness(t)
			ran := false

			h.ops.confirm(&question{title: "Delete", message: "Sure?"}, func() { ran = true })
			test.Tap(dialogButton(t, h.w, answer))

			if ran != (answer == "Yes") || h.activity != 1 {
				t.Fatalf("answered %s: action ran %v, activity %d", answer, ran, h.activity)
			}
		})
	}
}

func TestOperations_Confirm_InputInTheContentCountsAsActivity(t *testing.T) {
	h := newOpsHarness(t)
	entry := widget.NewSelectEntry(nil)

	h.ops.confirm(&question{
		title:   "Import",
		content: widget.NewForm(widget.NewFormItem("Group", entry)),
		confirm: "Import",
		dismiss: "Cancel",
	}, func() {})
	test.Type(entry, "w")

	if h.activity != 1 {
		t.Fatalf("typing in the dialog reported %d activity, want 1", h.activity)
	}
	test.Tap(dialogButton(t, h.w, "Import"))
	if h.activity != 2 {
		t.Fatalf("answering reported %d activity in all, want 2", h.activity)
	}
}
