package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
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
	pick     *scriptedPicker
	activity int
	// pauses and resumes count the auto-lock pauses taken and resumed.
	pauses, resumes int
	end             context.CancelFunc
}

func newOpsHarness(t *testing.T) *opsHarness {
	t.Helper()
	test.NewTempApp(t)
	h := &opsHarness{q: &queue{}, w: test.NewTempWindow(t, nil), status: widget.NewLabel("")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.end = cancel
	h.pick = &scriptedPicker{}
	pause := func() func() {
		h.pauses++
		return func() { h.resumes++ }
	}
	h.ops = newOperations(h.w, h.q.runner().withContext(ctx), h.status, func() { h.activity++ }, pause, h.pick)
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

// A result the session ends for after the work last checked its context,
// but before delivery, is handed to release rather than lost unwiped.
func TestOperations_StartOwned_ReleasesAResultDroppedAtTheLock(t *testing.T) {
	h := newOpsHarness(t)
	plaintext := []byte("decoded secret")
	var released [][]byte
	work := func(ctx context.Context) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h.end() // the vault locks between the work and its delivery
		return plaintext, nil
	}

	startOwned(h.ops, &operation{doing: "Couldn't"}, work,
		func([]byte) { t.Fatal("a result after the lock must be dropped") },
		func(b []byte) { released = append(released, b) })
	h.q.flush()

	if len(released) != 1 || &released[0][0] != &plaintext[0] {
		t.Fatalf("released %d results; want the dropped one, once", len(released))
	}
}

func TestOperations_StartOwned_DeliversWithoutReleasing(t *testing.T) {
	h := newOpsHarness(t)
	var got []byte
	startOwned(h.ops, &operation{doing: "Couldn't"}, succeed([]byte("x")),
		func(b []byte) { got = b },
		func([]byte) { t.Fatal("a delivered result is done's to release") })
	h.q.flush()
	if string(got) != "x" {
		t.Fatalf("done got %q", got)
	}
}

// Moving the vault and changing its passphrase pause the auto-lock until
// they end, however they end; other work leaves it running.
func TestOperations_Start_PausesTheAutoLockWhileAnOperationThatKeepsUnlockedRuns(t *testing.T) {
	tests := []struct {
		name string
		work func(context.Context) (none, error)
		lock bool // the session ends before the outcome arrives
	}{
		{"succeeds", succeed(none{}), false},
		{"fails", fail(errors.New("boom")), false},
		{"outlives the session", succeed(none{}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newOpsHarness(t)
			start(h.ops, &operation{kind: opVault, keepUnlocked: true, failed: func(error) {}}, tt.work, func(none) {})
			if h.pauses != 1 || h.resumes != 0 {
				t.Fatalf("while running: %d pauses, %d resumed; want the auto-lock paused", h.pauses, h.resumes)
			}
			if tt.lock {
				h.end()
			}
			h.q.flush()
			if h.resumes != 1 {
				t.Fatalf("after: %d resumes, want 1", h.resumes)
			}
		})
	}

	h := newOpsHarness(t)
	start(h.ops, &operation{kind: opSecretChange}, succeed(none{}), func(none) {})
	h.q.flush()
	if h.pauses != 0 {
		t.Fatal("other operations must leave the auto-lock running")
	}
}

// Work whose outcome the user must hear of even after a lock (a passphrase
// change that may have rewritten vlt.age) reports it through afterEnd.
func TestOperations_Start_ReportsAnOutcomeDroppedAtTheLockToAfterEnd(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name string
		err  error
	}{
		{"succeeded", nil},
		{"failed", boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newOpsHarness(t)
			var reports []error
			work := func(context.Context) (none, error) {
				h.end() // the vault locks after the work, before its delivery
				return none{}, tt.err
			}
			start(h.ops, &operation{
				doing:    "Couldn't",
				failed:   func(error) { t.Fatal("failed must not run after the lock") },
				afterEnd: func(err error) { reports = append(reports, err) },
			}, work, func(none) { t.Fatal("done must not run after the lock") })
			h.q.flush()

			if len(reports) != 1 || !errors.Is(reports[0], tt.err) || (tt.err == nil && reports[0] != nil) {
				t.Fatalf("afterEnd got %v; want once, with %v", reports, tt.err)
			}
		})
	}

	h := newOpsHarness(t)
	start(h.ops, &operation{afterEnd: func(error) { t.Fatal("a delivered outcome is not reported again") }},
		succeed(none{}), func(none) {})
	h.q.flush()
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

func TestOperations_PickFile_UsesTheChoiceAndCountsTheAnswer(t *testing.T) {
	h := newOpsHarness(t)
	var got []fyne.URIReadCloser
	use := func(r fyne.URIReadCloser) { got = append(got, r) }
	chosen := &fakeURIReader{Reader: strings.NewReader("x"), uri: storage.NewFileURI("/tmp/x")}

	h.ops.pickFile(use)
	h.pick.answerFile(t, chosen, nil)
	h.ops.pickFile(use)
	h.pick.answerFile(t, nil, nil) // cancelled

	if len(got) != 1 || got[0] != chosen || h.activity != 2 {
		t.Fatalf("used %d files, activity %d; want the one chosen and both answers counted", len(got), h.activity)
	}
}

func TestOperations_PickFile_ShowsAFailure(t *testing.T) {
	h := newOpsHarness(t)
	h.ops.pickFile(func(fyne.URIReadCloser) { t.Fatal("nothing was chosen") })
	h.pick.answerFile(t, nil, errors.New("no such folder"))
	if text := dialogText(t, h.w); !strings.Contains(text, "Couldn't choose a file: no such folder") {
		t.Fatalf("error dialog says %q", text)
	}
}

func TestOperations_PickFile_ClosesAFileChosenAfterTheSessionEnded(t *testing.T) {
	h := newOpsHarness(t)
	chosen := &fakeURIReader{Reader: strings.NewReader("x"), uri: storage.NewFileURI("/tmp/x")}

	h.ops.pickFile(func(fyne.URIReadCloser) { t.Fatal("a file chosen after the lock must not be used") })
	h.end()
	h.pick.answerFile(t, chosen, nil)

	if !chosen.closed || h.activity != 0 {
		t.Fatalf("closed %v, activity %d; want the file closed and no activity", chosen.closed, h.activity)
	}
}

func TestOperations_PickFolder_GivesThePath(t *testing.T) {
	h := newOpsHarness(t)
	dir := t.TempDir()
	folder, err := storage.ListerForURI(storage.NewFileURI(dir))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	h.ops.pickFolder(func(path string) { got = path })
	h.pick.answerFolder(t, folder, nil)
	h.ops.pickFolder(func(string) { t.Fatal("a cancelled chooser gives nothing") })
	h.pick.answerFolder(t, nil, nil)

	if got != dir || h.activity != 2 {
		t.Fatalf("got %q, activity %d", got, h.activity)
	}
}

func TestOperations_PickFolder_ShowsAFailure(t *testing.T) {
	h := newOpsHarness(t)
	h.ops.pickFolder(func(string) { t.Fatal("nothing was chosen") })
	h.pick.answerFolder(t, nil, errors.New("no such folder"))
	if text := dialogText(t, h.w); !strings.Contains(text, "Couldn't choose a folder: no such folder") {
		t.Fatalf("error dialog says %q", text)
	}
}

func TestOperations_PickFolder_DropsAnAnswerAfterTheSessionEnded(t *testing.T) {
	h := newOpsHarness(t)
	folder, err := storage.ListerForURI(storage.NewFileURI(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}

	h.ops.pickFolder(func(string) { t.Fatal("a folder chosen after the lock must not be used") })
	h.ops.end()
	h.pick.answerFolder(t, folder, nil)

	if h.activity != 0 || h.w.Canvas().Overlays().Top() != nil {
		t.Fatalf("activity %d, dialog shown %v; want the late answer dropped", h.activity, h.w.Canvas().Overlays().Top() != nil)
	}
}

// saveAnswers records what pickSave passed to use.
type saveAnswers struct{ got []fyne.URIWriteCloser }

func (a *saveAnswers) use(w fyne.URIWriteCloser) { a.got = append(a.got, w) }

func TestOperations_PickSave_AnswersExactlyOnceWhicheverWayItEnds(t *testing.T) {
	tests := []struct {
		name      string
		end       func(t *testing.T, h *opsHarness, w fyne.URIWriteCloser)
		wantFile  bool
		wantError bool
		late      bool // a file is chosen after the end: it must be closed
	}{
		{"chosen", func(t *testing.T, h *opsHarness, w fyne.URIWriteCloser) { h.pick.answerSave(t, w, nil) }, true, false, false},
		{"cancelled", func(t *testing.T, h *opsHarness, _ fyne.URIWriteCloser) { h.pick.answerSave(t, nil, nil) }, false, false, false},
		{"failed", func(t *testing.T, h *opsHarness, _ fyne.URIWriteCloser) {
			h.pick.answerSave(t, nil, errors.New("no such folder"))
		}, false, true, false},
		{"locked, then chosen late", func(t *testing.T, h *opsHarness, w fyne.URIWriteCloser) {
			h.ops.end()
			h.pick.answerSave(t, w, nil)
		}, false, false, true},
		{"session context ended, then chosen", func(t *testing.T, h *opsHarness, w fyne.URIWriteCloser) {
			h.end()
			h.pick.answerSave(t, w, nil)
		}, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newOpsHarness(t)
			answers := &saveAnswers{}
			w := memWriter(t, "out.txt")

			h.ops.pickSave("out.txt", "Couldn't export", answers.use)
			tt.end(t, h, w)

			if len(answers.got) != 1 {
				t.Fatalf("use was called %d times, want once", len(answers.got))
			}
			if gotFile := answers.got[0] != nil; gotFile != tt.wantFile {
				t.Fatalf("use got a file: %v, want %v", gotFile, tt.wantFile)
			}
			if tt.late && (!w.closed || w.Len() != 0) {
				t.Fatal("a file chosen too late must be closed, with nothing written")
			}
			if shown := h.w.Canvas().Overlays().Top() != nil; shown != tt.wantError {
				t.Fatalf("error dialog shown: %v, want %v", shown, tt.wantError)
			}
		})
	}
}

func TestOperations_PickSave_AfterTheEndAnswersAtOnce(t *testing.T) {
	h := newOpsHarness(t)
	h.ops.end()
	answers := &saveAnswers{}
	h.ops.pickSave("out.txt", "Couldn't export", answers.use)
	if len(answers.got) != 1 || answers.got[0] != nil || len(h.pick.saves) != 0 {
		t.Fatal("asking after the session ended should answer nil without a dialog")
	}
}

func TestOperations_Now_RunsAtOnceUnlessItsKindIsRunning(t *testing.T) {
	h := newOpsHarness(t)
	ran := 0
	act := func() { ran++ }

	if !h.ops.now(&operation{kind: opVault}, act) || ran != 1 {
		t.Fatal("with nothing running, now should run at once")
	}
	if !h.ops.now(&operation{kind: opVault}, act) || ran != 2 {
		t.Fatal("an operation run by now should not keep its kind busy")
	}
	start(h.ops, &operation{kind: opVault}, succeed(none{}), func(none) {})
	if h.ops.now(&operation{kind: opVault}, act) || ran != 2 || h.status.Text != waitMessage {
		t.Fatal("while its kind runs, now should refuse and ask the user to wait")
	}
	h.q.flush()
	if !h.ops.now(&operation{kind: opVault}, act) || ran != 3 {
		t.Fatal("once the running operation ends, now should run again")
	}
}
