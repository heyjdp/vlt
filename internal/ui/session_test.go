package ui

import (
	"context"
	"sync/atomic"
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
// local store does both before Close returns, and these sessions run Close
// inline).
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
	s := newSession[*store.Store](clock.factory, func(f func()) { f() }, inline, func() time.Duration { return timeout },
		sessionCallbacks{Warn: func() { ev.warned++ }, TimedOut: func() { ev.timedOut++ }})
	return s, clock, ev
}

// queuedSession is newTestSession with callbacks posted to a queue the test
// runs, as fyne.Do hands them to the main goroutine.
func queuedSession(timeout time.Duration) (*session[*store.Store], *fakeClock, *sessionEvents, *[]func()) {
	clock, ev, posted := &fakeClock{}, &sessionEvents{}, &[]func(){}
	s := newSession[*store.Store](clock.factory, func(f func()) { *posted = append(*posted, f) }, inline,
		func() time.Duration { return timeout },
		sessionCallbacks{Warn: func() { ev.warned++ }, TimedOut: func() { ev.timedOut++ }})
	return s, clock, ev, posted
}

// inline runs a store's Close at once, so tests over real local stores see
// the lock released when End returns.
func inline(f func()) { f() }

// slowStore stands in for a vault on a server: Close zeroes the key at once
// but the lock is released only when release is called.
type slowStore struct {
	closedAt int // how many Close calls
	zeroed   int // how many ZeroKey calls
	done     chan struct{}
}

func newSlowStore() *slowStore               { return &slowStore{done: make(chan struct{})} }
func (s *slowStore) ZeroKey()                { s.zeroed++ }
func (s *slowStore) Close()                  { s.closedAt++ }
func (s *slowStore) Closed() <-chan struct{} { return s.done }
func (s *slowStore) release()                { close(s.done) }

// stuckStore stands in for a local vault on a network folder that has
// stopped answering: ZeroKey takes effect at once, but Close blocks in the
// lock file's release until unstick is called (and zeroes only then, so a
// session relying on Close to zero the key is caught).
type stuckStore struct {
	zeroed  atomic.Bool
	unstuck chan struct{}
	done    chan struct{}
}

func newStuckStore() *stuckStore {
	return &stuckStore{unstuck: make(chan struct{}), done: make(chan struct{})}
}
func (s *stuckStore) ZeroKey()                { s.zeroed.Store(true) }
func (s *stuckStore) Close()                  { <-s.unstuck; s.ZeroKey(); close(s.done) }
func (s *stuckStore) Closed() <-chan struct{} { return s.done }
func (s *stuckStore) unstick()                { close(s.unstuck) }

// returnsPromptly reports whether f returns within a few seconds.
func returnsPromptly(f func()) bool {
	done := make(chan struct{})
	go func() {
		f()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

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

func TestSession_PauseHoldsOffTheAutoLockUntilResumed(t *testing.T) {
	t.Parallel()
	sess, clock, ev := newTestSession(sessionTimeout)
	st := openTestStore(t)
	sess.Begin(st)
	warn, timeout := clock.active(sessionTimeout-lockWarningLead), clock.active(sessionTimeout)

	resume := sess.Pause()
	warn.f() // already firing when the pause came
	timeout.f()
	if !warn.stopped || !timeout.stopped || ev.warned != 0 || ev.timedOut != 0 || isClosed(st) {
		t.Fatalf("a pause must stop the countdown and silence its timers: %+v", *ev)
	}
	if clock.active(sessionTimeout) != nil || clock.active(sessionTimeout-lockWarningLead) != nil {
		t.Fatal("no countdown while paused")
	}

	resume()
	if clock.active(sessionTimeout-lockWarningLead) == nil {
		t.Fatal("resuming should warn again before locking")
	}
	clock.active(sessionTimeout).f()
	if ev.timedOut != 1 || !isClosed(st) {
		t.Fatal("after the pause the full countdown should run and lock")
	}
}

func TestSession_ActivityWhilePausedKeepsThePause(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(sessionTimeout)
	sess.Begin(openTestStore(t))
	sess.Pause()
	before := clock.active(pausedLockAfter)

	sess.Activity()
	if clock.active(sessionTimeout) != nil {
		t.Fatal("input during a pause must not restart the short countdown")
	}
	after := clock.active(pausedLockAfter)
	if after == nil || after == before || !before.stopped {
		t.Fatal("input during a pause should restart the long wait in full")
	}
}

func TestSession_PausesNestAndResumeCountsOnce(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(sessionTimeout)
	sess.Begin(openTestStore(t))
	first, second := sess.Pause(), sess.Pause()

	first()
	first() // resuming twice is resuming once
	if clock.active(sessionTimeout) != nil {
		t.Fatal("the auto-lock must wait while another pause is held")
	}
	second()
	if clock.active(sessionTimeout) == nil {
		t.Fatal("the countdown should restart once every pause is resumed")
	}
}

// An operation stuck for good (a hung network folder) must not keep the
// key in memory for ever: a pause only lengthens the wait to pausedLockAfter.
func TestSession_APausedSessionStillLocksAfterLongWithoutInput(t *testing.T) {
	t.Parallel()
	sess, clock, ev := newTestSession(sessionTimeout)
	st := openTestStore(t)
	sess.Begin(st)
	sess.Pause()

	live := clock.running()
	if len(live) != 1 || live[0].d != pausedLockAfter {
		t.Fatalf("a pause arms %d timers; want only the auto-lock after %v", len(live), pausedLockAfter)
	}
	live[0].f()
	if ev.timedOut != 1 || !isClosed(st) {
		t.Fatal("the long wait should still lock the vault")
	}

	off, offClock, _ := newTestSession(0)
	off.Begin(openTestStore(t))
	off.Pause()
	if len(offClock.running()) != 0 {
		t.Fatal("with auto-lock off a pause arms nothing")
	}
}

// Locking by hand (or quitting) during an operation still ends the session
// at once, and the operation's resume, coming late, changes nothing.
func TestSession_EndDuringAPauseLocksAndALateResumeDoesNothing(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(sessionTimeout)
	st := openTestStore(t)
	ctx := sess.Begin(st)
	stale := sess.Pause()

	sess.End()
	if !isClosed(st) || ctx.Err() == nil || len(clock.running()) != 0 {
		t.Fatal("ending a paused session must close the store, cancel its work and stop every timer")
	}
	stale()
	if len(clock.running()) != 0 {
		t.Fatal("a resume after the lock must not start a countdown")
	}

	sess.Begin(openTestStore(t))
	resume := sess.Pause()
	stale()
	if clock.active(sessionTimeout) != nil {
		t.Fatal("a resume from an ended session must not end a pause in the next")
	}
	resume()
	if clock.active(sessionTimeout) == nil {
		t.Fatal("the next session's own resume should restart its countdown")
	}

	sess.End()
	sess.Pause()() // pausing while locked does nothing
	if len(clock.running()) != 0 {
		t.Fatal("no countdown while locked")
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
			sess := newSession[*slowStore](clock.factory, func(f func()) { f() }, inline,
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

// Locking must not wait on the vault's storage: a "local" vault on a network
// folder that stops answering would freeze the window (End runs on the main
// goroutine) and every keystroke (Activity waits on the session's mutex).
// The key is zeroed at once and the store detached; only the lock file's
// release waits, in the background, and quitting still waits for it.
func TestSession_EndingZeroesTheKeyAtOnceAndReleasesTheLockInTheBackground(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"lock", "auto-lock", "unlock again", "discard a waiting vault", "hold another"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			clock := &fakeClock{}
			sess := newSession[*stuckStore](clock.factory, func(f func()) { f() }, func(f func()) { go f() },
				func() time.Duration { return sessionTimeout }, sessionCallbacks{Warn: func() {}, TimedOut: func() {}})
			st, next := newStuckStore(), newStuckStore()
			next.unstick() // a store that follows st releases at once
			defer func() {
				select {
				case <-st.unstuck:
				default:
					st.unstick() // let the background Close end
				}
			}()
			var end func()
			switch how {
			case "lock":
				sess.Begin(st)
				end = sess.End
			case "auto-lock":
				sess.Begin(st)
				end = clock.active(sessionTimeout).f
			case "unlock again":
				sess.Begin(st)
				end = func() { sess.Begin(next) }
			case "discard a waiting vault":
				sess.Hold(st, func() {})
				end = func() {
					if sess.Take(st) {
						sess.Discard(st)
					}
				}
			case "hold another":
				sess.Hold(st, func() {})
				end = func() { sess.Hold(next, func() {}) }
			}

			if !returnsPromptly(end) {
				t.Fatal("ending waited for the store to release its lock")
			}
			if !st.zeroed.Load() || sess.Store() == st || sess.Pending() == st {
				t.Fatal("the key should be zeroed and the store let go before ending returns")
			}
			if !returnsPromptly(sess.Activity) {
				t.Fatal("input waited for the store to release its lock")
			}
			if sess.Close(20 * time.Millisecond) {
				t.Fatal("quitting should wait for the lock to be released")
			}
			st.unstick()
			if !sess.Close(time.Second) {
				t.Fatal("once the lock is released, quitting goes ahead")
			}
		})
	}
}

// A waiting store's time can run out while vlt quits. Whichever goes first,
// quitting must wait for that store's release: the timer must not take the
// store (out of the quit's reach) and only then let it go. The window is
// narrow; the loop finds it reliably under the race detector (go test -race).
func TestSession_QuitWhileAWaitingStoreTimesOutWaitsForItsRelease(t *testing.T) {
	t.Parallel()
	for range 20000 {
		clock := &fakeClock{}
		sess := newSession[*slowStore](clock.factory, func(func()) {}, inline,
			func() time.Duration { return sessionTimeout }, sessionCallbacks{Warn: func() {}, TimedOut: func() {}})
		st := newSlowStore() // never releases its lock
		sess.Hold(st, func() {})
		expire := clock.active(sessionTimeout).f
		expired := make(chan struct{})
		go func() {
			defer close(expired)
			expire()
		}()
		quitWaited := !sess.Close(0)
		<-expired
		if !quitWaited {
			t.Fatal("quitting did not wait for the release of a store whose time ran out meanwhile")
		}
	}
}

// Open Anyway takes a waiting store's lock over in the background. Until
// that ends the store stays the session's: being let go (quit, its time
// running out, another store held) zeroes its key at once, but its Close
// (which must not run during the takeover) waits for Idle, and quitting
// waits for that Close.
func TestSession_ABusyStoreLetGoIsClosedOnceIdle(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"quit", "time runs out", "hold another"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			clock := &fakeClock{}
			sess := newSession[*slowStore](clock.factory, func(f func()) { f() }, inline,
				func() time.Duration { return sessionTimeout }, sessionCallbacks{Warn: func() {}, TimedOut: func() {}})
			st := newSlowStore()
			expired := 0
			sess.Hold(st, func() { expired++ })
			if !sess.Busy(st) {
				t.Fatal("a waiting store can be made busy")
			}

			switch how {
			case "quit":
				if sess.Close(20 * time.Millisecond) {
					t.Fatal("quitting should wait for the busy store's release")
				}
			case "time runs out":
				clock.active(sessionTimeout).f()
				if expired != 1 {
					t.Fatal("the store's time ran out; onExpire should run")
				}
			case "hold another":
				other := newSlowStore()
				other.release()
				sess.Hold(other, func() {})
			}
			if st.zeroed == 0 || st.closedAt != 0 {
				t.Fatalf("letting a busy store go zeroes its key at once and leaves Close for later (zeroed %d, closed %d)", st.zeroed, st.closedAt)
			}

			sess.Idle(st)
			if st.closedAt != 1 || sess.Take(st) {
				t.Fatalf("once idle, a store let go is closed (closed %d) and no longer the session's", st.closedAt)
			}
			if sess.Close(20 * time.Millisecond) {
				t.Fatal("quitting should wait for the store's release")
			}
			st.release()
			if !sess.Close(time.Second) {
				t.Fatal("once the store has released its lock, quitting goes ahead")
			}
		})
	}
}

func TestSession_ABusyStoreNotLetGoIsTheCallersOnceTaken(t *testing.T) {
	t.Parallel()
	sess, clock, _ := newTestSession(sessionTimeout)
	st, other := openTestStore(t), openTestStore(t)
	if sess.Busy(other) {
		t.Fatal("only a waiting store can be made busy")
	}
	sess.Hold(st, func() {})
	if !sess.Busy(st) {
		t.Fatal("a waiting store can be made busy")
	}
	sess.Idle(st)
	if isClosed(st) || sess.Pending() != st {
		t.Fatal("a store not let go stays waiting once idle")
	}
	if !sess.Take(st) || !clock.timers[0].stopped {
		t.Fatal("the store is then taken as usual")
	}
	sess.Idle(st) // again: does nothing
	if isClosed(st) {
		t.Fatal("a taken store is the caller's")
	}
}
