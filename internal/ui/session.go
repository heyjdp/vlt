package ui

import (
	"context"
	"sync"
	"time"
)

// sessionStore is what a session needs of an open vault: closing it, which
// zeroes its key at once, and a channel closed once it has released its
// lock and connection (a vault on a server does that in the background).
// *store.Store satisfies it; tests also use a store that never finishes.
type sessionStore interface {
	comparable
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

// session is the vault session: the open store, a store waiting on the
// "Vault Already Open" question, the session context, and the auto-lock
// countdown with its warning.
//
// Its methods may be called from any goroutine; its state is guarded by
// its own mutex. When the auto-lock timeout passes, or a waiting store's
// time runs out, the store is closed (its key zeroed) at once, on the
// timer's goroutine, so a busy main goroutine never keeps a key in memory
// past the timeout. Only then do the callbacks run, through post, which in
// production is fyne.Do, so they always run on the Fyne main goroutine. A
// timer that was already firing when activity arrived or the session ended
// does nothing, and neither does a waiting store's timer once the store
// has been taken.
//
// An operation that must not be cut off for want of input (moving the
// vault, changing its passphrase) pauses the auto-lock (Pause) while it
// runs; End still ends a paused session at once.
type session[S sessionStore] struct {
	newTimer timerFactory
	post     func(func())
	timeout  func() time.Duration // the auto-lock timeout, re-read on each restart; 0 = off
	cb       sessionCallbacks

	mu     sync.Mutex
	store  S // the zero S (nil) while locked
	cancel context.CancelFunc
	// gen counts ended sessions (see Gen and Current).
	gen int
	// timerGen identifies the current pair of auto-lock timers.
	timerGen  int
	lockTimer stoppableTimer
	warnTimer stoppableTimer
	// pending is a store opened but not shown while the user decides
	// whether to open a vault that is open elsewhere. Its key is in memory,
	// so it is closed when the auto-lock timeout passes without an answer.
	pending      S
	pendingTimer stoppableTimer
	// closing is the last ended store's Closed channel, which Close waits
	// for.
	closing <-chan struct{}
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
// auto-lock timeout (0 = off); post runs a callback on the main goroutine.
func newSession[S sessionStore](timers timerFactory, post func(func()), timeout func() time.Duration, cb sessionCallbacks) *session[S] {
	return &session[S]{newTimer: timers, post: post, timeout: timeout, cb: cb}
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

// Pending returns the store waiting on a question, or the zero S (nil).
func (ss *session[S]) Pending() S {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.pending
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

// End closes the open store (zeroing its key), cancels the session context
// and stops the countdown. Ending a session that isn't open only counts as
// an end for Current.
func (ss *session[S]) End() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.endLocked()
}

// Hold keeps s waiting on a question until Take. If the auto-lock timeout
// (when on) passes first, s is closed and onExpire runs (through post). A
// store already waiting is closed.
func (ss *session[S]) Hold(s S, onExpire func()) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.dropPendingLocked()
	ss.pending = s
	d := ss.timeout()
	if d <= 0 {
		return
	}
	ss.pendingTimer = ss.newTimer(d, func() {
		if ss.Take(s) {
			s.Close()
			ss.post(onExpire)
		}
	})
}

// Take ends the wait for s and reports whether s was still waiting. When it
// returns true the caller owns s; false means it was already closed (the
// timeout passed, or vlt is quitting).
func (ss *session[S]) Take(s S) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var none S
	if s == none || ss.pending != s {
		return false
	}
	ss.pending = none
	ss.stopPendingTimerLocked()
	return true
}

// Close ends the session and closes a waiting store, then waits up to wait
// for every closed store to release its lock and connection. It reports
// whether they all did in time; a dead network must not stop vlt quitting.
func (ss *session[S]) Close(wait time.Duration) bool {
	ss.mu.Lock()
	var done []<-chan struct{}
	var none S
	if ss.pending != none {
		done = append(done, ss.pending.Closed())
	}
	ss.dropPendingLocked()
	ss.endLocked()
	if ss.closing != nil {
		done = append(done, ss.closing)
	}
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
		ss.closing = ss.store.Closed()
		ss.store.Close()
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

// dropPendingLocked closes a waiting store and stops its timer. The caller
// holds ss.mu.
func (ss *session[S]) dropPendingLocked() {
	var none S
	if ss.pending != none {
		ss.pending.Close()
		ss.pending = none
	}
	ss.stopPendingTimerLocked()
}

// stopPendingTimerLocked stops a waiting store's timer. The caller holds
// ss.mu.
func (ss *session[S]) stopPendingTimerLocked() {
	if ss.pendingTimer != nil {
		ss.pendingTimer.Stop()
		ss.pendingTimer = nil
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
