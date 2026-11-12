package ui

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

// openerRig is an opener over a real session, without a window: work and
// every store's Close wait in a queue the test runs, timers fire by hand,
// and the timeout's report waits in posted until the test runs it, as
// fyne.Do hands it to the main goroutine.
type openerRig struct {
	o        *opener
	sess     *session[*store.Store]
	q        *queue
	clock    *fakeClock
	posted   []func()
	outcomes []openOutcome
	busy     []bool // every busy(…) call, in order
}

// newOpenerRig returns a rig whose auto-lock timeout is timeout (0 = off).
// Background results are delivered through fyne.Do, which fyne/test runs
// inline, so the rig sets up a test app (but no window).
func newOpenerRig(t *testing.T, timeout time.Duration) *openerRig {
	t.Helper()
	test.NewTempApp(t)
	r := &openerRig{q: &queue{}, clock: &fakeClock{}}
	limit := func() time.Duration { return timeout }
	r.sess = newSession[*store.Store](r.clock.factory, func(f func()) { f() }, r.q.runner().spawn, limit,
		sessionCallbacks{Warn: func() {}, TimedOut: func() {}})
	r.o = newOpener(r.sess, r.q.runner(), func(f func()) { r.posted = append(r.posted, f) },
		r.clock.factory, limit, quietLog(), testKDF)
	t.Cleanup(func() {
		r.o.Abandon()
		r.sess.End()
		r.q.flush()
	})
	return r
}

func (r *openerRig) setBusy(b bool)          { r.busy = append(r.busy, b) }
func (r *openerRig) report(o openOutcome)    { r.outcomes = append(r.outcomes, o) }
func (r *openerRig) unlock(dir, pass string) { r.o.Unlock(dir, pass, r.setBusy, r.report) }
func (r *openerRig) create(dir string)       { r.o.Create(dir, testPassphrase, r.setBusy, r.report) }

// last returns the latest outcome reported.
func (r *openerRig) last(t *testing.T) openOutcome {
	t.Helper()
	if len(r.outcomes) == 0 {
		t.Fatal("nothing was reported")
	}
	return r.outcomes[len(r.outcomes)-1]
}

// kinds returns the kinds of the outcomes reported so far.
func (r *openerRig) kinds() []openOutcomeKind {
	var kinds []openOutcomeKind
	for _, o := range r.outcomes {
		kinds = append(kinds, o.kind)
	}
	return kinds
}

// waiting unlocks the vault in a new folder that another computer has open,
// and runs the open, leaving the vault waiting for an answer.
func (r *openerRig) waiting(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	newVaultWith(t, dir)
	plantLock(t, dir)
	r.unlock(dir, testPassphrase)
	runNext(t, r.q)
	if got := r.last(t); got.kind != vaultWaiting || got.holder.Host != "laptop.local" {
		t.Fatalf("a vault open elsewhere should wait, naming who has it: %+v", got)
	}
	return dir
}

// letGoInTheBackground checks that a store the opener let go was handed to
// the session, whose release of it is still queued (so it did not run on
// the caller), and that once the queue runs the lock is holder's ("" for
// released) and quitting has nothing left to wait for.
func letGoInTheBackground(t *testing.T, sess *session[*store.Store], q *queue, dir, holder string) {
	t.Helper()
	if sess.Store() != nil {
		t.Fatal("a store let go must not be the session's")
	}
	if sess.Close(0) {
		t.Fatal("the store let go should be released through the session, in the background")
	}
	q.flush()
	if got := lockHolder(t, dir); got != holder {
		t.Fatalf("after its release the lock should be held by %q, held by %q", holder, got)
	}
	if !sess.Close(closeWait) {
		t.Fatal("quitting should find every store released")
	}
	if sess.Store() != nil {
		t.Fatal("the vault must stay locked")
	}
}

// A result for a screen since replaced (a vault switch, a relock) is
// dropped, and the store opened or created for it let go.
func TestOpener_AbandonDropsResultsStillToCome(t *testing.T) {
	host, _ := os.Hostname()
	for _, how := range []string{"unlock", "create"} {
		t.Run(how, func(t *testing.T) {
			r := newOpenerRig(t, sessionTimeout)
			dir := filepath.Join(t.TempDir(), "vault")
			if how == "unlock" {
				newVaultWith(t, dir)
				r.unlock(dir, testPassphrase)
			} else {
				r.create(dir)
			}
			r.o.Abandon()
			runNext(t, r.q) // the open or create finishes

			if len(r.outcomes) != 0 || !slices.Equal(r.busy, []bool{true}) {
				t.Fatalf("nothing should reach the replaced screen: outcomes %v, busy %v", r.kinds(), r.busy)
			}
			if got := lockHolder(t, dir); got != host {
				t.Fatalf("the store took the lock and holds it until its release runs, held by %q", got)
			}
			letGoInTheBackground(t, r.sess, r.q, dir, "")
		})
	}
}

func TestOpener_AbandonLetsTheWaitingVaultGo(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := r.waiting(t)
	timer := r.clock.active(sessionTimeout)

	r.o.Abandon()

	if !timer.stopped {
		t.Fatal("the vault let go needs no timer")
	}
	letGoInTheBackground(t, r.sess, r.q, dir, "laptop.local")
}

func TestOpener_LookSaysWhetherAVaultIsThere(t *testing.T) {
	vault, empty := t.TempDir(), t.TempDir()
	newVaultWith(t, vault)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, dir     string
		exists, isErr bool
	}{
		{"a vault", vault, true, false},
		{"an empty folder", empty, false, false},
		{"a file", file, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newOpenerRig(t, 0)
			reported := 0
			r.o.Look(tt.dir, func(exists bool, err error) {
				reported++
				if exists != tt.exists || (err != nil) != tt.isErr {
					t.Errorf("Look = %v, %v; want %v, error %v", exists, err, tt.exists, tt.isErr)
				}
			})
			r.q.flush()
			if reported != 1 {
				t.Fatalf("Look reported %d times, want once", reported)
			}
		})
	}
}

func TestOpener_LookAndFolderCheckForAnAbandonedScreenReportNothing(t *testing.T) {
	r := newOpenerRig(t, 0)
	dir := t.TempDir()
	newVaultWith(t, dir)
	r.o.Look(dir, func(bool, error) { t.Error("Look reported to a replaced screen") })
	r.o.CheckFolder(dir, r.setBusy, func(folderCheck, error) { t.Error("CheckFolder reported to a replaced screen") })
	r.o.Abandon()
	r.q.flush()
	if !slices.Equal(r.busy, []bool{true}) {
		t.Fatalf("a replaced screen is left alone, busy %v", r.busy)
	}
}

func TestOpener_CheckFolderMarksTheScreenBusyUntilItReports(t *testing.T) {
	r := newOpenerRig(t, 0)
	parent := t.TempDir()
	inner := filepath.Join(parent, "vault")
	newVaultWith(t, inner)
	var found folderCheck
	r.o.CheckFolder(parent, r.setBusy, func(f folderCheck, err error) {
		if !slices.Equal(r.busy, []bool{true, false}) {
			t.Errorf("the screen should be usable again when the result arrives, busy %v", r.busy)
		}
		found = f
		if err != nil {
			t.Error(err)
		}
	})
	if !slices.Equal(r.busy, []bool{true}) {
		t.Fatal("the screen should be busy while the folder is checked")
	}
	r.q.flush()
	if found != (folderCheck{inner: inner}) {
		t.Fatalf("found %+v, want the vault below", found)
	}
}

func TestOpener_UnlockBeginsTheSession(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := t.TempDir()
	newVaultWith(t, dir)

	r.unlock(dir, testPassphrase)
	if len(r.outcomes) != 0 || r.sess.Store() != nil || !slices.Equal(r.busy, []bool{true}) {
		t.Fatal("the vault opens in the background, with the screen busy")
	}
	runNext(t, r.q)

	got := r.last(t)
	if got.kind != vaultOpened || got.store == nil || r.sess.Store() != got.store {
		t.Fatalf("the session should begin on the opened vault: %+v", got)
	}
	if !slices.Equal(r.busy, []bool{true, false}) {
		t.Fatalf("the screen should be usable again before the outcome, busy %v", r.busy)
	}
	host, _ := os.Hostname()
	if lockHolder(t, dir) != host {
		t.Fatal("the opened vault holds the lock")
	}
	r.sess.End()
	if got.ctx.Err() == nil {
		t.Fatal("the outcome carries the session's context, cancelled when it ends")
	}
}

func TestOpener_UnlockSaysWhyItFailed(t *testing.T) {
	for _, tt := range []struct {
		name    string
		prepare func(t *testing.T, dir string) string // returns the location to unlock
		pass    string
		kind    openOutcomeKind
		problem string // a prefix of what the screen says
	}{
		{
			name: "wrong passphrase", pass: "not the passphrase", kind: wrongPassphrase, problem: "Incorrect passphrase",
			prepare: func(t *testing.T, dir string) string { newVaultWith(t, dir); return dir },
		},
		{
			name: "vault gone", kind: vaultGone,
			prepare: func(_ *testing.T, dir string) string { return dir },
		},
		{
			name: "only leftovers", kind: nothingToUnlock, problem: "No vault key or secrets found in ",
			prepare: func(t *testing.T, dir string) string {
				writeFile(t, filepath.Join(dir, "vlt.json"), "{}")
				return dir
			},
		},
		{
			name: "key file missing", kind: keyMissing, problem: "This vault's key file (vlt.age) is missing",
			prepare: func(t *testing.T, dir string) string {
				newVaultWith(t, dir, testutil.PasswordSecret("gh", "x", "work"))
				if err := os.Remove(filepath.Join(dir, "vlt.age")); err != nil {
					t.Fatal(err)
				}
				return dir
			},
		},
		{
			name: "damaged format marker", kind: unlockFailed, problem: "Couldn't open the vault: ",
			prepare: func(t *testing.T, dir string) string {
				newVaultWith(t, dir)
				writeFile(t, filepath.Join(dir, "vlt.json"), "not json")
				return dir
			},
		},
		{
			name: "unreachable server", kind: unlockFailed, problem: "Cannot connect to the vault's server nas: ",
			prepare: func(t *testing.T, _ string) string { fakeSSH(t); return "nas:/srv/vault" },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newOpenerRig(t, sessionTimeout)
			dir := tt.prepare(t, t.TempDir())
			if tt.pass == "" {
				tt.pass = testPassphrase
			}
			r.unlock(dir, tt.pass)
			r.q.flush()

			got := r.last(t)
			if got.kind != tt.kind || !strings.HasPrefix(got.problem(), tt.problem) {
				t.Fatalf("got kind %d saying %q; want kind %d saying %q…", got.kind, got.problem(), tt.kind, tt.problem)
			}
			if tt.kind == nothingToUnlock && got.problem() != "No vault key or secrets found in "+dir {
				t.Fatalf("the problem should name the folder: %q", got.problem())
			}
			if r.sess.Store() != nil || !r.sess.Close(0) {
				t.Fatal("a failed unlock leaves no store behind")
			}
			if !slices.Equal(r.busy, []bool{true, false}) {
				t.Fatalf("the screen should be usable again, busy %v", r.busy)
			}
		})
	}
}

// writeFile writes text to path.
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOpener_CreateBeginsTheSession(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := filepath.Join(t.TempDir(), "vault")

	r.create(dir)
	if exists, _ := store.Exists(t.Context(), dir); exists || !slices.Equal(r.busy, []bool{true}) {
		t.Fatal("the vault is created in the background, with the screen busy")
	}
	runNext(t, r.q)

	got := r.last(t)
	if got.kind != vaultOpened || r.sess.Store() != got.store || !slices.Equal(r.busy, []bool{true, false}) {
		t.Fatalf("the session should begin on the new vault: %+v, busy %v", got, r.busy)
	}
}

func TestOpener_CreateOverAVaultIsRefused(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := t.TempDir()
	newVaultWith(t, dir)

	r.create(dir)
	r.q.flush()

	got := r.last(t)
	if got.kind != createFailed || !strings.HasPrefix(got.problem(), "Couldn't create the vault: ") {
		t.Fatalf("got kind %d saying %q", got.kind, got.problem())
	}
	if r.sess.Store() != nil {
		t.Fatal("nothing should be unlocked")
	}
}

// Unlocking and creating share one path: either way, a vault open elsewhere
// waits for an answer, with the auto-lock timeout running.
func TestOpener_AVaultOpenElsewhereWaitsForAnAnswer(t *testing.T) {
	for _, how := range []string{"unlock", "create"} {
		t.Run(how, func(t *testing.T) {
			r := newOpenerRig(t, sessionTimeout)
			dir := t.TempDir()
			if how == "unlock" {
				newVaultWith(t, dir)
			}
			plantLock(t, dir)
			if how == "unlock" {
				r.unlock(dir, testPassphrase)
			} else {
				r.create(dir)
			}
			r.q.flush()

			got := r.last(t)
			if got.kind != vaultWaiting || got.holder.Host != "laptop.local" || got.holder.PID != 4242 {
				t.Fatalf("expected to wait, naming who has the vault open: %+v", got)
			}
			if r.sess.Store() != nil || r.clock.active(sessionTimeout) == nil {
				t.Fatal("no session yet, and the wait is timed")
			}
			if lockHolder(t, dir) != "laptop.local" {
				t.Fatal("the other copy's lock must be left alone while the person decides")
			}
		})
	}
}

func TestOpener_WithAutoLockOffTheVaultWaitsUntilAnswered(t *testing.T) {
	r := newOpenerRig(t, 0)
	r.waiting(t)
	if len(r.clock.running()) != 0 {
		t.Fatal("with auto-lock off nothing times the wait")
	}
}

func TestOpener_AnUnansweredQuestionLetsTheVaultGoAfterTheTimeout(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := r.waiting(t)
	busy := len(r.busy)

	r.clock.active(sessionTimeout).f()
	if r.sess.Close(0) {
		t.Fatal("the vault should be let go when the timeout passes, before the main goroutine hears of it")
	}
	if len(r.posted) != 1 || len(r.outcomes) != 1 {
		t.Fatal("the screen hears of it on the main goroutine")
	}
	r.posted[0]()

	if got := r.last(t); got.kind != waitTimedOut || !slices.Equal(r.busy[busy:], []bool{false}) {
		t.Fatalf("expected the timeout, with the screen usable: %+v, busy %v", got, r.busy[busy:])
	}
	r.o.OpenAnyway() // a late answer
	letGoInTheBackground(t, r.sess, r.q, dir, "laptop.local")
	if len(r.outcomes) != 2 {
		t.Fatalf("an answer after the timeout does nothing: %v", r.kinds())
	}
}

func TestOpener_ATimeoutNoticeForAnAbandonedScreenIsDropped(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	r.waiting(t)
	r.clock.active(sessionTimeout).f()
	r.o.Abandon() // the screen is replaced before the main goroutine runs the notice
	r.posted[0]()
	if len(r.outcomes) != 1 {
		t.Fatalf("a replaced screen hears nothing: %v", r.kinds())
	}
}

// A second vault waiting replaces the first, which is let go; the first's
// timer, already firing, leaves the second alone.
func TestOpener_ASecondWaitingVaultReplacesTheFirst(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	first := r.waiting(t)
	firstTimer := r.clock.active(sessionTimeout)
	r.waiting(t)

	if !firstTimer.stopped {
		t.Fatal("the first vault's wait should be over")
	}
	firstTimer.f()
	if len(r.posted) != 0 {
		t.Fatal("the first vault's timer must leave the second waiting")
	}
	letGoInTheBackground(t, r.sess, r.q, first, "laptop.local")
	r.o.OpenAnyway()
	r.q.flush()
	if got := r.last(t); got.kind != vaultOpened || r.sess.Store() != got.store {
		t.Fatalf("the second vault should still be waiting, and open: %+v", got)
	}
}

func TestOpener_OpenAnywayTakesOverAndBeginsTheSession(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := r.waiting(t)
	timer := r.clock.active(sessionTimeout)

	r.o.OpenAnyway()
	if r.busy[len(r.busy)-1] != true || r.sess.Store() != nil {
		t.Fatal("the lock is taken over in the background, with the screen busy")
	}
	runNext(t, r.q)

	got := r.last(t)
	if got.kind != vaultOpened || r.sess.Store() != got.store || r.busy[len(r.busy)-1] {
		t.Fatalf("the session should begin once the lock is taken over: %+v", got)
	}
	host, _ := os.Hostname()
	if lockHolder(t, dir) != host || !timer.stopped {
		t.Fatal("the lock is this computer's now, and the wait is over")
	}
	timer.f() // already firing when the takeover ended
	if r.sess.Store() != got.store || len(r.posted) != 0 {
		t.Fatal("the timer of an answered question must leave the vault alone")
	}
}

// A takeover that fails (here, the folder has become read-only) is logged,
// and the vault opens all the same: the lock only warns. The other copy's
// lock stays.
func TestOpener_AFailedTakeoverIsLoggedAndTheVaultOpens(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	var logged bytes.Buffer
	r.o.log = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dir := r.waiting(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	r.o.OpenAnyway()
	runNext(t, r.q)

	got := r.last(t)
	if got.kind != vaultOpened || r.sess.Store() != got.store || r.busy[len(r.busy)-1] {
		t.Fatalf("the vault should open although its lock could not be taken over: %+v", got)
	}
	if !strings.Contains(logged.String(), "cannot take over the vault lock") {
		t.Fatalf("the failed takeover should be logged, got %q", logged.String())
	}
	if lockHolder(t, dir) != "laptop.local" {
		t.Fatal("the other copy's lock should be left as it was")
	}
}

func TestOpener_CancelLetsTheWaitingVaultGo(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := r.waiting(t)
	timer := r.clock.active(sessionTimeout)
	reported := len(r.outcomes)

	r.o.Cancel()

	if len(r.outcomes) != reported || !timer.stopped {
		t.Fatal("Cancel reports nothing and stops the timer")
	}
	timer.f() // already firing
	if len(r.posted) != 0 {
		t.Fatal("the timer of an answered question does nothing")
	}
	letGoInTheBackground(t, r.sess, r.q, dir, "laptop.local")
}

// The screen is replaced, or vlt quits, while Open Anyway takes the lock
// over: the vault is let go at once, and its release, which waits for the
// takeover, is what quitting waits for.
func TestOpener_AbandonDuringATakeoverLetsTheVaultGoOnceItEnds(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := r.waiting(t)
	r.o.OpenAnyway()

	r.o.Abandon()
	if r.sess.Close(0) {
		t.Fatal("quitting must wait for the vault being taken over")
	}
	host, _ := os.Hostname()
	runNext(t, r.q) // the takeover ends
	if lockHolder(t, dir) != host || r.sess.Store() != nil {
		t.Fatal("the takeover ran, but the session must not begin")
	}
	letGoInTheBackground(t, r.sess, r.q, dir, "")
	if slices.Contains(r.kinds(), vaultOpened) {
		t.Fatal("an abandoned screen hears nothing")
	}
}

func TestOpener_OpenAnywayAfterTheVaultWasLetGoDoesNothing(t *testing.T) {
	r := newOpenerRig(t, sessionTimeout)
	dir := r.waiting(t)
	r.o.Abandon() // quitting, which leaves the dialog up
	busy := len(r.busy)

	r.o.OpenAnyway()

	if len(r.busy) != busy {
		t.Fatal("no takeover starts, so the screen is not made busy")
	}
	letGoInTheBackground(t, r.sess, r.q, dir, "laptop.local")
}

// The Waiting vault's time can run out while vlt quits. Whichever goes
// first, quitting must wait for that vault's release: the timer must not
// take the vault out of the quit's reach and only then let it go. The
// window is narrow; the loop finds it reliably under the race detector (go
// test -race).
func TestOpener_QuitWhileTheWaitingVaultTimesOutWaitsForItsRelease(t *testing.T) {
	t.Parallel()
	st := openTestStore(t) // let go again and again; its release never runs
	never := func(func()) {}
	for range 20000 {
		clock := &fakeClock{}
		limit := func() time.Duration { return sessionTimeout }
		sess := newSession[*store.Store](clock.factory, never, never, limit, sessionCallbacks{Warn: func() {}, TimedOut: func() {}})
		o := newOpener(sess, runner{spawn: never}, never, clock.factory, limit, quietLog())
		o.waitFor(st, attempt{busy: func(bool) {}, report: func(openOutcome) {}})
		expire := clock.active(sessionTimeout).f
		expired := make(chan struct{})
		go func() {
			defer close(expired)
			expire()
		}()
		o.Abandon()
		quitWaited := !sess.Close(0)
		<-expired
		if !quitWaited {
			t.Fatal("quitting did not wait for the release of a vault whose time ran out meanwhile")
		}
	}
}
