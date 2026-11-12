package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

// Tests of the Settings screen (settings.go). Most open it through an
// unlocked app's own wiring; the Settings module itself (config.go) is
// tested in config_test.go.

// settingsApp unlocks an app on a vault holding one secret, built with
// opts, and opens Settings through the app's own wiring.
func settingsApp(t *testing.T, opts ...rigOption) (*appRig, *SettingsScreen) {
	t.Helper()
	r := newAppRig(t, append([]rigOption{withSecrets(testutil.PasswordSecret("gh", "x", "work"))}, opts...)...)
	r.unlock()
	ss := r.openSettings()
	return r, ss
}

// lockControls returns the auto-lock checkbox and slider.
func lockControls(ss *SettingsScreen) (*widget.Check, *widget.Slider) {
	box := ss.buildLockTimeout()
	return findType[*widget.Check](box), findType[*widget.Slider](box)
}

func TestSettings_EnvironmentValuesAreNotSavedBySliderChanges(t *testing.T) {
	dir := t.TempDir()
	newVaultWith(t, dir)
	r := newAppRig(t, withLoadedSettings(func(prefs fyne.Preferences) *Settings {
		return LoadSettings(prefs, env(map[string]string{envVaultDir: dir}), "/default", quietLog())
	}))
	r.unlock()
	ss := r.openSettings()

	ttl := findType[*widget.Slider](ss.buildClipboardTTL())
	ttl.SetValue(60)
	ttl.OnChangeEnded(60)

	restarted := restart(r.prefs())
	if restarted.Vault() != localAt("/default") {
		t.Fatalf("an env-provided vault dir leaked into saved settings: %v", restarted.Vault())
	}
	if restarted.ClipboardTTL() != time.Minute {
		t.Fatalf("the slider change itself should be saved, got %v", restarted.ClipboardTTL())
	}
	r.lock()
}

func TestSettings_SwitchedVaultIsSavedOnlyOnceOpened(t *testing.T) {
	r, ss := settingsApp(t)
	prefs := r.prefs()
	newDir := filepath.Join(t.TempDir(), "other")

	ss.ops.changeVaultNow(func() { ss.switchVault(localAt(newDir), widget.NewLabel("")) })
	if got := restart(prefs).Vault(); got == localAt(newDir) {
		t.Fatal("a switched-to path must not be saved before a vault is opened or created there")
	}

	if r.shownScreen() != screenCreate {
		t.Fatal("switching to an empty folder should show the create screen")
	}
	r.createConfirming(testPassphrase)

	if got := restart(prefs).Vault(); got != localAt(newDir) {
		t.Fatalf("after creating a vault there, the new path should be saved; got %v", got)
	}
	r.lock()
}

func TestSettings_MovedVaultIsRememberedAfterUnlock(t *testing.T) {
	r, ss := settingsApp(t)
	prefs := r.prefs()
	newDir := filepath.Join(t.TempDir(), "moved")

	ss.moveVault(localAt(newDir), widget.NewButton("Move Vault", nil), widget.NewLabel(""), widget.NewLabel(""))
	r.unlock() // the relock after a move; unlock at the new location

	if got := restart(prefs).Vault(); got != localAt(newDir) {
		t.Fatalf("after moving and unlocking, the vault dir should be saved as %q, got %v", newDir, got)
	}
	r.lock()
}

func TestSettings_BrowseFillsInTheChosenFolder(t *testing.T) {
	r, ss := settingsApp(t)
	box := ss.buildVaultLocation()
	dir := t.TempDir()
	folder, err := storage.ListerForURI(storage.NewFileURI(dir))
	if err != nil {
		t.Fatal(err)
	}

	test.Tap(findButton(box, "Browse…"))
	r.pick.answerFolder(t, folder, nil)

	if got := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").Text; got != dir {
		t.Fatalf("the location entry holds %q, want the chosen folder %q", got, dir)
	}
	r.lock()
}

// A move to an unfinished copy made before the vault last changed says
// what is there and what to do, rather than that a vault exists.
func TestSettings_AMoveToAnOutdatedUnfinishedCopySaysToDeleteIt(t *testing.T) {
	r, ss := settingsApp(t)
	box := ss.buildVaultLocation()
	files, err := filepath.Glob(filepath.Join(r.settings.Vault().Path, "work", "*.age"))
	if err != nil || len(files) != 1 {
		t.Fatalf("the vault's secret file: %v, %v", files, err)
	}
	stale := t.TempDir() // an unfinished copy: one secret, no vlt.age
	if err := os.Mkdir(filepath.Join(stale, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "work", filepath.Base(files[0])), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.store().Update(context.Background(), testutil.PasswordSecret("gh", "changed since", "work")); err != nil {
		t.Fatal(err)
	}

	entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(stale)
	tapAndConfirm(t, r.window, box, "Move Vault")

	if !visibleText(box, "unfinished copy of this vault") || !visibleText(box, "Delete that folder, then move the vault again") {
		t.Fatal("a move to an outdated unfinished copy should say to delete it, then move again")
	}
	r.lock()
}

func TestSettings_FailedMoveSaysWhyUnderTheLocation(t *testing.T) {
	r, ss := settingsApp(t)
	box := ss.buildVaultLocation()
	taken := t.TempDir()
	newVaultWith(t, taken) // a vault is already there

	entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(taken)
	tapAndConfirm(t, r.window, box, "Move Vault")

	if !visibleText(box, "Couldn't move the vault: vault already exists") {
		t.Fatal("a failed move should say why under the location")
	}
	if r.locked() || r.settings.Vault() == localAt(taken) {
		t.Fatal("a failed move must neither lock nor change the vault location")
	}
	r.lock()
}

func TestSettings_MoveOrSwitchWithoutATargetAsksForOne(t *testing.T) {
	tests := []struct {
		button, want string
	}{
		{"Move Vault", "Please enter or browse to a new vault directory"},
		{"Switch Vault (no copy)", "Please enter or browse to a vault directory"},
	}
	for _, tt := range tests {
		t.Run(tt.button, func(t *testing.T) {
			r, ss := settingsApp(t)
			box := ss.buildVaultLocation()
			entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText("  ")

			test.Tap(findButton(box, tt.button))

			if !visibleText(box, tt.want) || r.window.Canvas().Overlays().Top() != nil {
				t.Fatalf("an empty location should show %q and ask nothing", tt.want)
			}
			r.lock()
		})
	}
}

func TestSettings_LockTimeoutSliderIsSaved(t *testing.T) {
	r, ss := settingsApp(t, withSettings(withLockTimeoutMs(DefaultLockTimeoutMs)))

	_, slider := lockControls(ss)
	slider.SetValue(600_000)
	slider.OnChangeEnded(600_000)

	if got := restart(r.prefs()).LockTimeout(); got != 10*time.Minute {
		t.Fatalf("saved lock timeout = %v, want 10m", got)
	}
	r.lock()
}

func TestSettings_AutoLockCanBeTurnedOffAndOn(t *testing.T) {
	r, ss := settingsApp(t, withSettings(withLockTimeoutMs(DefaultLockTimeoutMs)))
	clock := r.clock
	r.mainView().tracker.instrument(r.detail())
	check, slider := lockControls(ss)
	r.mainView().tracker.instrument(check)

	check.SetChecked(false)
	if r.settings.LockTimeout() != 0 || !slider.Disabled() {
		t.Fatal("unticking Auto-lock should turn it off and disable the slider")
	}
	if restart(r.prefs()).LockTimeout() != 0 {
		t.Fatal("turning auto-lock off should be saved")
	}
	if clock.active(time.Duration(DefaultLockTimeoutMs)*time.Millisecond) != nil {
		t.Fatal("turning auto-lock off should stop the countdown")
	}

	check.SetChecked(true)
	if r.settings.LockTimeout() != DefaultLockTimeoutMs*time.Millisecond || slider.Disabled() {
		t.Fatal("ticking Auto-lock should restore the slider's timeout")
	}
	if clock.active(time.Duration(DefaultLockTimeoutMs)*time.Millisecond) == nil {
		t.Fatal("re-enabling auto-lock should arm the countdown straight away")
	}
	r.lock()
}

func TestSettings_EnvironmentControlledSettingsAreDisabled(t *testing.T) {
	a := test.NewTempApp(t)
	ss := &SettingsScreen{settings: LoadSettings(a.Preferences(), env(map[string]string{
		envLockTimeout: "120000", envClipboardTTL: "60",
	}), "/default", quietLog())}

	check, slider := lockControls(ss)
	ttl := ss.buildClipboardTTL()
	if !check.Disabled() || !slider.Disabled() || !findType[*widget.Slider](ttl).Disabled() {
		t.Fatal("settings set by environment variables should be disabled in Settings")
	}
	if !strings.Contains(findType[*widget.Label](ttl).Text, envClipboardTTL) {
		t.Fatal("the label should say which variable controls the setting")
	}
}

// queuedSettings is an app built with opts, unlocked on a vault holding
// one secret, whose background work waits in its queue, with Settings open
// and its vault location card built.
func queuedSettings(t *testing.T, opts ...rigOption) (*appRig, fyne.CanvasObject) {
	t.Helper()
	r := newAppRig(t, append([]rigOption{withQueuedRunner(), withSecrets(testutil.PasswordSecret("gh", "x", "work"))}, opts...)...)
	r.unlockOpening()
	r.flush() // the first load of the list
	return r, r.openSettings().buildVaultLocation()
}

// tapAndConfirm taps the button labelled text in box and, if it asks,
// answers Yes.
func tapAndConfirm(t *testing.T, w fyne.Window, box fyne.CanvasObject, text string) {
	t.Helper()
	test.Tap(findButton(box, text))
	if top := w.Canvas().Overlays().Top(); top != nil && findButton(top, "Yes") != nil {
		test.Tap(findButton(top, "Yes"))
	}
}

func TestSettings_MoveVaultIsNotStartedTwice(t *testing.T) {
	r, box := queuedSettings(t)
	w := r.window
	entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(filepath.Join(t.TempDir(), "moved"))

	tapAndConfirm(t, w, box, "Move Vault")
	tapAndConfirm(t, w, box, "Move Vault") // again, while the copy runs
	// Settings opened again shows fresh buttons; the copy still runs.
	again := r.openSettings().buildVaultLocation()
	entryWithPlaceholder(t, again, "Folder, or user@host:/path on an SSH server").SetText(filepath.Join(t.TempDir(), "other"))
	tapAndConfirm(t, w, again, "Move Vault")

	if r.queued() != 1 {
		t.Fatalf("%d copies started, want 1", r.queued())
	}
	if !findButton(box, "Move Vault").Disabled() {
		t.Fatal("Move Vault should be disabled while the vault is copied")
	}
	r.flush()
	r.lock()
}

func TestSettings_SwitchVaultWaitsForAMoveToFinish(t *testing.T) {
	r, box := queuedSettings(t)
	w := r.window
	dir := r.settings.Vault()
	path := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server")
	path.SetText(filepath.Join(t.TempDir(), "moved"))
	tapAndConfirm(t, w, box, "Move Vault")

	path.SetText(filepath.Join(t.TempDir(), "other"))
	tapAndConfirm(t, w, box, "Switch Vault (no copy)")

	if r.settings.Vault() != dir || r.locked() {
		t.Fatal("switching while the vault is being copied must wait, not lock and switch")
	}
	if r.status() != waitMessage {
		t.Fatalf("status bar says %q, want it to ask the user to wait", r.status())
	}
	r.flush()
	r.lock()
}

func TestSettings_PassphraseChangeWaitsForAMoveToFinish(t *testing.T) {
	r, box := queuedSettings(t)
	w := r.window
	entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(filepath.Join(t.TempDir(), "moved"))
	tapAndConfirm(t, w, box, "Move Vault")

	card := r.openSettings().buildChangePassphrase()
	test.Type(entryWithPlaceholder(t, card, "Current passphrase"), testPassphrase)
	test.Type(entryWithPlaceholder(t, card, "New passphrase"), "an even better passphrase")
	test.Type(entryWithPlaceholder(t, card, "Confirm new passphrase"), "an even better passphrase")
	test.Tap(findButton(card, "Change Passphrase"))

	if r.queued() != 1 {
		t.Fatalf("%d vault-wide operations running, want only the copy", r.queued())
	}
	r.flush()
	r.lock()
}

// The auto-lock waits while the vault is moved or its passphrase changed,
// however long that takes without input, and counts down in full after.
// (Each fails here, so the vault stays unlocked afterwards.)
func TestSettings_TheAutoLockWaitsForAMoveOrPassphraseChange(t *testing.T) {
	tests := []struct {
		name  string
		start func(t *testing.T, r *appRig, box fyne.CanvasObject)
	}{
		{"move", func(t *testing.T, r *appRig, box fyne.CanvasObject) {
			taken := t.TempDir()
			newVaultWith(t, taken)
			entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(taken)
			tapAndConfirm(t, r.window, box, "Move Vault")
		}},
		{"passphrase change", func(t *testing.T, r *appRig, _ fyne.CanvasObject) {
			card := r.openSettings().buildChangePassphrase()
			test.Type(entryWithPlaceholder(t, card, "Current passphrase"), "not the passphrase")
			test.Type(entryWithPlaceholder(t, card, "New passphrase"), "an even better passphrase")
			test.Type(entryWithPlaceholder(t, card, "Confirm new passphrase"), "an even better passphrase")
			test.Tap(findButton(card, "Change Passphrase"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, box := queuedSettings(t, withSettings(withLockTimeoutMs(DefaultLockTimeoutMs)))
			clock := r.clock
			timeout := r.settings.LockTimeout()
			tt.start(t, r, box)
			if r.queued() != 1 {
				t.Fatalf("%d operations running, want 1", r.queued())
			}

			if clock.active(timeout) != nil || clock.active(timeout-lockWarningLead) != nil {
				t.Fatal("no auto-lock countdown while the operation runs")
			}
			if clock.active(pausedLockAfter) == nil {
				t.Fatal("the vault should still lock after a long wait without input")
			}
			r.flush()
			if r.locked() {
				t.Fatal("the failed operation must not lock the vault")
			}
			clock.active(timeout).f()
			if !r.locked() {
				t.Fatal("once the operation ends, the auto-lock should count down again")
			}
		})
	}
}

// Locking by hand during a move still locks at once, stops the copy, and
// leaves no key file at the destination.
func TestSettings_LockingDuringAMoveStopsIt(t *testing.T) {
	r, box := queuedSettings(t)
	w := r.window
	dir := r.settings.Vault()
	dest := filepath.Join(t.TempDir(), "moved")
	entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(dest)
	tapAndConfirm(t, w, box, "Move Vault")

	r.lock()
	if !r.locked() {
		t.Fatal("Lock must end the session during a move")
	}
	r.flush()

	if _, err := os.Stat(filepath.Join(dest, "vlt.age")); !os.IsNotExist(err) {
		t.Fatalf("a move stopped by a lock left vlt.age at the destination (stat: %v)", err)
	}
	if r.settings.Vault() != dir || w.Canvas().Overlays().Top() != nil {
		t.Fatal("a stopped move must neither change the location nor report a move")
	}
}

// A passphrase change the vault locked in the middle of is still reported,
// on the unlock screen: here the lock came first, so nothing changed.
func TestSettings_APassphraseChangeCutOffByALockIsReported(t *testing.T) {
	r, _ := queuedSettings(t)
	w := r.window
	card := r.openSettings().buildChangePassphrase()
	test.Type(entryWithPlaceholder(t, card, "Current passphrase"), testPassphrase)
	test.Type(entryWithPlaceholder(t, card, "New passphrase"), "an even better passphrase")
	test.Type(entryWithPlaceholder(t, card, "Confirm new passphrase"), "an even better passphrase")
	test.Tap(findButton(card, "Change Passphrase"))

	r.lock()
	r.flush()

	if text := dialogText(t, w); !strings.Contains(text, "passphrase was not changed") {
		t.Fatalf("after the lock the dialog says %q; want it to say the passphrase was not changed", text)
	}
	r.unlockOpening()
	if r.locked() {
		t.Fatal("the old passphrase should still unlock the vault")
	}
	r.lock()
}

// changeFailed is a passphrase change the store reports as failed, having
// left the vault as it was (Unchanged) or maybe changed it (MaybeChanged).
func changeFailed(change store.Change, err error) error {
	return &store.ChangeError{Change: change, Err: fmt.Errorf("store.ChangePassphrase: %w", err)}
}

// fakeVault is a vaultChanger whose passphrase change ends as told: err
// is what ChangePassphrase returns (nil when it took effect).
type fakeVault struct {
	err error
}

func (f fakeVault) ChangePassphrase(context.Context, string, string) error { return f.err }

func (fakeVault) CopyTo(context.Context, string) error { return errors.New("not copied") }

// changePassphraseOn asks Settings, through its form, to change the
// passphrase of a vault faked by v, on app's queued runner.
func changePassphraseOn(t *testing.T, r *appRig, v fakeVault) fyne.CanvasObject {
	t.Helper()
	ss := r.openSettings()
	ss.store = v
	card := ss.buildChangePassphrase()
	test.Type(entryWithPlaceholder(t, card, "Current passphrase"), testPassphrase)
	test.Type(entryWithPlaceholder(t, card, "New passphrase"), "an even better passphrase")
	test.Type(entryWithPlaceholder(t, card, "Confirm new passphrase"), "an even better passphrase")
	test.Tap(findButton(card, "Change Passphrase"))
	return card
}

// shownText returns the text of every label shown in o.
func shownText(o fyne.CanvasObject) string {
	var b strings.Builder
	findDeep(o, func(o fyne.CanvasObject) bool {
		if l, ok := o.(*widget.Label); ok && l.Visible() {
			b.WriteString(l.Text + "\n")
		}
		return false
	})
	return b.String()
}

// A passphrase change reports what the store says of it while the vault
// stays unlocked: changed relocks and says so; otherwise the vault stays
// unlocked and the form says why, possibly saved when the store can't
// tell.
func TestSettings_APassphraseChangeReportsTheStoresOutcome(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		relocked bool
		want     string
	}{
		{"changed", nil, true, "unlock it with the new passphrase"},
		{"maybe changed", changeFailed(store.MaybeChanged, store.ErrConnectionLost), false, "may or may not have been saved"},
		{"unchanged", changeFailed(store.Unchanged, store.ErrWrongPassphrase), false, "Current passphrase is incorrect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := queuedSettings(t)
			w := r.window
			card := changePassphraseOn(t, r, fakeVault{err: tt.err})
			r.flush()

			if relocked := r.locked(); relocked != tt.relocked {
				t.Fatalf("relocked = %v, want %v", relocked, tt.relocked)
			}
			text := shownText(card)
			if tt.relocked {
				text = dialogText(t, w)
			}
			if !strings.Contains(text, tt.want) {
				t.Fatalf("it says %q; want it to say %q", text, tt.want)
			}
			r.lock()
		})
	}
}

// A passphrase change the vault locked during reports how the store says it
// ended, over the unlock screen: changed; not changed when the lock stopped
// it; possibly saved when the store can't tell (the server's reply to the
// rename was lost); otherwise the error.
func TestSettings_APassphraseChangeEndedByALockReportsItsOutcome(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    []string
		notWant string
	}{
		{
			"changed", nil,
			[]string{"The vault locked as the passphrase change finished", "unlock it with the new passphrase"},
			"not changed",
		},
		{
			"stopped by the lock", changeFailed(store.Unchanged, context.Canceled),
			[]string{"the passphrase was not changed", "Unlock with your current passphrase"},
			"may or may not",
		},
		{
			"key zeroed by the lock", changeFailed(store.Unchanged, store.ErrClosed),
			[]string{"the passphrase was not changed", "Unlock with your current passphrase"},
			"may or may not",
		},
		{
			"reply lost", changeFailed(store.MaybeChanged, store.ErrConnectionLost),
			[]string{"may or may not have been saved", "try the new one"},
			"was not changed",
		},
		{
			// Never "not changed" while the store can't tell, whatever stopped it.
			"maybe changed, then stopped", changeFailed(store.MaybeChanged, fmt.Errorf("%w: %w", store.ErrConnectionLost, context.Canceled)),
			[]string{"may or may not have been saved"},
			"was not changed",
		},
		{
			"connection lost before the write", changeFailed(store.Unchanged, store.ErrConnectionLost),
			[]string{"Couldn't change the passphrase: lost the connection"},
			"may or may not",
		},
		{
			"another error", changeFailed(store.Unchanged, errors.New("disk full")),
			[]string{"Couldn't change the passphrase: disk full"},
			"may or may not",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := queuedSettings(t)
			w := r.window
			changePassphraseOn(t, r, fakeVault{err: tt.err})
			r.lock()
			r.flush()

			text := dialogText(t, w)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("the report says %q; want it to say %q", text, want)
				}
			}
			if strings.Contains(text, tt.notWant) {
				t.Fatalf("the report says %q; it must not say %q", text, tt.notWant)
			}
		})
	}
}

// A passphrase change that fails while the vault stays unlocked says why
// under the form; when the store can't tell whether it was saved, that it
// may have been, mentioning the server only for a vault on one.
func TestPassphraseChangeFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		err     error
		want    string
		notWant string
	}{
		{
			"wrong passphrase", changeFailed(store.Unchanged, store.ErrWrongPassphrase),
			"Current passphrase is incorrect", "may or may not",
		},
		{
			"reply lost", changeFailed(store.MaybeChanged, store.ErrConnectionLost),
			"The change may or may not have been saved: the server may have made it though its reply was lost. " +
				"If your current passphrase is refused next time you unlock, try the new one.", "Couldn't change",
		},
		{
			"local rename not made durable", changeFailed(store.MaybeChanged, errors.New("sync dir: input/output error")),
			"The change may or may not have been saved. If your current passphrase is refused", "server",
		},
		{
			"server unreachable before the write", changeFailed(store.Unchanged, store.ErrUnreachable),
			"Couldn't change the passphrase: cannot connect", "may or may not",
		},
		{
			"another error", changeFailed(store.Unchanged, errors.New("disk full")),
			"Couldn't change the passphrase: disk full", "may or may not",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := passphraseChangeFailure(tt.err)
			if !strings.Contains(got, tt.want) || strings.Contains(got, tt.notWant) {
				t.Fatalf("passphraseChangeFailure = %q; want it to say %q and not %q", got, tt.want, tt.notWant)
			}
		})
	}
}
