package ui

import (
	"context"
	"testing"
	"time"

	"github.com/heyjdp/vlt/internal/store"
)

// openTestStore creates a vault in a temporary folder and opens it.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	newVaultWith(t, dir)
	s, err := store.Open(context.Background(), dir, testPassphrase, quietLog(), testKDF)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// isClosed reports whether s has been closed and has released its lock (a
// local store does both before Close returns).
func isClosed(s *store.Store) bool {
	select {
	case <-s.Closed():
		return true
	default:
		return false
	}
}

// sessionEvents records the callbacks a session fires.
type sessionEvents struct{ warned, timedOut int }

// newTestSession is a session over real stores with hand-fired timers,
// callbacks run inline, and the given auto-lock timeout.
func newTestSession(timeout time.Duration) (*session[*store.Store], *fakeClock, *sessionEvents) {
	clock := &fakeClock{}
	ev := &sessionEvents{}
	s := newSession[*store.Store](clock.factory, func(f func()) { f() }, func() time.Duration { return timeout },
		sessionCallbacks{Warn: func() { ev.warned++ }, TimedOut: func() { ev.timedOut++ }})
	return s, clock, ev
}

// queuedSession is newTestSession with callbacks posted to a queue the test
// runs, as fyne.Do hands them to the main goroutine.
func queuedSession(timeout time.Duration) (*session[*store.Store], *fakeClock, *sessionEvents, *[]func()) {
	clock, ev, posted := &fakeClock{}, &sessionEvents{}, &[]func(){}
	s := newSession[*store.Store](clock.factory, func(f func()) { *posted = append(*posted, f) },
		func() time.Duration { return timeout },
		sessionCallbacks{Warn: func() { ev.warned++ }, TimedOut: func() { ev.timedOut++ }})
	return s, clock, ev, posted
}

// slowStore stands in for a vault on a server: Close zeroes the key at once
// but the lock is released only when release is called.
type slowStore struct {
	closedAt int // how many Close calls
	done     chan struct{}
}

func newSlowStore() *slowStore               { return &slowStore{done: make(chan struct{})} }
func (s *slowStore) Close()                  { s.closedAt++ }
func (s *slowStore) Closed() <-chan struct{} { return s.done }
func (s *slowStore) release()                { close(s.done) }

const sessionTimeout = 5 * time.Minute

func TestSession_BeginAndEnd(t *testing.T) {
	t.Parallel()
	sess, _, _ := newTestSession(sessionTimeout)
	if sess.Store() != nil {
		t.Fatal("a new session has no vault open")
	}
	st := openTestStore(t)
	ctx := sess.Begin(st)
	gen := sess.Gen()
	if sess.Store() != st || ctx.Err() != nil || !sess.Current(gen) {
		t.Fatal("Begin should open the session on the store")
	}

	sess.End()
	if sess.Store() != nil || ctx.Err() == nil || !isClosed(st) {
		t.Fatal("End should close the store and cancel the session context")
	}
	if sess.Current(gen) {
		t.Fatal("work started before End must see that the session ended")
	}
	sess.End() // ending again is harmless
}

func TestSession_TimesOutAfterInactivity(t *testing.T) {
	t.Parallel()
	sess, clock, ev := newTestSession(sessionTimeout)
	sess.Begin(openTestStore(t))

	warn := clock.active(sessionTimeout - lockWarningLead)
	timeout := clock.active(sessionTimeout)
	if warn == nil || timeout == nil {
		t.Fatal("Begin should arm the warning and the auto-lock")
	}
	warn.f()
	if ev.warned != 1 || ev.timedOut != 0 {
		t.Fatalf("events = %+v, want one warning", *ev)
	}
	timeout.f()
	if ev.timedOut != 1 || sess.Store() != nil {
		t.Fatal("the auto-lock should end the session after the timeout")
	}
}

func TestSession_TheTimeoutZeroesTheKeyBeforeTheMainGoroutineRuns(t *testing.T) {
	t.Parallel()
	sess, clock, ev, posted := queuedSession(sessionTimeout)
	st := openTestStore(t)
	sess.Begin(st)
	clock.active(sessionTimeout - lockWarningLead).f()
	clock.active(sessionTimeout).f()
	// The main goroutine is busy (a menu open, a resize): nothing posted
	// has run, but the key must already be gone.
	if !isClosed(st) || sess.Store() != nil {
		t.Fatal("the timeout should close the store on the timer's goroutine")
	}
	if ev.warned != 0 || ev.timedOut != 0 || len(*posted) != 2 {
		t.Fatalf("callbacks must wait for the main goroutine: %+v", *ev)
	}
	for _, f := range *posted {
		f()
	}
	if ev.timedOut != 1 {
		t.Fatal("the app should then hear about the timeout")
	}
}

func TestSession_ALateTimeoutNoticeAfterAnotherLockIsDropped(t *testing.T) {
	t.Parallel()
	sess, clock, ev, posted := queuedSession(sessionTimeout)
	sess.Begin(openTestStore(t))
	clock.active(sessionTimeout).f()
	sess.End() // the user locked (or unlocked again and locked) meanwhile
	for _, f := range *posted {
		f()
	}
	if ev.timedOut != 0 {
		t.Fatal("a timeout notice for a session already handled must do nothing")
	}
}

func TestSession_ActivityRestartsTheCountdown(t *testing.T) {
	t.Parallel()
	sess, clock, ev := newTestSession(sessionTimeout)
	sess.Begin(openTestStore(t))
	oldWarn, oldTimeout := clock.active(sessionTimeout-lockWarningLead), clock.active(sessionTimeout)

	sess.Activity()
	if !oldTimeout.stopped || !oldWarn.stopped {
		t.Fatal("activity should stop the running timers")
	}
	// Timers that were already firing when the activity came are ignored.
	oldWarn.f()
	oldTimeout.f()
	if ev.warned != 0 || ev.timedOut != 0 {
		t.Fatalf("superseded timers fired: %+v", *ev)
	}
	if clock.active(sessionTimeout) == nil {
		t.Fatal("activity should start a fresh countdown")
	}
}

func TestSession_NoCountdownWhileLockedOrWithAutoLockOff(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(sessionTimeout)
	sess.Activity()
	if len(clock.timers) != 0 {
		t.Fatal("activity with no vault open starts nothing")
	}

	off, offClock, _ := newTestSession(0)
	off.Begin(openTestStore(t))
	off.Activity()
	if len(offClock.timers) != 0 {
		t.Fatal("with auto-lock off there is no countdown")
	}

	short, shortClock, _ := newTestSession(time.Minute - time.Second)
	short.Begin(openTestStore(t))
	if len(shortClock.timers) != 1 {
		t.Fatalf("a timeout too short for a warning arms %d timers, want only the auto-lock", len(shortClock.timers))
	}
}

func TestSession_EndStopsTheCountdown(t *testing.T) {
	t.Parallel()
	sess, clock, ev := newTestSession(sessionTimeout)
	sess.Begin(openTestStore(t))
	warn, timeout := clock.active(sessionTimeout-lockWarningLead), clock.active(sessionTimeout)

	sess.End()
	warn.f()
	timeout.f()
	if !timeout.stopped || ev.warned != 0 || ev.timedOut != 0 {
		t.Fatalf("a timer firing after End must do nothing: %+v", *ev)
	}
}

func TestSession_APendingStoreIsClosedWhenTheTimeoutPasses(t *testing.T) {
	t.Parallel()
	sess, clock, _, posted := queuedSession(sessionTimeout)
	st := openTestStore(t)
	expired := 0
	sess.Hold(st, func() { expired++ })

	clock.active(sessionTimeout).f()
	if !isClosed(st) || sess.Pending() != nil || sess.Take(st) {
		t.Fatal("an unanswered question should close the waiting store at once")
	}
	if expired != 0 || len(*posted) != 1 {
		t.Fatal("the app hears about it on the main goroutine")
	}
	(*posted)[0]()
	if expired != 1 {
		t.Fatal("the expiry callback should run")
	}
}

func TestSession_WithAutoLockOffAPendingStoreWaitsForAnAnswer(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(0)
	st := openTestStore(t)
	sess.Hold(st, func() { t.Error("no timeout, no expiry") })
	if len(clock.timers) != 0 || sess.Pending() != st {
		t.Fatalf("with auto-lock off Hold arms %d timers, want none", len(clock.timers))
	}
}

func TestSession_HoldingAnotherStoreClosesTheFirst(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(sessionTimeout)
	first, second := openTestStore(t), openTestStore(t)
	sess.Hold(first, func() {})
	firstTimer := clock.active(sessionTimeout)
	sess.Hold(second, func() {})
	if !isClosed(first) || !firstTimer.stopped || sess.Pending() != second {
		t.Fatal("a store replaced while waiting must not keep its key")
	}
}

func TestSession_TakeNilIsFalse(t *testing.T) {
	t.Parallel()
	sess, _, _ := newTestSession(sessionTimeout)
	if sess.Take(nil) {
		t.Fatal("nothing is pending, so there is nothing to take")
	}
}

func TestSession_BeginningAgainEndsTheOpenSession(t *testing.T) {
	t.Parallel()
	sess, _, _ := newTestSession(sessionTimeout)
	first := openTestStore(t)
	ctx := sess.Begin(first)
	gen := sess.Gen()
	sess.Begin(openTestStore(t))
	if !isClosed(first) || ctx.Err() == nil || sess.Current(gen) {
		t.Fatal("beginning a second session must end the first")
	}
}

func TestSession_WarningNeedsTwiceTheLead(t *testing.T) {
	t.Parallel()
	for timeout, want := range map[time.Duration]time.Duration{
		2*lockWarningLead - time.Nanosecond: 0,
		2 * lockWarningLead:                 lockWarningLead,
		sessionTimeout:                      sessionTimeout - lockWarningLead,
	} {
		if got := warnDelay(timeout); got != want {
			t.Errorf("warnDelay(%v) = %v, want %v", timeout, got, want)
		}
	}
	sess, clock, _ := newTestSession(2 * lockWarningLead)
	sess.Begin(openTestStore(t))
	if clock.active(lockWarningLead) == nil {
		t.Fatal("a timeout of exactly twice the lead leaves room for a warning")
	}
}

func TestSession_TakingAPendingStoreStopsItsTimer(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(sessionTimeout)
	st := openTestStore(t)
	expired := 0
	sess.Hold(st, func() { expired++ })
	timer := clock.active(sessionTimeout)
	if sess.Pending() != st {
		t.Fatal("the held store should be pending")
	}

	if !sess.Take(st) || sess.Take(st) || sess.Pending() != nil {
		t.Fatal("a pending store is taken exactly once")
	}
	timer.f() // already firing when it was taken
	if !timer.stopped || expired != 0 || isClosed(st) {
		t.Fatal("a taken store is the caller's; the timer must leave it alone")
	}
}

func TestSession_CloseClosesTheOpenAndPendingStoresAndWaits(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(sessionTimeout)
	open, pending := openTestStore(t), openTestStore(t)
	ctx := sess.Begin(open)
	sess.Hold(pending, func() {})

	timer := clock.active(sessionTimeout)
	if !sess.Close(time.Second) {
		t.Fatal("local stores release their locks at once")
	}
	if !isClosed(open) || !isClosed(pending) || ctx.Err() == nil || sess.Store() != nil || sess.Take(pending) || !timer.stopped {
		t.Fatal("Close should end the session, close both stores and stop the waiting store's timer")
	}
}

func TestSession_CloseWaitsForStoresToReleaseTheirLocks(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"open", "pending", "ended earlier"} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()
			clock := &fakeClock{}
			sess := newSession[*slowStore](clock.factory, func(f func()) { f() },
				func() time.Duration { return sessionTimeout }, sessionCallbacks{Warn: func() {}, TimedOut: func() {}})
			st := newSlowStore()
			switch which {
			case "open":
				sess.Begin(st)
			case "pending":
				sess.Hold(st, func() {})
			case "ended earlier":
				sess.Begin(st)
				sess.End()
			}
			if sess.Close(20*time.Millisecond) || st.closedAt == 0 {
				t.Fatal("Close should close the store, then give up waiting after the deadline")
			}
			st.release()
			if !sess.Close(time.Second) {
				t.Fatal("once the store has released its lock, Close succeeds")
			}
		})
	}
}

func TestSession_CloseWaitsForAStoreEndedEarlier(t *testing.T) {
	t.Parallel()
	sess, _, _ := newTestSession(sessionTimeout)
	st := openTestStore(t)
	sess.Begin(st)
	sess.End()
	if !sess.Close(time.Second) {
		t.Fatal("the store ended earlier has released its lock")
	}
}
