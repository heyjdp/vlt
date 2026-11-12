package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/heyjdp/vlt/internal/testutil"
)

const testTimeout = 5 * time.Minute

// autoLocking builds an app over a vault holding one secret, with
// auto-lock after testTimeout.
func autoLocking() []rigOption {
	return []rigOption{
		withSecrets(testutil.PasswordSecret("gh", "x", "work")),
		withSettings(withLockTimeoutMs(int(testTimeout / time.Millisecond))),
	}
}

func TestAutoLock_TypingInAFormResetsTheCountdown(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock
	first := clock.active(testTimeout)
	if first == nil {
		t.Fatal("unlocking should start the auto-lock timer")
	}

	r.selectSecret(0)
	entry := firstEditableEntry(r.detail())
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
	if !r.locked() {
		t.Fatal("the vault should lock once the restarted countdown expires")
	}
}

func TestAutoLock_SearchAndListSelectionCountAsActivity(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock

	before := clock.active(testTimeout)
	r.search("g")
	if !before.stopped {
		t.Fatal("typing in the search box must reset the countdown")
	}
	before = clock.active(testTimeout)
	r.selectSecret(0)
	if !before.stopped {
		t.Fatal("selecting a list row must reset the countdown")
	}
	r.lock()
}

func TestAutoLock_IdlenessLocks(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock

	clock.active(testTimeout).f()

	if !r.locked() {
		t.Fatal("the vault should lock when the timer fires")
	}
	if r.shownScreen() != screenUnlock {
		t.Fatal("expected the unlock screen after auto-lock")
	}
	if clock.active(testTimeout) != nil || clock.active(warnDelay(testTimeout)) != nil {
		t.Fatal("locking must stop the auto-lock timers")
	}
}

func TestAutoLock_WarnsBeforeLockingAndActivityDismisses(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock

	warn := clock.active(warnDelay(testTimeout))
	if warn == nil {
		t.Fatal("a 5-minute timeout should schedule a warning")
	}
	warn.f()
	if !r.lockWarningShown() {
		t.Fatal("expected a lock warning")
	}

	r.search("x")
	if r.lockWarningShown() {
		t.Fatal("activity should dismiss the warning")
	}
	if clock.active(testTimeout) == nil {
		t.Fatal("activity should restart the countdown")
	}
	r.lock()
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
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock

	r.settings.SetLockTimeout(10 * time.Minute)
	r.search("x")

	if clock.active(10*time.Minute) == nil {
		t.Fatal("a changed timeout should be used when the countdown restarts")
	}
	r.lock()
}

func TestAutoLock_DisabledWhenTimeoutIsZero(t *testing.T) {
	r := newAppRig(t)
	clock := r.clock
	r.unlock()

	if len(clock.timers) != 0 {
		t.Fatalf("a zero timeout must not start timers, got %d", len(clock.timers))
	}
	r.search("x")
	if len(clock.timers) != 0 {
		t.Fatal("activity must not start timers when auto-lock is disabled")
	}
	r.lock()
}

func TestAutoLock_TypingWithNothingFocusedResetsTheCountdown(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock
	r.window.Canvas().Unfocus()

	before := clock.active(testTimeout)
	test.TypeOnCanvas(r.window.Canvas(), "x")

	if !before.stopped {
		t.Fatal("key presses with no focused widget must reset the countdown")
	}
	r.lock()
}

func TestAutoLock_StayUnlockedRestartsTheCountdown(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock

	lock := clock.active(testTimeout)
	clock.active(warnDelay(testTimeout)).f()
	r.stayUnlocked() // "Stay unlocked"

	if r.locked() {
		t.Fatal("Stay unlocked must not lock")
	}
	if !lock.stopped || clock.active(testTimeout) == nil {
		t.Fatal("Stay unlocked must restart the countdown")
	}
	r.lock()
}

func TestAutoLock_LockNowLocksImmediately(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock

	clock.active(warnDelay(testTimeout)).f()
	r.lockNow() // the user pressing "Lock now" dismisses the dialog

	if !r.locked() {
		t.Fatal("Lock now should lock the vault")
	}
}

func TestAutoLock_StaleTimersAreIgnoredAfterActivity(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock

	oldLock := clock.active(testTimeout)
	oldWarn := clock.active(warnDelay(testTimeout))
	r.search("x") // activity replaces both timers

	// Callbacks of the replaced timers that were already running.
	oldWarn.f()
	if r.lockWarningShown() {
		t.Fatal("a warning from a replaced timer must not be shown")
	}
	oldLock.f()
	if r.locked() {
		t.Fatal("a lock from a replaced timer must not lock the vault")
	}
	r.lock()
}

func TestAutoLock_DialogCallbacksCountAsActivity(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()
	clock := r.clock

	test.Tap(findButton(r.mainView().content(), "Import File"))
	before := clock.active(testTimeout)
	r.pick.answerFile(t, nil, nil) // the file chooser, cancelled
	if !before.stopped {
		t.Fatal("dialog callbacks must reset the countdown")
	}
	r.lock()
}

func TestActivityTracker_DoesNotRetainReplacedForms(t *testing.T) {
	r := newAppRig(t, autoLocking()...)
	r.unlock()

	for range 20 {
		r.mainView().list.list.UnselectAll()
		r.selectSecret(0)
	}
	// Only the current panel's widgets are remembered, not 20 old forms.
	if n := len(r.mainView().tracker.seen); n > 40 {
		t.Fatalf("tracker retains %d widgets after re-rendering; old forms are leaking", n)
	}
	r.lock()
	if r.shownScreen() == screenMain {
		t.Fatal("locking should drop the main view and its activity tracker")
	}
}
