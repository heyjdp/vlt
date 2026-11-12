package ui

import (
	"context"
	"slices"
	"sync"
	"time"
)

// sessionStore is what a session needs of an open vault: zeroing its key
// (ZeroKey, which must not block), closing it (Close, which releases its
// lock file and connection and may block on the vault's storage: a network
// folder or a server that stops answering, or a takeover of its lock still
// running), and a channel closed once Close has finished. The session is
// the one place that keeps Close from blocking its caller: it runs every
// Close through spawn. *store.Store satisfies it; tests also use
// stores that never finish.
type sessionStore interface {
	comparable
	ZeroKey()
	Close()
	Closed() <-chan struct{}
}

// sessionCallbacks are what a session reports to the app, on the main
// goroutine.
type sessionCallbacks struct {
	// Warn runs lockWarningLead before the auto-lock.
	Warn func()
	// TimedOut runs after the auto-lock has ended the session.
	TimedOut func()
}

// session is the vault session: the open store, the session context, and
// the auto-lock countdown with its warning.
//
// Its methods may be called from any goroutine; its state is guarded by
// its own mutex. Every store the session lets go of (on End, the auto-lock,
// Begin, Discard, Close) has its key zeroed at once and is detached under
// that mutex; its Close (the lock file's release, the connection) then
// runs through spawn, a new goroutine in production, so neither the main
// goroutine nor the mutex ever waits on the vault's storage. Close waits a
// bounded time for those releases. A store whose lock is being taken over
// (Open Anyway) is let go the same way: the store itself makes its Close
// wait for the takeover. When the auto-lock timeout passes, the key is
// zeroed at once, on the timer's goroutine, so a busy main goroutine never
// keeps a key in memory past the timeout. Only then do the callbacks run,
// through post, which in production is fyne.Do, so they always run on the
// Fyne main goroutine. A timer that was already firing when activity
// arrived or the session ended does nothing.
//
// An operation that must not be cut off for want of input (moving the
// vault, changing its passphrase) pauses the auto-lock (Pause) while it
// runs; End still ends a paused session at once.
type session[S sessionStore] struct {
	newTimer timerFactory
	post     func(func())
	// spawn runs a let-go store's Close without waiting for it: a new
	// goroutine in production; tests over local stores run it inline.
	spawn   func(func())
	timeout func() time.Duration // the auto-lock timeout, re-read on each restart; 0 = off
	cb      sessionCallbacks

	mu     sync.Mutex
	store  S // the zero S (nil) while locked
	cancel context.CancelFunc
	// gen counts ended sessions (see Gen and Current).
	gen int
	// timerGen identifies the current pair of auto-lock timers.
	timerGen  int
	lockTimer stoppableTimer
	warnTimer stoppableTimer
	// closing holds the Closed channels of stores let go of whose release
	// may still be running, which Close waits for.
	closing []<-chan struct{}
	// paused counts the pauses of this session not yet resumed (see Pause).
	paused int
}

// pausedLockAfter is how long the auto-lock waits without input while the
// session is paused (or the timeout, if longer). Moving a vault or changing
// its passphrase relies on its own failure to end: a server that stops
// answering is noticed within about 45 seconds (ServerAlive: 15 s, 3 times). This bound is
// for what nothing else ends, such as a hung network folder, so that it
// cannot keep the vault key in memory for ever.
const pausedLockAfter = time.Hour

// newSession returns a session with no vault open. timeout returns the
// auto-lock timeout (0 = off); post runs a callback on the main goroutine;
// spawn runs a let-go store's Close in the background.
func newSession[S sessionStore](timers timerFactory, post, spawn func(func()), timeout func() time.Duration, cb sessionCallbacks) *session[S] {
	return &session[S]{newTimer: timers, post: post, spawn: spawn, timeout: timeout, cb: cb}
}

// Begin opens the session on s and starts the auto-lock countdown. The
// context it returns is cancelled when the session ends. A session already
// open is ended first.
func (ss *session[S]) Begin(s S) context.Context {
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // kept in ss.cancel; End calls it
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var none S
	if ss.store != none {
		ss.endLocked()
	}
	ss.store, ss.cancel = s, cancel
	ss.restartLocked()
	return ctx
}

// Store returns the open store, or the zero S (nil) while locked.
func (ss *session[S]) Store() S {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.store
}

// Gen identifies the current session; pass it to Current later to learn
// whether the session has ended since.
func (ss *session[S]) Gen() int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.gen
}

// Current reports whether no session has ended since Gen returned gen.
func (ss *session[S]) Current(gen int) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return gen == ss.gen
}

// Activity restarts the auto-lock countdown, re-reading the timeout (so a
// change in Settings applies at once). It does nothing while locked.
func (ss *session[S]) Activity() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var none S
	if ss.store != none {
		ss.restartLocked()
	}
}

// Pause keeps the auto-lock from ending the session while an operation that
// must not be cut off runs (moving the vault, changing its passphrase), and
// returns what resumes it. Until every pause is resumed there is no warning,
// and the vault locks only after pausedLockAfter without input; then the
// countdown restarts in full. Resuming again, or after the session has
// ended, does nothing. End (a lock by hand, quitting) still ends a paused
// session at once. Pausing while locked does nothing.
func (ss *session[S]) Pause() (resume func()) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var none S
	if ss.store == none {
		return func() {}
	}
	ss.paused++
	ss.restartLocked()
	gen := ss.gen
	var once sync.Once
	return func() {
		once.Do(func() {
			ss.mu.Lock()
			defer ss.mu.Unlock()
			if gen != ss.gen {
				return // that session has ended; its pauses went with it
			}
			ss.paused--
			ss.restartLocked()
		})
	}
}

// End zeroes the open store's key and lets it go (its lock is released in
// the background), cancels the session context and stops the countdown.
// Ending a session that isn't open only counts as an end for Current.
func (ss *session[S]) End() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.endLocked()
}

// Discard zeroes the key of a store the caller owns and never began a
// session on (the opener's Waiting vault, or one opened for a screen since
// replaced) and closes it in the background; Close waits for it like the
// session's own.
func (ss *session[S]) Discard(s S) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.letGoLocked(s)
}

// Close ends the session, then waits up to wait for every store let go of
// (its own and those discarded) to release its lock and connection. It
// reports whether they all did in time; a dead network must not stop vlt
// quitting.
func (ss *session[S]) Close(wait time.Duration) bool {
	ss.mu.Lock()
	ss.endLocked()
	done := slices.Clone(ss.closing)
	ss.mu.Unlock()

	deadline := time.After(wait)
	for _, ch := range done {
		select {
		case <-ch:
		case <-deadline:
			return false
		}
	}
	return true
}

// endLocked is End with ss.mu held.
func (ss *session[S]) endLocked() {
	var none S
	if ss.store != none {
		ss.letGoLocked(ss.store)
		ss.store = none
	}
	if ss.cancel != nil {
		ss.cancel()
		ss.cancel = nil
	}
	ss.stopLocked()
	ss.paused = 0
	ss.gen++
}

// letGoLocked zeroes s's key now and closes s through spawn, keeping its
// Closed channel for Close (and dropping those already closed). The caller
// holds ss.mu and no longer refers to s.
func (ss *session[S]) letGoLocked(s S) {
	s.ZeroKey()
	ss.closing = slices.DeleteFunc(ss.closing, isClosedChan)
	ss.closing = append(ss.closing, s.Closed())
	ss.spawn(s.Close)
}

// isClosedChan reports whether ch is closed.
func isClosedChan(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// restartLocked starts a fresh countdown: the auto-lock after the timeout,
// and the warning lockWarningLead earlier when there is room for it; or,
// while paused, only the auto-lock after pausedLockAfter. The caller holds
// ss.mu.
func (ss *session[S]) restartLocked() {
	ss.stopLocked()
	d := ss.timeout()
	if d <= 0 {
		return
	}
	gen := ss.timerGen
	if ss.paused > 0 {
		ss.lockTimer = ss.newTimer(max(d, pausedLockAfter), func() { ss.expire(gen) })
		return
	}
	ss.lockTimer = ss.newTimer(d, func() { ss.expire(gen) })
	if w := warnDelay(d); w > 0 {
		ss.warnTimer = ss.newTimer(w, func() {
			ss.post(func() {
				ss.mu.Lock()
				current := gen == ss.timerGen
				ss.mu.Unlock()
				if current {
					ss.cb.Warn()
				}
			})
		})
	}
}

// expire ends the session for inactivity, on the timer's goroutine, if the
// timers of generation gen are still the current ones (ending the session,
// like activity, starts a new generation). It then tells the app through
// post, unless another session has ended since.
func (ss *session[S]) expire(gen int) {
	ss.mu.Lock()
	if gen != ss.timerGen {
		ss.mu.Unlock()
		return
	}
	ss.endLocked()
	ended := ss.gen
	ss.mu.Unlock()
	ss.post(func() {
		if ss.Current(ended) {
			ss.cb.TimedOut()
		}
	})
}

// stopLocked stops the countdown; timers already firing see a newer
// generation. The caller holds ss.mu.
func (ss *session[S]) stopLocked() {
	ss.timerGen++
	if ss.lockTimer != nil {
		ss.lockTimer.Stop()
		ss.lockTimer = nil
	}
	if ss.warnTimer != nil {
		ss.warnTimer.Stop()
		ss.warnTimer = nil
	}
}

// warnDelay returns when to warn before locking, or 0 for no warning (the
// timeout is too short to leave room for one).
func warnDelay(timeout time.Duration) time.Duration {
	if timeout < 2*lockWarningLead {
		return 0
	}
	return timeout - lockWarningLead
}
