package ui

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/update"
)

// appRig is an App under test together with what it was built from: a
// headless Fyne app and window, its Settings, a fake clock, a runner that
// works inline or holds work in a queue, a scripted picker and a browser
// that records links. A test of App builds one with newAppRig and drives
// it through the verbs and queries below (unlock, lock, locked,
// shownScreen, …), so App's fields are known here, in one place; a test
// reaches into App only for the wiring it checks.
//
// Like every widget test, a rig test must not call t.Parallel (see
// helpers_test.go).
type appRig struct {
	t   *testing.T
	app *App
	// fyneApp is the test app: its clipboard and in-memory preferences.
	fyneApp  fyne.App
	window   fyne.Window
	settings *Settings
	// dir is the folder the rig chose for the vault, holding one unless
	// withoutVault; "" when the test chose the location.
	dir string
	// clock records the timers the app starts; tests fire them by hand.
	clock *fakeClock
	// queue holds background work while the runner is held.
	queue  *queue
	isHeld bool
	// pick answers the app's file and folder choosers (unless
	// withFyneDialogs).
	pick *scriptedPicker
	// opened records the links the app opened in the browser.
	opened []string
	// browserErr, if set, is what opening a link fails with.
	browserErr error
}

// rigConfig is what the rig options choose.
type rigConfig struct {
	secrets      []*store.Secret
	noVault      bool
	location     *string
	settingsFrom func(fyne.Preferences) *Settings
	settingsOpts []func(*Settings)
	queued       bool
	fyneDialogs  bool
	version      string
	browserErr   error
}

// rigOption changes how newAppRig builds the app.
type rigOption func(*rigConfig)

// withSecrets puts secrets in the rig's vault.
func withSecrets(secrets ...*store.Secret) rigOption {
	return func(c *rigConfig) { c.secrets = append(c.secrets, secrets...) }
}

// withoutVault leaves the rig's vault folder missing, so the app shows the
// create screen.
func withoutVault() rigOption {
	return func(c *rigConfig) { c.noVault = true }
}

// atLocation proposes the vault location text, as written (a prepared
// folder, a server, a bad location); the rig makes no vault.
func atLocation(text string) rigOption {
	return func(c *rigConfig) { c.location = &text }
}

// withSettings changes the settings the rig starts from (see settingsAt).
func withSettings(opts ...func(*Settings)) rigOption {
	return func(c *rigConfig) { c.settingsOpts = append(c.settingsOpts, opts...) }
}

// withLoadedSettings has the test load the settings itself from the test
// app's preferences (LoadSettings, with its environment and default
// location); the rig makes no vault.
func withLoadedSettings(load func(fyne.Preferences) *Settings) rigOption {
	return func(c *rigConfig) { c.settingsFrom = load }
}

// withQueuedRunner holds background work in the rig's queue until the test
// runs it. The look at the location is run, so the app starts on its first
// screen.
func withQueuedRunner() rigOption {
	return func(c *rigConfig) { c.queued = true }
}

// withVersion builds the app as version (none is a development build).
func withVersion(version string) rigOption {
	return func(c *rigConfig) { c.version = version }
}

// withFyneDialogs shows Fyne's own file and folder dialogs instead of the
// scripted picker.
func withFyneDialogs() rigOption {
	return func(c *rigConfig) { c.fyneDialogs = true }
}

// withFailingBrowser makes opening a link fail with err.
func withFailingBrowser(err error) rigOption {
	return func(c *rigConfig) { c.browserErr = err }
}

// newAppRig builds an App, by default over an empty vault in a new folder,
// with work run inline, a fake clock, a scripted picker, the cheap test
// work factor and settings that are all zero (auto-lock off), and shows
// its first screen.
func newAppRig(t *testing.T, opts ...rigOption) *appRig {
	t.Helper()
	var cfg rigConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	a := test.NewTempApp(t)
	r := &appRig{
		t: t, fyneApp: a, window: test.NewTempWindow(t, nil),
		clock: &fakeClock{}, queue: &queue{}, isHeld: cfg.queued,
		pick: &scriptedPicker{}, browserErr: cfg.browserErr,
	}
	switch {
	case cfg.settingsFrom != nil:
		r.settings = cfg.settingsFrom(a.Preferences())
	case cfg.location != nil:
		r.settings = settingsAt(a, *cfg.location, cfg.settingsOpts...)
	default:
		r.dir = filepath.Join(t.TempDir(), "vault")
		if !cfg.noVault {
			newVaultWith(t, r.dir, cfg.secrets...)
		}
		r.settings = settingsAt(a, r.dir, cfg.settingsOpts...)
	}
	deps := &AppDeps{
		FyneApp:   a,
		Window:    r.window,
		Settings:  r.settings,
		Logger:    quietLog(),
		Version:   cfg.version,
		run:       runner{spawn: r.spawn},
		timers:    r.clock.factory,
		open:      r.browse,
		storeOpts: []store.Option{testKDF},
	}
	if !cfg.fyneDialogs {
		deps.pick = r.pick
	}
	r.app = newApp(deps)
	r.flush() // the look at the location
	return r
}

// spawn is the app's runner: work runs at once, or waits in the queue
// while the runner is held.
func (r *appRig) spawn(f func()) {
	if r.isHeld {
		r.queue.pending = append(r.queue.pending, f)
		return
	}
	f()
}

// browse is the app's browser.
func (r *appRig) browse(u *url.URL) error {
	if r.browserErr != nil {
		return r.browserErr
	}
	r.opened = append(r.opened, u.String())
	return nil
}

// hold makes background work wait in the queue from now on.
func (r *appRig) hold() { r.isHeld = true }

// runInline runs background work at once from now on; work already queued
// still waits for flush.
func (r *appRig) runInline() { r.isHeld = false }

// flush runs the queued work, and any work it queues, in order.
func (r *appRig) flush() { r.queue.flush() }

// runNext runs the oldest piece of queued work.
func (r *appRig) runNext() {
	r.t.Helper()
	runNext(r.t, r.queue)
}

// queued is how many pieces of work wait in the queue.
func (r *appRig) queued() int { return len(r.queue.pending) }

// prefs are the test app's saved settings.
func (r *appRig) prefs() fyne.Preferences { return r.fyneApp.Preferences() }

// screenKind is a screen the app can show.
type screenKind int

const (
	screenUnknown screenKind = iota
	// screenChecking is shown while the app looks at the vault location.
	screenChecking
	screenUnlock
	screenCreate
	// screenVaultError says why the vault can't be used, with Retry.
	screenVaultError
	screenMain
)

func (s screenKind) String() string {
	switch s {
	case screenChecking:
		return "checking"
	case screenUnlock:
		return "unlock"
	case screenCreate:
		return "create"
	case screenVaultError:
		return "vault error"
	case screenMain:
		return "main view"
	default:
		return fmt.Sprintf("screen(%d)", int(s))
	}
}

// shownScreen is the screen the app shows. The test fails if the app holds
// more than one of the main view, the unlock screen and the create screen.
func (r *appRig) shownScreen() screenKind {
	r.t.Helper()
	held := 0
	for _, isHeld := range []bool{r.app.main != nil, r.app.unlockScreen != nil, r.app.createScreen != nil} {
		if isHeld {
			held++
		}
	}
	if held > 1 {
		r.t.Fatalf("the app holds %d screens at once (main %v, unlock %v, create %v)",
			held, r.app.main != nil, r.app.unlockScreen != nil, r.app.createScreen != nil)
	}
	switch {
	case r.app.main != nil:
		return screenMain
	case r.app.unlockScreen != nil:
		return screenUnlock
	case r.app.createScreen != nil:
		return screenCreate
	case findButton(r.window.Content(), "Retry") != nil:
		return screenVaultError
	case visibleText(r.window.Content(), "Checking vault…"):
		return screenChecking
	default:
		return screenUnknown
	}
}

// unlockScreen is the unlock screen, which must be shown.
func (r *appRig) unlockScreen() *UnlockScreen {
	r.t.Helper()
	if r.app.unlockScreen == nil {
		r.t.Fatalf("expected the unlock screen, the app shows the %v screen", r.shownScreen())
	}
	return r.app.unlockScreen
}

// createScreen is the create screen, which must be shown.
func (r *appRig) createScreen() *CreateScreen {
	r.t.Helper()
	if r.app.createScreen == nil {
		r.t.Fatalf("expected the create screen, the app shows the %v screen", r.shownScreen())
	}
	return r.app.createScreen
}

// unlock unlocks with testPassphrase on the unlock screen.
func (r *appRig) unlock() {
	r.t.Helper()
	r.unlockWith(testPassphrase)
}

// unlockWith types passphrase into the unlock screen and submits it.
func (r *appRig) unlockWith(passphrase string) {
	r.t.Helper()
	s := r.unlockScreen()
	test.Type(s.entry, passphrase)
	test.Tap(s.unlockBtn)
}

// unlockOpening unlocks with a held runner and runs only the open, leaving
// the first load of the list queued.
func (r *appRig) unlockOpening() {
	r.t.Helper()
	r.unlock()
	r.runNext()
	if r.app.main == nil {
		r.t.Fatal("expected the main view after the open completes")
	}
}

// createConfirming types testPassphrase into the create screen, confirm
// as its confirmation, and submits them.
func (r *appRig) createConfirming(confirm string) {
	r.t.Helper()
	s := r.createScreen()
	test.Type(s.entry, testPassphrase)
	test.Type(s.confirm, confirm)
	test.Tap(s.createBtn)
}

// screenError is the error shown on the unlock or create screen, or "".
func (r *appRig) screenError() string {
	r.t.Helper()
	var l *widget.Label
	switch r.shownScreen() {
	case screenUnlock:
		l = r.app.unlockScreen.errorLabel
	case screenCreate:
		l = r.app.createScreen.errorLabel
	default:
		r.t.Fatalf("no unlock or create screen to show an error, the app shows the %v screen", r.shownScreen())
	}
	if !l.Visible() {
		return ""
	}
	return l.Text
}

// passphraseLeft is the text left in the passphrase entries of the unlock
// or create screen.
func (r *appRig) passphraseLeft() string {
	r.t.Helper()
	switch r.shownScreen() {
	case screenUnlock:
		return r.app.unlockScreen.entry.Text
	case screenCreate:
		return r.app.createScreen.entry.Text + r.app.createScreen.confirm.Text
	default:
		r.t.Fatalf("no passphrase entry, the app shows the %v screen", r.shownScreen())
		return ""
	}
}

// screenInputs are the unlock or create screen's inputs and its progress
// bar.
func (r *appRig) screenInputs() ([]fyne.Disableable, *widget.ProgressBarInfinite) {
	r.t.Helper()
	switch r.shownScreen() {
	case screenUnlock:
		s := r.app.unlockScreen
		return []fyne.Disableable{s.entry, s.unlockBtn}, s.progress
	case screenCreate:
		s := r.app.createScreen
		inputs := []fyne.Disableable{s.entry, s.confirm, s.createBtn}
		for _, b := range []*widget.Button{s.openBtn, s.serverBtn} {
			if b != nil {
				inputs = append(inputs, b)
			}
		}
		return inputs, s.progress
	default:
		r.t.Fatalf("no unlock or create screen, the app shows the %v screen", r.shownScreen())
		return nil, nil
	}
}

// busy reports whether the unlock or create screen shows progress with
// every input disabled.
func (r *appRig) busy() bool {
	r.t.Helper()
	inputs, progress := r.screenInputs()
	for _, in := range inputs {
		if !in.Disabled() {
			return false
		}
	}
	return progress.Visible()
}

// usable reports whether the unlock or create screen shows no progress and
// every input is enabled.
func (r *appRig) usable() bool {
	r.t.Helper()
	inputs, progress := r.screenInputs()
	for _, in := range inputs {
		if in.Disabled() {
			return false
		}
	}
	return !progress.Visible()
}

// lock locks the vault, as the Lock button does.
func (r *appRig) lock() { r.app.Lock() }

// locked reports whether no vault is open.
func (r *appRig) locked() bool { return r.app.session.Store() == nil }

// store is the open vault's store; the vault must be unlocked.
func (r *appRig) store() *store.Store {
	r.t.Helper()
	s := r.app.session.Store()
	if s == nil {
		r.t.Fatal("expected the vault to be unlocked")
	}
	return s
}

// mainView is the main view, which must be shown.
func (r *appRig) mainView() *mainView {
	r.t.Helper()
	if r.app.main == nil {
		r.t.Fatalf("expected the main view, the app shows the %v screen", r.shownScreen())
	}
	return r.app.main
}

// listed are the secrets loaded into the main view's list.
func (r *appRig) listed() []*store.Secret {
	r.t.Helper()
	return r.mainView().list.secrets
}

// selectSecret selects row i of the list, showing that secret.
func (r *appRig) selectSecret(i int) {
	r.t.Helper()
	r.mainView().list.list.Select(i)
}

// search types text into the list's search box.
func (r *appRig) search(text string) {
	r.t.Helper()
	test.Type(r.mainView().list.searchEntry, text)
}

// detail is the detail panel's content.
func (r *appRig) detail() fyne.CanvasObject {
	r.t.Helper()
	return r.mainView().detail.Content()
}

// openSettings opens Settings, as its button does.
func (r *appRig) openSettings() *SettingsScreen {
	r.t.Helper()
	return r.mainView().openSettings()
}

// importFile imports f through Import File and the file chooser.
func (r *appRig) importFile(f fyne.URIReadCloser) {
	r.t.Helper()
	test.Tap(findButton(r.mainView().content(), "Import File"))
	r.pick.answerFile(r.t, f, nil)
}

// status is the status bar's text.
func (r *appRig) status() string { return r.app.statusBar.Text }

// lockWarningShown reports whether the auto-lock's warning is shown.
func (r *appRig) lockWarningShown() bool { return r.app.lockWarning != nil }

// stayUnlocked answers the lock warning with "Stay unlocked".
func (r *appRig) stayUnlocked() {
	r.t.Helper()
	if r.app.lockWarning == nil {
		r.t.Fatal("no lock warning is shown")
	}
	r.app.lockWarning.Confirm()
}

// lockNow answers the lock warning with "Lock now", which dismisses it.
func (r *appRig) lockNow() {
	r.t.Helper()
	if r.app.lockWarning == nil {
		r.t.Fatal("no lock warning is shown")
	}
	r.app.lockWarning.Hide()
}

// releases makes the app's release checker answer rel or err from now on,
// and returns how many times it has been asked.
func (r *appRig) releases(rel update.Release, err error) *int {
	calls := 0
	r.app.latest = func(context.Context) (update.Release, error) {
		calls++
		return rel, err
	}
	return &calls
}
