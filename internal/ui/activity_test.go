package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/yourorg/vlt/internal/testutil"
)

const testTimeout = 5 * time.Minute

func newLockingApp(t *testing.T) (*App, *fakeClock) {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "x", "work"))
	clock := &fakeClock{}
	cfg := Config{VaultDir: dir, LockTimeoutMs: int(testTimeout / time.Millisecond)}
	app := newApp(a, test.NewTempWindow(t, nil), cfg, quietLog(), inlineRunner(), clock.factory, testKDF)
	unlock(app)
	return app, clock
}

func TestAutoLock_TypingInAFormResetsTheCountdown(t *testing.T) {
	app, clock := newLockingApp(t)
	first := clock.active(testTimeout)
	if first == nil {
		t.Fatal("unlocking should start the auto-lock timer")
	}

	app.secretList.list.Select(0)
	entry := firstEditableEntry(app.detailView.Content())
	if entry == nil {
		t.Fatal("expected an editable entry in the secret form")
	}
	// Measure from after the selection, so only the typing can restart it.
	beforeTyping := clock.active(testTimeout)
	test.Type(entry, "more")

	if !beforeTyping.stopped || clock.active(testTimeout) == nil {
		t.Fatal("typing in a form must restart the auto-lock countdown")
	}

	// Once typing stops, the restarted countdown still locks.
	clock.active(testTimeout).f()
	if app.store != nil {
		t.Fatal("the vault should lock once the restarted countdown expires")
	}
}

func TestAutoLock_SearchAndListSelectionCountAsActivity(t *testing.T) {
	app, clock := newLockingApp(t)

	before := clock.active(testTimeout)
	test.Type(app.secretList.searchEntry, "g")
	if !before.stopped {
		t.Fatal("typing in the search box must reset the countdown")
	}
	before = clock.active(testTimeout)
	app.secretList.list.Select(0)
	if !before.stopped {
		t.Fatal("selecting a list row must reset the countdown")
	}
	app.Lock()
}

func TestAutoLock_IdlenessLocks(t *testing.T) {
	app, clock := newLockingApp(t)

	clock.active(testTimeout).f()

	if app.store != nil {
		t.Fatal("the vault should lock when the timer fires")
	}
	if app.unlockScreen == nil {
		t.Fatal("expected the unlock screen after auto-lock")
	}
	if clock.active(testTimeout) != nil || clock.active(warnDelay(testTimeout)) != nil {
		t.Fatal("locking must stop the auto-lock timers")
	}
}

func TestAutoLock_WarnsBeforeLockingAndActivityDismisses(t *testing.T) {
	app, clock := newLockingApp(t)

	warn := clock.active(warnDelay(testTimeout))
	if warn == nil {
		t.Fatal("a 5-minute timeout should schedule a warning")
	}
	warn.f()
	if app.lockWarning == nil {
		t.Fatal("expected a lock warning")
	}

	test.Type(app.secretList.searchEntry, "x")
	if app.lockWarning != nil {
		t.Fatal("activity should dismiss the warning")
	}
	if clock.active(testTimeout) == nil {
		t.Fatal("activity should restart the countdown")
	}
	app.Lock()
}

func TestAutoLock_ShortTimeoutHasNoWarning(t *testing.T) {
	if got := warnDelay(45 * time.Second); got != 0 {
		t.Fatalf("a 45s timeout leaves no room for a 30s warning, got %v", got)
	}
	if got := warnDelay(5 * time.Minute); got != 5*time.Minute-lockWarningLead {
		t.Fatalf("warnDelay(5m) = %v", got)
	}
}

func TestAutoLock_TimeoutChangeAppliesOnNextActivity(t *testing.T) {
	app, clock := newLockingApp(t)

	app.config.LockTimeoutMs = int((10 * time.Minute) / time.Millisecond)
	test.Type(app.secretList.searchEntry, "x")

	if clock.active(10*time.Minute) == nil {
		t.Fatal("a changed timeout should be used when the countdown restarts")
	}
	app.Lock()
}

func TestAutoLock_DisabledWhenTimeoutIsZero(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	clock := &fakeClock{}
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), clock.factory, testKDF)
	unlock(app)

	if len(clock.timers) != 0 {
		t.Fatalf("a zero timeout must not start timers, got %d", len(clock.timers))
	}
	test.Type(app.secretList.searchEntry, "x")
	if len(clock.timers) != 0 {
		t.Fatal("activity must not start timers when auto-lock is disabled")
	}
	app.Lock()
}

func TestAutoLock_TypingWithNothingFocusedResetsTheCountdown(t *testing.T) {
	app, clock := newLockingApp(t)
	app.window.Canvas().Unfocus()

	before := clock.active(testTimeout)
	test.TypeOnCanvas(app.window.Canvas(), "x")

	if !before.stopped {
		t.Fatal("key presses with no focused widget must reset the countdown")
	}
	app.Lock()
}

func TestAutoLock_StayUnlockedRestartsTheCountdown(t *testing.T) {
	app, clock := newLockingApp(t)

	lock := clock.active(testTimeout)
	clock.active(warnDelay(testTimeout)).f()
	app.lockWarning.Confirm() // "Stay unlocked"

	if app.store == nil {
		t.Fatal("Stay unlocked must not lock")
	}
	if !lock.stopped || clock.active(testTimeout) == nil {
		t.Fatal("Stay unlocked must restart the countdown")
	}
	app.Lock()
}

func TestAutoLock_LockNowLocksImmediately(t *testing.T) {
	app, clock := newLockingApp(t)

	clock.active(warnDelay(testTimeout)).f()
	app.lockWarning.Hide() // the user pressing "Lock now" dismisses the dialog

	if app.store != nil {
		t.Fatal("Lock now should lock the vault")
	}
}

func TestAutoLock_StaleTimersAreIgnoredAfterActivity(t *testing.T) {
	app, clock := newLockingApp(t)

	oldLock := clock.active(testTimeout)
	oldWarn := clock.active(warnDelay(testTimeout))
	test.Type(app.secretList.searchEntry, "x") // activity replaces both timers

	// Callbacks of the replaced timers that were already running.
	oldWarn.f()
	if app.lockWarning != nil {
		t.Fatal("a warning from a replaced timer must not be shown")
	}
	oldLock.f()
	if app.store == nil {
		t.Fatal("a lock from a replaced timer must not lock the vault")
	}
	app.Lock()
}

func TestAutoLock_DialogCallbacksCountAsActivity(t *testing.T) {
	app, clock := newLockingApp(t)

	before := clock.active(testTimeout)
	app.detailView.fileOps.activity() // e.g. the file picker or group form returning
	if !before.stopped {
		t.Fatal("dialog callbacks must reset the countdown")
	}
	app.Lock()
}

func TestActivityTracker_DoesNotRetainReplacedForms(t *testing.T) {
	app, _ := newLockingApp(t)

	for range 20 {
		app.secretList.list.UnselectAll()
		app.secretList.list.Select(0)
	}
	// Only the current panel's widgets are remembered, not 20 old forms.
	if n := len(app.tracker.seen); n > 40 {
		t.Fatalf("tracker retains %d widgets after re-rendering; old forms are leaking", n)
	}
	app.Lock()
	if app.tracker != nil {
		t.Fatal("locking should drop the activity tracker")
	}
}
