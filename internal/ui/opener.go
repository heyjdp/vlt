package ui

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/heyjdp/vlt/internal/store"
)

// opener takes the person from a vault's location to its session: it looks
// at the location (is a vault there?), checks a folder chosen with Open
// Existing Vault, unlocks or creates the vault with the passphrase, and
// reports one outcome for each attempt. A vault open elsewhere becomes the
// Waiting vault, which the opener holds until the person answers: Open
// Anyway takes its lock over and begins the session, while Cancel, the
// auto-lock timeout passing first, or Abandon let it go.
//
// Work is done for the screen shown when it started. Abandon, called when
// that screen is replaced (or vlt quits), drops every result still to come:
// a store opened or created meanwhile is let go, never shown. Every store
// the opener lets go goes through session.Discard, which zeroes its key at
// once and closes it off the caller's goroutine, and which session.Close
// (quitting) waits for, a bounded time.
//
// Unlock, Create, CheckFolder, Look, OpenAnyway, Cancel and Abandon run on
// the main goroutine, and so do the busy and report functions they are
// given. The Waiting vault's timer fires on its own goroutine: it lets the
// vault go there, so a busy main goroutine never keeps the key past the
// timeout, and reports through post. The opener's mutex guards the screen
// and the Waiting vault; a store leaves the wait only under it, straight
// into the session or to session.Discard, so quitting always finds it.
type opener struct {
	session   *session[*store.Store]
	run       runner
	post      func(func())
	newTimer  timerFactory
	timeout   func() time.Duration // the auto-lock timeout; 0 = off
	log       *slog.Logger
	storeOpts []store.Option

	mu sync.Mutex
	// screen identifies the screen work is done for; Abandon moves on.
	screen int
	// waiting is the Waiting vault (nil when there is none), asked about
	// for the attempt waitingFor, with waitTimer running out its time.
	waiting    *store.Store
	waitingFor attempt
	waitTimer  stoppableTimer
}

// attempt is one Unlock or Create: the screen it started from, what marks
// that screen busy while work runs, and what receives its outcomes.
type attempt struct {
	screen int
	busy   func(bool)
	report func(openOutcome)
}

// openOutcomeKind is what came of an attempt to open a vault.
type openOutcomeKind int

const (
	// vaultOpened: the session has begun on the vault (openOutcome.store,
	// openOutcome.ctx).
	vaultOpened openOutcomeKind = iota + 1
	// vaultWaiting: the vault is open elsewhere (openOutcome.holder). It
	// waits for OpenAnyway or Cancel.
	vaultWaiting
	// waitTimedOut: the Waiting vault was let go because no answer came
	// within the auto-lock timeout.
	waitTimedOut
	// vaultGone: no vault is at the location any more.
	vaultGone
	// wrongPassphrase: the passphrase does not unlock the vault.
	wrongPassphrase
	// nothingToUnlock: files of a vault are left at the location, but no
	// vault key or secrets.
	nothingToUnlock
	// keyMissing: the vault's key file (vlt.age) is missing.
	keyMissing
	// unlockFailed: the vault could not be opened (openOutcome.err).
	unlockFailed
	// createFailed: the vault could not be created (openOutcome.err).
	createFailed
)

// openOutcome is what Unlock and Create report: one kind, and what goes
// with it.
type openOutcome struct {
	kind   openOutcomeKind
	store  *store.Store    // vaultOpened
	ctx    context.Context // vaultOpened: the session's context, cancelled when it ends
	holder *store.LockInfo // vaultWaiting (zero if unknown)
	dir    string          // nothingToUnlock
	err    error           // unlockFailed, createFailed
}

// problem is what the screen an attempt started from says about an outcome
// that leaves it there with an error, or "" for any other outcome.
func (o openOutcome) problem() string {
	switch o.kind {
	case wrongPassphrase:
		return "Incorrect passphrase"
	case nothingToUnlock:
		return "No vault key or secrets found in " + o.dir
	case keyMissing:
		return "This vault's key file (vlt.age) is missing, so its secrets cannot be decrypted. If moving the vault here was cut short, switch back to the original vault and move it here again (if that vault changed since, delete this folder first); otherwise restore vlt.age from a backup."
	case unlockFailed:
		if isServerError(o.err) {
			return errorMessage("", o.err)
		}
		return errorMessage("Couldn't open the vault", o.err)
	case createFailed:
		return errorMessage("Couldn't create the vault", o.err)
	default:
		return ""
	}
}

// newOpener returns an opener that hands the vaults it opens to sess. Work
// runs on run; post runs the timeout's report on the main goroutine;
// timers and timeout time the Waiting vault (no timer while timeout returns
// 0); storeOpts go to store.Exists, Open and Create.
func newOpener(sess *session[*store.Store], run runner, post func(func()), timers timerFactory, timeout func() time.Duration, log *slog.Logger, storeOpts ...store.Option) *opener {
	return &opener{session: sess, run: run, post: post, newTimer: timers, timeout: timeout, log: log, storeOpts: storeOpts}
}

// Abandon is called when the screen work was started for is replaced, or
// vlt quits: results still to come are dropped (stores opened or created
// meanwhile are let go), and so is the Waiting vault. An answer to the
// question after that does nothing.
func (o *opener) Abandon() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.screen++
	o.letGoWaitingLocked()
}

// Look reports, to report, whether a vault is at the location dir.
func (o *opener) Look(dir string, report func(exists bool, err error)) {
	forScreen(o,
		func(ctx context.Context) (bool, error) { return store.Exists(ctx, dir, o.storeOpts...) },
		report, nil)
}

// CheckFolder reports, to report, whether the folder dir, chosen to open,
// holds a vault, or else which of its subfolders does (see checkFolder).
// busy marks the screen busy meanwhile.
func (o *opener) CheckFolder(dir string, busy func(bool), report func(folderCheck, error)) {
	busy(true)
	forScreen(o,
		func(ctx context.Context) (folderCheck, error) { return checkFolder(ctx, dir) },
		func(found folderCheck, err error) {
			busy(false)
			report(found, err)
		}, nil)
}

// unlockResult is what Unlock's background work found.
type unlockResult struct {
	store *store.Store
	// leftovers reports, after ErrVaultNotFound, whether files of a vault
	// remain (so the error is shown rather than offering to create one).
	leftovers bool
}

// Unlock opens the vault at dir with passphrase in the background (one
// scrypt, or a migration), with the screen busy meanwhile, and reports the
// outcome: the screen is not busy by then.
func (o *opener) Unlock(dir, passphrase string, busy func(bool), report func(openOutcome)) {
	busy(true)
	forScreen(o,
		func(ctx context.Context) (unlockResult, error) {
			s, err := store.Open(ctx, dir, passphrase, o.log, o.storeOpts...)
			if errors.Is(err, store.ErrVaultNotFound) {
				leftovers, _ := store.Exists(ctx, dir, o.storeOpts...)
				return unlockResult{leftovers: leftovers}, err
			}
			return unlockResult{store: s}, err
		},
		func(r unlockResult, err error) {
			busy(false)
			if err != nil {
				report(unlockFailure(dir, r.leftovers, err))
				return
			}
			o.opened(r.store, busy, report)
		},
		func(r unlockResult) { o.letGo(r.store) })
}

// unlockFailure is the outcome of an unlock that failed with err.
func unlockFailure(dir string, leftovers bool, err error) openOutcome {
	switch {
	case errors.Is(err, store.ErrWrongPassphrase):
		return openOutcome{kind: wrongPassphrase}
	case errors.Is(err, store.ErrVaultNotFound) && leftovers:
		return openOutcome{kind: nothingToUnlock, dir: dir}
	case errors.Is(err, store.ErrVaultNotFound):
		return openOutcome{kind: vaultGone}
	case errors.Is(err, store.ErrKeyMissing):
		return openOutcome{kind: keyMissing}
	default:
		return openOutcome{kind: unlockFailed, err: err}
	}
}

// Create creates a vault at dir with passphrase in the background, with
// the screen busy meanwhile, and reports the outcome as Unlock does.
func (o *opener) Create(dir, passphrase string, busy func(bool), report func(openOutcome)) {
	busy(true)
	forScreen(o,
		func(ctx context.Context) (*store.Store, error) {
			return store.Create(ctx, dir, passphrase, o.log, o.storeOpts...)
		},
		func(s *store.Store, err error) {
			busy(false)
			if err != nil {
				report(openOutcome{kind: createFailed, err: err})
				return
			}
			o.opened(s, busy, report)
		},
		o.letGo)
}

// opened begins the session on s, just opened or created for the current
// screen, unless another copy of vlt has the vault open: then s becomes the
// Waiting vault.
func (o *opener) opened(s *store.Store, busy func(bool), report func(openOutcome)) {
	holder, elsewhere := s.OpenElsewhere()
	if !elsewhere {
		report(openOutcome{kind: vaultOpened, store: s, ctx: o.session.Begin(s)})
		return
	}
	o.waitFor(s, attempt{busy: busy, report: report})
	report(openOutcome{kind: vaultWaiting, holder: &holder})
}

// waitFor makes s the Waiting vault, asked about for a on the current
// screen, and starts its timer (none while the timeout is off). A vault
// already waiting is let go.
func (o *opener) waitFor(s *store.Store, a attempt) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.letGoWaitingLocked()
	a.screen = o.screen
	o.waiting, o.waitingFor = s, a
	if d := o.timeout(); d > 0 {
		o.waitTimer = o.newTimer(d, func() { o.expire(s) })
	}
}

// expire lets the Waiting vault s go when its time runs out, on the
// timer's goroutine, and then reports waitTimedOut on the main goroutine,
// unless s is no longer waiting (answered, or abandoned) or the screen has
// been abandoned since.
func (o *opener) expire(s *store.Store) {
	o.mu.Lock()
	if o.waiting != s {
		o.mu.Unlock()
		return
	}
	a := o.waitingFor
	o.letGoWaitingLocked()
	o.mu.Unlock()
	o.post(func() {
		if o.current(a.screen) {
			a.busy(false)
			a.report(openOutcome{kind: waitTimedOut})
		}
	})
}

// OpenAnyway answers the question about the Waiting vault: its lock is
// taken over in the background, with the screen busy, and then the session
// begins on it (reported as vaultOpened). A failed takeover is logged and
// the vault opens all the same: the lock only warns. Until the takeover
// ends the vault is still waiting, so the timeout and Abandon still let it
// go (its Close then waits for the takeover). With no Waiting vault (it
// was let go already) it does nothing.
func (o *opener) OpenAnyway() {
	o.mu.Lock()
	s, a := o.waiting, o.waitingFor
	o.mu.Unlock()
	if s == nil {
		return
	}
	a.busy(true)
	background(o.run,
		func(ctx context.Context) (none, error) { return none{}, s.TakeOver(ctx) },
		func(_ none, err error) {
			o.mu.Lock()
			if o.waiting != s {
				o.mu.Unlock()
				return // let go meanwhile (timed out, abandoned)
			}
			o.stopWaitTimerLocked()
			o.waiting, o.waitingFor = nil, attempt{}
			ctx := o.session.Begin(s)
			o.mu.Unlock()
			a.busy(false)
			if err != nil {
				o.log.Warn("cannot take over the vault lock", "err", err)
			}
			a.report(openOutcome{kind: vaultOpened, store: s, ctx: ctx})
		})
}

// Cancel answers the question about the Waiting vault: it is let go, and
// nothing is reported.
func (o *opener) Cancel() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.letGoWaitingLocked()
}

// current reports whether screen is still the one work is done for.
func (o *opener) current(screen int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return screen == o.screen
}

// letGo lets go of s, if not nil, as the session lets go of its own.
func (o *opener) letGo(s *store.Store) {
	if s != nil {
		o.session.Discard(s)
	}
}

// letGoWaitingLocked lets the Waiting vault go, if there is one, and stops
// its timer. The caller holds o.mu.
func (o *opener) letGoWaitingLocked() {
	o.stopWaitTimerLocked()
	if o.waiting != nil {
		o.session.Discard(o.waiting)
		o.waiting, o.waitingFor = nil, attempt{}
	}
}

// stopWaitTimerLocked stops the Waiting vault's timer. The caller holds
// o.mu.
func (o *opener) stopWaitTimerLocked() {
	if o.waitTimer != nil {
		o.waitTimer.Stop()
		o.waitTimer = nil
	}
}

// forScreen runs work through o.run for the screen shown now, then passes
// its result to done on the main goroutine, or, if that screen has been
// abandoned meanwhile, to drop (when not nil) instead.
func forScreen[T any](o *opener, work func(context.Context) (T, error), done func(T, error), drop func(T)) {
	o.mu.Lock()
	screen := o.screen
	o.mu.Unlock()
	background(o.run, work, func(v T, err error) {
		if !o.current(screen) {
			if drop != nil {
				drop(v)
			}
			return
		}
		done(v, err)
	})
}

// folderCheck is what checkFolder found in a folder chosen to open.
type folderCheck struct {
	vault bool   // the folder holds a vault
	inner string // otherwise, a direct subfolder that holds one, if any
}

// checkFolder reports whether dir holds a vault and, if not, whether one of
// its direct subfolders does: picking the folder above a vault is an easy
// slip. Unreadable subfolders are skipped in that search.
func checkFolder(ctx context.Context, dir string) (folderCheck, error) {
	exists, err := store.Exists(ctx, dir)
	if err != nil || exists {
		return folderCheck{vault: exists}, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return folderCheck{}, nil
	}
	if err != nil {
		return folderCheck{}, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		inner := filepath.Join(dir, e.Name())
		if ok, err := store.Exists(ctx, inner); err == nil && ok {
			return folderCheck{inner: inner}, nil
		}
	}
	return folderCheck{}, nil
}
