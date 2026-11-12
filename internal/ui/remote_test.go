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
	r := newAppRig(t)

	r.unlock()
	host, _ := os.Hostname()
	if r.locked() || lockHolder(t, r.dir) != host {
		t.Fatalf("unlocking should record this computer in vlt.lock, got %q", lockHolder(t, r.dir))
	}
	r.lock()
	if got := lockHolder(t, r.dir); got != "" {
		t.Fatalf("locking should remove vlt.lock, held by %q", got)
	}
}

func TestApp_VaultOpenElsewhereAsksAndOpenAnywayTakesOver(t *testing.T) {
	r := newAppRig(t)
	plantLock(t, r.dir)
	w := r.window

	r.unlock()
	if !r.locked() || r.shownScreen() != screenUnlock {
		t.Fatal("the vault must not open before the user answers")
	}
	msg := findType[*widget.Label](w.Canvas().Overlays().Top())
	if msg == nil || !strings.Contains(msg.Text, "laptop.local") || !strings.Contains(msg.Text, "29 Oct 2026 09:30 UTC") {
		t.Fatalf("the dialog should say who has the vault open: %v", msg)
	}
	test.Tap(dialogButton(t, w, "Open Anyway"))

	host, _ := os.Hostname()
	if r.locked() || lockHolder(t, r.dir) != host {
		t.Fatalf("Open Anyway should unlock and take the lock over (held by %q)", lockHolder(t, r.dir))
	}
	r.lock()
}

func TestApp_VaultOpenElsewhereCancelLeavesItLocked(t *testing.T) {
	r := newAppRig(t)
	plantLock(t, r.dir)

	r.unlock()
	test.Tap(dialogButton(t, r.window, "Cancel"))

	if !r.locked() || r.shownScreen() != screenUnlock {
		t.Fatal("Cancel must leave the vault locked on the unlock screen")
	}
	if got := lockHolder(t, r.dir); got != "laptop.local" {
		t.Fatalf("Cancel must leave the other copy's lock alone, held by %q", got)
	}
	if !r.usable() {
		t.Fatal("the unlock screen should be usable again")
	}
}

func TestApp_UnlockScreenNamesTheVault(t *testing.T) {
	r := newAppRig(t)
	if findDeep(r.unlockScreen().Content(), func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Text == displayLocation(r.dir)
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
	local := t.TempDir()
	r := newAppRig(t, atLocation("nas:/srv/vault"), withSettings(withDefaultVault(local)))

	content := r.window.Content()
	if shown := r.shownScreen(); shown == screenUnlock || shown == screenCreate {
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
	if r.settings.Vault() != localAt(local) || r.shownScreen() != screenCreate {
		t.Fatalf("expected the create screen for %s, location now %q", local, r.settings.Vault())
	}
}

// A saved location that no longer parses (a rule tightened since it was
// saved) is shown with the reason, and the default vault is offered.
func TestApp_UnparsableSavedLocationShowsWhyAndOffersTheDefault(t *testing.T) {
	local := t.TempDir()
	r := newAppRig(t, withLoadedSettings(func(prefs fyne.Preferences) *Settings {
		prefs.SetString(prefVaultDir, "nas:")
		return LoadSettings(prefs, env(nil), local, quietLog())
	}))
	w := r.window

	want := "nas:\n\nInvalid vault location: \"nas:\" names no folder on the server"
	if findDeep(w.Content(), func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Text == want
	}) == nil {
		t.Fatalf("the error screen should name the location as saved and say why: %q", want)
	}
	test.Tap(findButton(w.Content(), "Use the Vault on This Computer"))
	if r.shownScreen() != screenCreate {
		t.Fatal("expected the create screen for the default location")
	}
	r.createConfirming(testPassphrase)
	if got := LoadSettings(r.prefs(), env(nil), "/elsewhere", quietLog()).Vault(); got != localAt(local) {
		t.Fatalf("after creating the default vault, saved location = %q, want %q", got, local)
	}
	r.lock()
}

// The offer is for another vault: a current location that is the default,
// written another way, is not offered again.
func TestApp_UnreadableDefaultVaultIsNotOfferedAgain(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w := newAppRig(t, atLocation("  "+file), withSettings(withDefaultVault(file))).window

	if findButton(w.Content(), "Retry") == nil {
		t.Fatal("expected the error screen")
	}
	if findButton(w.Content(), "Use the Vault on This Computer") != nil {
		t.Fatal("the current location is the default; it must not be offered as another vault")
	}
}

func TestApp_UnreachableServerFromTheEnvironmentOffersOnlyRetry(t *testing.T) {
	fakeSSH(t)
	w := newAppRig(t, withLoadedSettings(func(prefs fyne.Preferences) *Settings {
		return LoadSettings(prefs, env(map[string]string{envVaultDir: "nas:/srv/vault"}), t.TempDir(), quietLog())
	})).window
	if findButton(w.Content(), "Use the Vault on This Computer") != nil {
		t.Fatal("VLT_VAULT_DIR fixes the location; no switch should be offered")
	}
}

func TestApp_ServerDialogValidatesAndSwitchesTheLocation(t *testing.T) {
	fakeSSH(t)
	r := newAppRig(t, withoutVault())
	w := r.window
	if r.shownScreen() != screenCreate || r.createScreen().serverBtn == nil {
		t.Fatal("the create screen should offer a vault on a server")
	}

	test.Tap(r.createScreen().serverBtn)
	entry := entryWithPlaceholder(t, w.Canvas().Overlays().Top(), "me@server.example:/home/me/vault")
	for _, bad := range []string{"/local/folder", "nas:", "-oProxyCommand=x:/v"} {
		if entry.Validator(bad) == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	test.Type(entry, "me@nas:/srv/vault")
	test.Tap(dialogButton(t, w, "Use This Location"))

	if got := r.settings.Vault().String(); got != "me@nas:/srv/vault" {
		t.Fatalf("location = %q", got)
	}
	if r.shownScreen() == screenCreate {
		t.Fatal("the fake server is unreachable, so the error screen should show, not the create screen")
	}
}

func TestSettings_RefusesABadLocation(t *testing.T) {
	r := newAppRig(t)
	r.unlock()
	before := r.settings.Vault()
	ss := r.openSettings()
	box := ss.buildVaultLocation()
	path := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server")

	path.SetText("nas:")
	test.Tap(findButton(box, "Switch Vault (no copy)"))
	errLabel := findDeep(box, func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Visible() && strings.Contains(l.Text, "names no folder")
	})
	if errLabel == nil || r.settings.Vault() != before {
		t.Fatal("a location without a folder must be refused with a reason")
	}
	r.lock()
}

func TestApp_UnansweredOpenElsewhereDialogLocksAfterTheTimeout(t *testing.T) {
	r := newAppRig(t, withSettings(withLockTimeoutMs(60_000)))
	plantLock(t, r.dir)
	w := r.window

	r.unlock()
	timer := r.clock.active(time.Minute)
	if w.Canvas().Overlays().Top() == nil || timer == nil {
		t.Fatal("the dialog should be guarded by the auto-lock timeout")
	}
	timer.f()
	if w.Canvas().Overlays().Top() != nil || r.shownScreen() != screenUnlock || !r.usable() {
		t.Fatal("the dialog should be gone and the unlock screen usable")
	}
	if got := lockHolder(t, r.dir); got != "laptop.local" {
		t.Fatalf("the other copy's lock must be left alone, held by %q", got)
	}
}

func TestApp_QuitReleasesTheLock(t *testing.T) {
	r := newAppRig(t)
	r.unlock()
	if lockHolder(t, r.dir) == "" {
		t.Fatal("expected a lock while unlocked")
	}
	r.app.closeVaults(closeWait) // what quit does before exiting
	if got := lockHolder(t, r.dir); got != "" {
		t.Fatalf("quitting while unlocked must remove vlt.lock, held by %q", got)
	}
}

// A location saved by an older version, or typed by hand, may be written
// differently from the one entered; it is still the same place.
func TestSettings_SwitchToTheSameLocationWrittenDifferently(t *testing.T) {
	r := newAppRig(t)
	r.unlock()
	r.settings.useLocation("ssh://me@nas:2222/srv/my vault") // as saved by an older version
	ss := r.openSettings()
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
	r.lock()
}

// Unlocking finished after the screen was replaced (a vault switch): the
// opened store is let go.
func TestApp_AnUnlockFinishingAfterAVaultSwitchIsDiscarded(t *testing.T) {
	r := newAppRig(t, withQueuedRunner())

	r.unlock()
	r.app.showUnlockScreen() // as switching vaults does, while scrypt runs
	r.runNext()              // the open finishes

	letGoInTheBackground(t, r.app.session, r.queue, r.dir, "")
}

// "Open Anyway" answered after the waiting store was let go (vlt is
// quitting, which leaves the dialog up) does nothing: no takeover starts,
// and the screen is not left busy.
func TestApp_OpenAnywayAfterTheWaitingStoreWasLetGoDoesNothing(t *testing.T) {
	r := newAppRig(t, withQueuedRunner())
	plantLock(t, r.dir)

	r.unlock()
	r.runNext()
	r.app.closeVaults(0) // quitting lets the waiting vault go
	test.Tap(dialogButton(t, r.window, "Open Anyway"))
	r.flush()

	if r.shownScreen() != screenUnlock || !r.usable() {
		t.Fatal("the unlock screen should not be left busy")
	}
	if got := lockHolder(t, r.dir); got != "laptop.local" {
		t.Fatalf("no takeover should run, the lock is held by %q", got)
	}
	if !r.app.closeVaults(closeWait) || !r.locked() {
		t.Fatal("the vault must stay locked, with every store released")
	}
}
