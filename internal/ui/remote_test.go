package ui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// plantLock writes vlt.lock as vlt on another computer would.
func plantLock(t *testing.T, dir string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"host": "laptop.local", "user": "me", "pid": 4242,
		"since": time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC), "token": "theirs",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vlt.lock"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func lockHolder(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "vlt.lock"))
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var lock struct{ Host string }
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	return lock.Host
}

func TestApp_UnlockTakesTheLockAndLockReleasesIt(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)

	unlock(app)
	host, _ := os.Hostname()
	if app.session.Store() == nil || lockHolder(t, dir) != host {
		t.Fatalf("unlocking should record this computer in vlt.lock, got %q", lockHolder(t, dir))
	}
	app.Lock()
	if got := lockHolder(t, dir); got != "" {
		t.Fatalf("locking should remove vlt.lock, held by %q", got)
	}
}

func TestApp_VaultOpenElsewhereAsksAndOpenAnywayTakesOver(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	plantLock(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)

	unlock(app)
	if app.session.Store() != nil || app.unlockScreen == nil {
		t.Fatal("the vault must not open before the user answers")
	}
	msg := findType[*widget.Label](w.Canvas().Overlays().Top())
	if msg == nil || !strings.Contains(msg.Text, "laptop.local") || !strings.Contains(msg.Text, "29 Oct 2026 09:30 UTC") {
		t.Fatalf("the dialog should say who has the vault open: %v", msg)
	}
	test.Tap(dialogButton(t, w, "Open Anyway"))

	host, _ := os.Hostname()
	if app.session.Store() == nil || lockHolder(t, dir) != host {
		t.Fatalf("Open Anyway should unlock and take the lock over (held by %q)", lockHolder(t, dir))
	}
	app.Lock()
}

func TestApp_VaultOpenElsewhereCancelLeavesItLocked(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	plantLock(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)

	unlock(app)
	test.Tap(dialogButton(t, w, "Cancel"))

	if app.session.Store() != nil || app.unlockScreen == nil {
		t.Fatal("Cancel must leave the vault locked on the unlock screen")
	}
	if got := lockHolder(t, dir); got != "laptop.local" {
		t.Fatalf("Cancel must leave the other copy's lock alone, held by %q", got)
	}
	if app.unlockScreen.unlockBtn.Disabled() {
		t.Fatal("the unlock screen should be usable again")
	}
}

func TestApp_UnlockScreenNamesTheVault(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	if findDeep(app.unlockScreen.Content(), func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Text == displayLocation(dir)
	}) == nil {
		t.Fatal("the unlock screen should show the vault's location")
	}
}

// fakeSSH puts an ssh on PATH that fails as an unreachable server does.
// Tests using it cannot run in parallel.
func fakeSSH(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ssh")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'ssh: connect to host nas port 22: Connection refused' >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH")) // the fake ssh comes first
}

func TestApp_UnreachableServerOffersRetryAndTheLocalVault(t *testing.T) {
	fakeSSH(t)
	a := test.NewTempApp(t)
	local := t.TempDir()
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, settingsAt(a, "nas:/srv/vault", withDefaultVault(local)), quietLog(), inlineRunner(), noTimers, testKDF)

	content := w.Content()
	if app.unlockScreen != nil || app.createScreen != nil {
		t.Fatal("an unreachable server must not offer to create a vault")
	}
	for _, want := range []string{"Cannot reach the vault's server", "Connection refused"} {
		if findDeep(content, func(o fyne.CanvasObject) bool {
			l, ok := o.(*widget.Label)
			return ok && strings.Contains(l.Text, want)
		}) == nil {
			t.Fatalf("the error screen should say %q", want)
		}
	}
	if findButton(content, "Retry") == nil {
		t.Fatal("expected Retry")
	}
	test.Tap(findButton(content, "Use the Vault on This Computer"))
	if app.settings.Vault() != localAt(local) || app.createScreen == nil {
		t.Fatalf("expected the create screen for %s, location now %q", local, app.settings.Vault())
	}
}

// A saved location that no longer parses (a rule tightened since it was
// saved) is shown with the reason, and the default vault is offered.
func TestApp_UnparsableSavedLocationShowsWhyAndOffersTheDefault(t *testing.T) {
	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	local := t.TempDir()
	a.Preferences().SetString(prefVaultDir, "nas:")
	app := newApp(a, w, LoadSettings(a.Preferences(), env(nil), local, quietLog()), quietLog(), inlineRunner(), noTimers, testKDF)

	want := "nas:\n\nInvalid vault location: \"nas:\" names no folder on the server"
	if findDeep(w.Content(), func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Text == want
	}) == nil {
		t.Fatalf("the error screen should name the location as saved and say why: %q", want)
	}
	test.Tap(findButton(w.Content(), "Use the Vault on This Computer"))
	if app.createScreen == nil {
		t.Fatal("expected the create screen for the default location")
	}
	test.Type(app.createScreen.entry, testPassphrase)
	test.Type(app.createScreen.confirm, testPassphrase)
	test.Tap(app.createScreen.createBtn)
	if got := LoadSettings(a.Preferences(), env(nil), "/elsewhere", quietLog()).Vault(); got != localAt(local) {
		t.Fatalf("after creating the default vault, saved location = %q, want %q", got, local)
	}
	app.Lock()
}

// The offer is for another vault: a current location that is the default,
// written another way, is not offered again.
func TestApp_UnreadableDefaultVaultIsNotOfferedAgain(t *testing.T) {
	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	file := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	newApp(a, w, settingsAt(a, "  "+file, withDefaultVault(file)), quietLog(), inlineRunner(), noTimers, testKDF)

	if findButton(w.Content(), "Retry") == nil {
		t.Fatal("expected the error screen")
	}
	if findButton(w.Content(), "Use the Vault on This Computer") != nil {
		t.Fatal("the current location is the default; it must not be offered as another vault")
	}
}

func TestApp_UnreachableServerFromTheEnvironmentOffersOnlyRetry(t *testing.T) {
	fakeSSH(t)
	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	newApp(a, w, LoadSettings(a.Preferences(), env(map[string]string{envVaultDir: "nas:/srv/vault"}), t.TempDir(), quietLog()),
		quietLog(), inlineRunner(), noTimers, testKDF)
	if findButton(w.Content(), "Use the Vault on This Computer") != nil {
		t.Fatal("VLT_VAULT_DIR fixes the location; no switch should be offered")
	}
}

func TestApp_ServerDialogValidatesAndSwitchesTheLocation(t *testing.T) {
	fakeSSH(t)
	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, settingsAt(a, t.TempDir()), quietLog(), inlineRunner(), noTimers, testKDF)
	if app.createScreen == nil || app.createScreen.serverBtn == nil {
		t.Fatal("the create screen should offer a vault on a server")
	}

	test.Tap(app.createScreen.serverBtn)
	entry := entryWithPlaceholder(t, w.Canvas().Overlays().Top(), "me@server.example:/home/me/vault")
	for _, bad := range []string{"/local/folder", "nas:", "-oProxyCommand=x:/v"} {
		if entry.Validator(bad) == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	test.Type(entry, "me@nas:/srv/vault")
	test.Tap(dialogButton(t, w, "Use This Location"))

	if got := app.settings.Vault().String(); got != "me@nas:/srv/vault" {
		t.Fatalf("location = %q", got)
	}
	if app.createScreen != nil {
		t.Fatal("the fake server is unreachable, so the error screen should show, not the create screen")
	}
}

func TestSettings_RefusesABadLocation(t *testing.T) {
	app, _ := newFlowApp(t)
	unlock(app)
	before := app.settings.Vault()
	ss := app.openSettings(app.session.Store(), app.sessionOps)
	box := ss.buildVaultLocation()
	path := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server")

	path.SetText("nas:")
	test.Tap(findButton(box, "Switch Vault (no copy)"))
	errLabel := findDeep(box, func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Visible() && strings.Contains(l.Text, "names no folder")
	})
	if errLabel == nil || app.settings.Vault() != before {
		t.Fatal("a location without a folder must be refused with a reason")
	}
	app.Lock()
}

func TestApp_CancelClosesTheWaitingVault(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	plantLock(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)

	unlock(app)
	waiting := app.session.Pending()
	if waiting == nil {
		t.Fatal("the opened vault should wait for an answer")
	}
	test.Tap(dialogButton(t, w, "Cancel"))
	select {
	case <-waiting.Closed():
	default:
		t.Fatal("Cancel must close the waiting vault (its key is in memory)")
	}
	if app.session.Pending() != nil {
		t.Fatal("nothing should be pending after Cancel")
	}
}

func TestApp_UnansweredOpenElsewhereDialogLocksAfterTheTimeout(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	plantLock(t, dir)
	w := test.NewTempWindow(t, nil)
	clock := &fakeClock{}
	app := newApp(a, w, settingsAt(a, dir, withLockTimeoutMs(60_000)), quietLog(), inlineRunner(), clock.factory, testKDF)

	unlock(app)
	waiting := app.session.Pending()
	timer := clock.active(time.Minute)
	if waiting == nil || timer == nil {
		t.Fatal("the dialog should be guarded by the auto-lock timeout")
	}
	timer.f()
	select {
	case <-waiting.Closed():
	default:
		t.Fatal("an unanswered dialog must close the vault after the timeout")
	}
	if w.Canvas().Overlays().Top() != nil || app.unlockScreen.unlockBtn.Disabled() {
		t.Fatal("the dialog should be gone and the unlock screen usable")
	}
	if got := lockHolder(t, dir); got != "laptop.local" {
		t.Fatalf("the other copy's lock must be left alone, held by %q", got)
	}
}

func TestApp_QuitReleasesTheLock(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)
	if lockHolder(t, dir) == "" {
		t.Fatal("expected a lock while unlocked")
	}
	app.session.Close(time.Second) // what quit does before exiting
	if got := lockHolder(t, dir); got != "" {
		t.Fatalf("quitting while unlocked must remove vlt.lock, held by %q", got)
	}
}

// A location saved by an older version, or typed by hand, may be written
// differently from the one entered; it is still the same place.
func TestSettings_SwitchToTheSameLocationWrittenDifferently(t *testing.T) {
	app, _ := newFlowApp(t)
	unlock(app)
	app.settings.useLocation("ssh://me@nas:2222/srv/my vault") // as saved by an older version
	ss := app.openSettings(app.session.Store(), app.sessionOps)
	box := ss.buildVaultLocation()
	path := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server")

	path.SetText("ssh://me@nas:2222/srv/my%20vault")
	test.Tap(findButton(box, "Switch Vault (no copy)"))
	if findDeep(box, func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Visible() && l.Text == "Already using that vault directory"
	}) == nil {
		t.Fatal("an escaped form of the current location should count as the same")
	}
	app.Lock()
}

// runNext runs the first piece of work q holds.
func runNext(t *testing.T, q *queue) {
	t.Helper()
	if len(q.pending) == 0 {
		t.Fatal("no work queued")
	}
	next := q.pending[0]
	q.pending = q.pending[1:]
	next()
}

// queuedLockApp is an app over a vault in dir with a queued runner, showing
// the unlock screen (or the create screen, for a missing vault).
func queuedLockApp(t *testing.T, dir string) (*App, fyne.Window, *queue) {
	t.Helper()
	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	q := &queue{}
	app := newApp(a, w, settingsAt(a, dir), quietLog(), q.runner(), noTimers, testKDF)
	q.flush()
	return app, w, q
}

// Quitting while "Open Anyway" takes the lock over: the store being taken
// over is still the session's, so quitting waits for its release, which
// follows the takeover.
func TestApp_QuitDuringATakeoverWaitsForTheStoresRelease(t *testing.T) {
	dir := t.TempDir()
	newVaultWith(t, dir)
	plantLock(t, dir)
	app, w, q := queuedLockApp(t, dir)

	unlock(app)
	runNext(t, q)
	waiting := app.session.Pending()
	test.Tap(dialogButton(t, w, "Open Anyway"))

	if app.session.Close(0) {
		t.Fatal("quitting must wait for the vault being taken over")
	}
	if isClosedChan(waiting.Closed()) {
		t.Fatal("the store must not be closed while its lock is taken over")
	}
	q.flush() // the takeover ends; the store is then closed

	if !isClosedChan(waiting.Closed()) || app.session.Store() != nil {
		t.Fatal("after quitting, the store taken over is closed and the vault stays locked")
	}
	if got := lockHolder(t, dir); got != "" {
		t.Fatalf("the lock taken over should be released, held by %q", got)
	}
	if !app.session.Close(time.Second) {
		t.Fatal("quitting should then find every store released")
	}
}
