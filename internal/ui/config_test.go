package ui

import (
	"bytes"
	"log/slog"
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

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// restart loads the settings saved in prefs, as the next launch would,
// with no environment variables and the default location /default.
func restart(prefs fyne.Preferences) *Settings {
	return LoadSettings(prefs, env(nil), "/default", quietLog())
}

// location parses s, failing the test if it does not parse.
func location(t *testing.T, s string) store.Location {
	t.Helper()
	loc, err := store.ParseLocation(s)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// The module's own tests use the test app's in-memory preferences and no
// vault.

func TestLoadSettings_DefaultsWhenNothingIsSet(t *testing.T) {
	a := test.NewTempApp(t)

	s := restart(a.Preferences())

	if s.Vault() != localAt("/default") || s.LockTimeout() != 5*time.Minute ||
		s.ClipboardTTL() != 30*time.Second || s.CheckUpdates() || s.AnnouncedRelease() != "" {
		t.Fatalf("got vault %v, lock %v, ttl %v, updates %v", s.Vault(), s.LockTimeout(), s.ClipboardTTL(), s.CheckUpdates())
	}
	if _, unusable := s.UnusableVault(); unusable != nil {
		t.Fatal(unusable)
	}
	for _, setting := range []Setting{SettingVault, SettingLockTimeout, SettingClipboardTTL} {
		if _, ok := s.SetByEnv(setting); ok {
			t.Fatalf("setting %d should not be set by the environment", setting)
		}
	}
}

func TestLoadSettings_SavedSettingsSurviveRestart(t *testing.T) {
	a := test.NewTempApp(t)
	s := restart(a.Preferences())
	s.ProposeVault(localAt("/moved/vault"))
	s.CommitVault()
	s.SetLockTimeout(10 * time.Minute)
	s.SetClipboardTTL(15 * time.Second)
	s.SetCheckUpdates(true)
	s.SetAnnouncedRelease("v0.3.0")

	got := restart(a.Preferences())

	if got.Vault() != localAt("/moved/vault") || got.LockTimeout() != 10*time.Minute ||
		got.ClipboardTTL() != 15*time.Second || !got.CheckUpdates() || got.AnnouncedRelease() != "v0.3.0" {
		t.Fatalf("after restart got vault %v, lock %v, ttl %v, updates %v, announced %q",
			got.Vault(), got.LockTimeout(), got.ClipboardTTL(), got.CheckUpdates(), got.AnnouncedRelease())
	}
}

func TestLoadSettings_EnvironmentOverridesSavedSettings(t *testing.T) {
	a := test.NewTempApp(t)
	s := restart(a.Preferences())
	s.ProposeVault(localAt("/saved"))
	s.CommitVault()
	s.SetLockTimeout(10 * time.Minute)
	s.SetClipboardTTL(15 * time.Second)

	got := LoadSettings(a.Preferences(), env(map[string]string{
		envVaultDir: "/from/env", envLockTimeout: "120000", envClipboardTTL: "60",
	}), "/default", quietLog())

	if got.Vault() != localAt("/from/env") || got.LockTimeout() != 2*time.Minute || got.ClipboardTTL() != time.Minute {
		t.Fatalf("got vault %v, lock %v, ttl %v", got.Vault(), got.LockTimeout(), got.ClipboardTTL())
	}
	for setting, want := range map[Setting]string{
		SettingVault: envVaultDir, SettingLockTimeout: envLockTimeout, SettingClipboardTTL: envClipboardTTL,
	} {
		if variable, ok := got.SetByEnv(setting); !ok || variable != want {
			t.Errorf("SetByEnv(%d) = %q, %v; want %q", setting, variable, ok, want)
		}
	}
}

// A location is used in the form vlt writes, however it was given; one that
// does not parse is kept as given, so opening it shows why.
func TestLoadSettings_ReadsTheLocationInTheFormVltWrites(t *testing.T) {
	tests := []struct {
		name, saved, env, want string
	}{
		{"saved by an older version", "ssh://me@nas:2222/srv/my vault", "", "ssh://me@nas:2222/srv/my%20vault"},
		{"from the environment", "", "ssh://me@nas/srv/vault", "me@nas:/srv/vault"},
		{"a local path", "/Users/me/vault", "", "/Users/me/vault"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := test.NewTempApp(t)
			if tt.saved != "" {
				a.Preferences().SetString(prefVaultDir, tt.saved)
			}

			got := LoadSettings(a.Preferences(), env(map[string]string{envVaultDir: tt.env}), "/default", quietLog())

			if got.Vault().String() != tt.want {
				t.Fatalf("location = %q, want %q", got.Vault(), tt.want)
			}
		})
	}
}

func TestLoadSettings_UnparsableLocationIsKeptAsGiven(t *testing.T) {
	a := test.NewTempApp(t)
	a.Preferences().SetString(prefVaultDir, "nas:")

	s := restart(a.Preferences())

	text, err := s.UnusableVault()
	if text != "nas:" || err == nil || s.Vault() != (store.Location{}) {
		t.Fatalf("UnusableVault() = %q, %v; Vault() = %v", text, err, s.Vault())
	}
	if def, ok := s.DefaultVault(); !ok || def != localAt("/default") {
		t.Fatal("the default vault should be offered instead")
	}
	s.CommitVault()
	if a.Preferences().String(prefVaultDir) != "nas:" {
		t.Fatal("an unusable location is not committed over")
	}

	s.ProposeVault(localAt("/default"))
	if _, err := s.UnusableVault(); err != nil || s.Vault() != localAt("/default") {
		t.Fatal("proposing a location should replace the unusable one")
	}
}

func TestLoadSettings_InvalidEnvironmentFallsBackToSavedValue(t *testing.T) {
	a := test.NewTempApp(t)
	restart(a.Preferences()).SetLockTimeout(10 * time.Minute)

	got := LoadSettings(a.Preferences(), env(map[string]string{envLockTimeout: "five minutes"}), "/default", quietLog())

	if _, fixed := got.SetByEnv(SettingLockTimeout); got.LockTimeout() != 10*time.Minute || fixed {
		t.Fatalf("an unparsable env var should fall back to the saved value, got %v", got.LockTimeout())
	}
}

func TestLoadSettings_ClampsOutOfRangeValuesAndLogs(t *testing.T) {
	tests := []struct {
		name        string
		lock, ttl   string
		wantLock    time.Duration
		wantTTL     time.Duration
		wantWarning bool
	}{
		{"too small", "1000", "1", 30 * time.Second, 5 * time.Second, true},
		{"too large", "999999999", "500", 30 * time.Minute, 2 * time.Minute, true},
		{"auto-lock off", "0", "30", 0, 30 * time.Second, false},
		{"negative means off", "-5", "30", 0, 30 * time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := test.NewTempApp(t)
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, nil))

			got := LoadSettings(a.Preferences(), env(map[string]string{envLockTimeout: tt.lock, envClipboardTTL: tt.ttl}), "/d", log)

			if got.LockTimeout() != tt.wantLock || got.ClipboardTTL() != tt.wantTTL {
				t.Fatalf("got lock=%v ttl=%v, want lock=%v ttl=%v", got.LockTimeout(), got.ClipboardTTL(), tt.wantLock, tt.wantTTL)
			}
			if logged := strings.Contains(logs.String(), "clamped"); logged != tt.wantWarning {
				t.Fatalf("clamp warning logged = %v, want %v; log: %s", logged, tt.wantWarning, logs.String())
			}
		})
	}
}

func TestSettings_SettersClampAndSave(t *testing.T) {
	tests := []struct {
		name          string
		lock, ttl     time.Duration
		wantLockSaved time.Duration
		wantTTLSaved  time.Duration
	}{
		{"in range", 10 * time.Minute, 45 * time.Second, 10 * time.Minute, 45 * time.Second},
		{"too small", time.Second, time.Second, 30 * time.Second, 5 * time.Second},
		{"too large", 2 * time.Hour, 10 * time.Minute, 30 * time.Minute, 2 * time.Minute},
		{"auto-lock off", 0, 30 * time.Second, 0, 30 * time.Second},
		{"negative means off", -time.Minute, 30 * time.Second, 0, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := test.NewTempApp(t)
			s := restart(a.Preferences())

			s.SetLockTimeout(tt.lock)
			s.SetClipboardTTL(tt.ttl)

			if s.LockTimeout() != tt.wantLockSaved || s.ClipboardTTL() != tt.wantTTLSaved {
				t.Fatalf("now lock=%v ttl=%v, want lock=%v ttl=%v", s.LockTimeout(), s.ClipboardTTL(), tt.wantLockSaved, tt.wantTTLSaved)
			}
			if got := restart(a.Preferences()); got.LockTimeout() != tt.wantLockSaved || got.ClipboardTTL() != tt.wantTTLSaved {
				t.Fatalf("saved lock=%v ttl=%v, want lock=%v ttl=%v", got.LockTimeout(), got.ClipboardTTL(), tt.wantLockSaved, tt.wantTTLSaved)
			}
		})
	}
}

// A setting an environment variable fixes cannot be changed, and is never
// saved: the variable would override it on the next launch anyway.
func TestSettings_SettingsFixedByTheEnvironmentAreNeitherChangedNorSaved(t *testing.T) {
	a := test.NewTempApp(t)
	s := LoadSettings(a.Preferences(), env(map[string]string{
		envVaultDir: "/from/env", envLockTimeout: "120000", envClipboardTTL: "60",
	}), "/default", quietLog())

	s.SetLockTimeout(10 * time.Minute)
	s.SetClipboardTTL(15 * time.Second)
	s.ProposeVault(localAt("/other"))
	s.CommitVault()

	if s.Vault() != localAt("/from/env") || s.LockTimeout() != 2*time.Minute || s.ClipboardTTL() != time.Minute {
		t.Fatalf("now vault %v, lock %v, ttl %v; the environment's values should stay", s.Vault(), s.LockTimeout(), s.ClipboardTTL())
	}
	if _, ok := s.DefaultVault(); ok {
		t.Fatal("VLT_VAULT_DIR fixes the location, so the default must not be offered")
	}
	got := restart(a.Preferences())
	if got.Vault() != localAt("/default") || got.LockTimeout() != 5*time.Minute || got.ClipboardTTL() != 30*time.Second {
		t.Fatalf("saved vault %v, lock %v, ttl %v; nothing should be saved", got.Vault(), got.LockTimeout(), got.ClipboardTTL())
	}
}

// A proposed location sticks only once committed, so a mistyped one is
// never remembered.
func TestSettings_ProposedLocationIsSavedOnlyWhenCommitted(t *testing.T) {
	a := test.NewTempApp(t)
	s := restart(a.Preferences())

	s.ProposeVault(location(t, "me@nas:/srv/vault"))
	if s.Vault() != location(t, "me@nas:/srv/vault") {
		t.Fatalf("Vault() = %v, want the proposed location", s.Vault())
	}
	if got := restart(a.Preferences()).Vault(); got != localAt("/default") {
		t.Fatalf("an uncommitted location was saved: %v", got)
	}

	s.CommitVault()
	if got := restart(a.Preferences()).Vault(); got != location(t, "me@nas:/srv/vault") {
		t.Fatalf("after committing, saved location = %v", got)
	}
}

// The default location depends on the environment (XDG_DATA_HOME), which
// can differ between launches, so it is saved once committed too.
func TestSettings_DefaultLocationIsCommittedToo(t *testing.T) {
	a := test.NewTempApp(t)
	restart(a.Preferences()).CommitVault()

	got := LoadSettings(a.Preferences(), env(nil), "/another/default", quietLog())

	if got.Vault() != localAt("/default") {
		t.Fatalf("location = %v, want the default it was committed as", got.Vault())
	}
}

// The same place written two ways is the same location.
func TestSettings_LocationsAreComparedByValue(t *testing.T) {
	a := test.NewTempApp(t)
	a.Preferences().SetString(prefVaultDir, "ssh://me@nas/srv/vault")
	s := LoadSettings(a.Preferences(), env(nil), "  /default  ", quietLog())

	if s.Vault() != location(t, "me@nas:/srv/vault") {
		t.Fatalf("Vault() = %v, want me@nas:/srv/vault", s.Vault())
	}
	if def, ok := s.DefaultVault(); !ok || def != localAt("/default") {
		t.Fatalf("DefaultVault() = %v, %v; the default is another vault", def, ok)
	}
	s.ProposeVault(location(t, "/default"))
	if _, ok := s.DefaultVault(); ok {
		t.Fatal("the default, written another way, is the current location; it must not be offered")
	}
}

func TestFormatLockTimeout(t *testing.T) {
	t.Parallel()
	tests := map[int]string{
		0:         "Auto-lock: off",
		30_000:    "Auto-lock timeout: 30 s",
		300_000:   "Auto-lock timeout: 5 min",
		90_000:    "Auto-lock timeout: 1.5 min",
		1_800_000: "Auto-lock timeout: 30 min",
	}
	for ms, want := range tests {
		if got := formatLockTimeout(ms); got != want {
			t.Errorf("formatLockTimeout(%d) = %q, want %q", ms, got, want)
		}
	}
}

// settingsApp unlocks an app for a vault in a new folder, with settings
// changed by opts, and opens Settings through the app's own wiring.
func settingsApp(t *testing.T, clock timerFactory, opts ...func(*Settings)) (*App, *SettingsScreen) {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "x", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir, opts...), quietLog(), inlineRunner(), clock, testKDF)
	unlock(app)
	ss := app.openSettings(app.session.Store(), app.sessionOps)
	return app, ss
}

// lockControls returns the auto-lock checkbox and slider.
func lockControls(ss *SettingsScreen) (*widget.Check, *widget.Slider) {
	box := ss.buildLockTimeout()
	return findType[*widget.Check](box), findType[*widget.Slider](box)
}

func TestSettings_EnvironmentValuesAreNotSavedBySliderChanges(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	cfg := LoadSettings(a.Preferences(), env(map[string]string{envVaultDir: dir}), "/default", quietLog())
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), cfg, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)
	ss := app.openSettings(app.session.Store(), app.sessionOps)

	ttl := findType[*widget.Slider](ss.buildClipboardTTL())
	ttl.SetValue(60)
	ttl.OnChangeEnded(60)

	restarted := restart(a.Preferences())
	if restarted.Vault() != localAt("/default") {
		t.Fatalf("an env-provided vault dir leaked into saved settings: %v", restarted.Vault())
	}
	if restarted.ClipboardTTL() != time.Minute {
		t.Fatalf("the slider change itself should be saved, got %v", restarted.ClipboardTTL())
	}
	app.Lock()
}

func TestSettings_SwitchedVaultIsSavedOnlyOnceOpened(t *testing.T) {
	app, ss := settingsApp(t, noTimers)
	prefs := app.fyneApp.Preferences()
	newDir := filepath.Join(t.TempDir(), "other")

	ss.switchVault(localAt(newDir), widget.NewLabel(""))
	if got := restart(prefs).Vault(); got == localAt(newDir) {
		t.Fatal("a switched-to path must not be saved before a vault is opened or created there")
	}

	if app.createScreen == nil {
		t.Fatal("switching to an empty folder should show the create screen")
	}
	test.Type(app.createScreen.entry, "correct horse battery")
	test.Type(app.createScreen.confirm, "correct horse battery")
	test.Tap(app.createScreen.createBtn)

	if got := restart(prefs).Vault(); got != localAt(newDir) {
		t.Fatalf("after creating a vault there, the new path should be saved; got %v", got)
	}
	app.Lock()
}

func TestSettings_MovedVaultIsRememberedAfterUnlock(t *testing.T) {
	app, ss := settingsApp(t, noTimers)
	prefs := app.fyneApp.Preferences()
	newDir := filepath.Join(t.TempDir(), "moved")

	ss.moveVault(localAt(newDir), widget.NewButton("Move Vault", nil), widget.NewLabel(""), widget.NewLabel(""))
	unlock(app) // the relock after a move; unlock at the new location

	if got := restart(prefs).Vault(); got != localAt(newDir) {
		t.Fatalf("after moving and unlocking, the vault dir should be saved as %q, got %v", newDir, got)
	}
	app.Lock()
}

func TestSettings_BrowseFillsInTheChosenFolder(t *testing.T) {
	app, ss := settingsApp(t, noTimers)
	p := pickerOf(app)
	box := ss.buildVaultLocation()
	dir := t.TempDir()
	folder, err := storage.ListerForURI(storage.NewFileURI(dir))
	if err != nil {
		t.Fatal(err)
	}

	test.Tap(findButton(box, "Browse…"))
	p.answerFolder(t, folder, nil)

	if got := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").Text; got != dir {
		t.Fatalf("the location entry holds %q, want the chosen folder %q", got, dir)
	}
	app.Lock()
}

func TestSettings_FailedMoveSaysWhyUnderTheLocation(t *testing.T) {
	app, ss := settingsApp(t, noTimers)
	box := ss.buildVaultLocation()
	taken := t.TempDir()
	newVaultWith(t, taken) // a vault is already there

	entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(taken)
	tapAndConfirm(t, app.window, box, "Move Vault")

	if !visibleText(box, "Couldn't move the vault: vault already exists") {
		t.Fatal("a failed move should say why under the location")
	}
	if app.session.Store() == nil || app.settings.Vault() == localAt(taken) {
		t.Fatal("a failed move must neither lock nor change the vault location")
	}
	app.Lock()
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
			app, ss := settingsApp(t, noTimers)
			box := ss.buildVaultLocation()
			entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText("  ")

			test.Tap(findButton(box, tt.button))

			if !visibleText(box, tt.want) || app.window.Canvas().Overlays().Top() != nil {
				t.Fatalf("an empty location should show %q and ask nothing", tt.want)
			}
			app.Lock()
		})
	}
}

func TestSettings_LockTimeoutSliderIsSaved(t *testing.T) {
	app, ss := settingsApp(t, noTimers, withLockTimeoutMs(DefaultLockTimeoutMs))

	_, slider := lockControls(ss)
	slider.SetValue(600_000)
	slider.OnChangeEnded(600_000)

	if got := restart(app.fyneApp.Preferences()).LockTimeout(); got != 10*time.Minute {
		t.Fatalf("saved lock timeout = %v, want 10m", got)
	}
	app.Lock()
}

func TestSettings_AutoLockCanBeTurnedOffAndOn(t *testing.T) {
	clock := &fakeClock{}
	app, ss := settingsApp(t, clock.factory, withLockTimeoutMs(DefaultLockTimeoutMs))
	app.tracker.instrument(app.detailView.Content())
	check, slider := lockControls(ss)
	app.tracker.instrument(check)

	check.SetChecked(false)
	if app.settings.LockTimeout() != 0 || !slider.Disabled() {
		t.Fatal("unticking Auto-lock should turn it off and disable the slider")
	}
	if restart(app.fyneApp.Preferences()).LockTimeout() != 0 {
		t.Fatal("turning auto-lock off should be saved")
	}
	if clock.active(time.Duration(DefaultLockTimeoutMs)*time.Millisecond) != nil {
		t.Fatal("turning auto-lock off should stop the countdown")
	}

	check.SetChecked(true)
	if app.settings.LockTimeout() != DefaultLockTimeoutMs*time.Millisecond || slider.Disabled() {
		t.Fatal("ticking Auto-lock should restore the slider's timeout")
	}
	if clock.active(time.Duration(DefaultLockTimeoutMs)*time.Millisecond) == nil {
		t.Fatal("re-enabling auto-lock should arm the countdown straight away")
	}
	app.Lock()
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

// The default location depends on the environment (XDG_DATA_HOME), which can
// differ between launches, so it is remembered once a vault opens there.
func TestApp_DefaultLocationIsSavedAfterUnlock(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), LoadSettings(a.Preferences(), env(nil), dir, quietLog()),
		quietLog(), inlineRunner(), noTimers, testKDF)
	prefs := a.Preferences()
	if prefs.String(prefVaultDir) != "" {
		t.Fatal("nothing should be saved before the vault is unlocked")
	}

	unlock(app)

	if got := restart(prefs).Vault(); got != localAt(dir) {
		t.Fatalf("saved location = %v, want %q", got, dir)
	}
	app.Lock()
}

func TestApp_LocationFromEnvironmentIsNeverSaved(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	cfg := LoadSettings(a.Preferences(), env(map[string]string{envVaultDir: dir}), "/default", quietLog())
	app := newApp(a, test.NewTempWindow(t, nil), cfg, quietLog(), inlineRunner(), noTimers, testKDF)

	unlock(app)

	if got := a.Preferences().String(prefVaultDir); got != "" {
		t.Fatalf("a location set by %s must not be saved, got %q", envVaultDir, got)
	}
	app.Lock()
}

func TestMigratePreferences_CarriesOverTheSettingsOfTheOldAppID(t *testing.T) {
	a := test.NewTempApp(t)
	legacy := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(legacy, []byte(`{"vaultDir":"/Users/me/vaults/main","lockTimeoutMs":600000,"clipboardTTLs":15,"other":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	MigratePreferences(a.Preferences(), legacy, quietLog())
	got := restart(a.Preferences())
	if got.Vault() != localAt("/Users/me/vaults/main") || got.LockTimeout() != 10*time.Minute || got.ClipboardTTL() != 15*time.Second {
		t.Fatalf("after migrating: vault %v, lock %v, ttl %v", got.Vault(), got.LockTimeout(), got.ClipboardTTL())
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("the old settings file should be left in place")
	}
}

func TestMigratePreferences_NeverOverwritesSettingsUnderTheNewID(t *testing.T) {
	a := test.NewTempApp(t)
	s := restart(a.Preferences())
	s.ProposeVault(localAt("/new/vault"))
	s.CommitVault()
	legacy := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(legacy, []byte(`{"vaultDir":"/old/vault","clipboardTTLs":15}`), 0o600); err != nil {
		t.Fatal(err)
	}
	MigratePreferences(a.Preferences(), legacy, quietLog())
	if got := a.Preferences().String(prefVaultDir); got != "/new/vault" {
		t.Fatalf("vaultDir = %q", got)
	}
	if got := a.Preferences().Int(prefClipboardTTL); got != 0 {
		t.Fatalf("nothing should be migrated once settings exist, clipboard TTL = %d", got)
	}
}

func TestMigratePreferences_MissingOrBrokenOldFileIsIgnored(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	MigratePreferences(a.Preferences(), filepath.Join(dir, "absent.json"), quietLog())
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	MigratePreferences(a.Preferences(), broken, quietLog())
	if a.Preferences().String(prefVaultDir) != "" {
		t.Fatal("nothing should be set")
	}
}

func TestMigratePreferences_RunsOnceSoLaterChangesStick(t *testing.T) {
	a := test.NewTempApp(t)
	legacy := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(legacy, []byte(`{"lockTimeoutMs":600000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	MigratePreferences(a.Preferences(), legacy, quietLog())
	restart(a.Preferences()).SetLockTimeout(0) // the user turns auto-lock off
	MigratePreferences(a.Preferences(), legacy, quietLog())
	if got := restart(a.Preferences()).LockTimeout(); got != 0 {
		t.Fatalf("a later start brought the old timeout back: %v", got)
	}
}

func TestMigratePreferences_ANumberSavedUnderTheNewIDStopsIt(t *testing.T) {
	a := test.NewTempApp(t)
	restart(a.Preferences()).SetClipboardTTL(45 * time.Second)
	legacy := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(legacy, []byte(`{"vaultDir":"/old/vault"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	MigratePreferences(a.Preferences(), legacy, quietLog())
	if got := a.Preferences().String(prefVaultDir); got != "" {
		t.Fatalf("vaultDir = %q; nothing should be copied once a setting exists", got)
	}
}

func TestMigratePreferences_AnEmptyOldLocationIsNotCopied(t *testing.T) {
	a := test.NewTempApp(t)
	legacy := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(legacy, []byte(`{"vaultDir":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	MigratePreferences(a.Preferences(), legacy, quietLog())
	if got := restart(a.Preferences()).Vault(); got != localAt("/default") {
		t.Fatalf("location = %v, want the default", got)
	}
}

// queuedSettings is an unlocked app whose background work waits in q, with
// Settings open and its vault location card built.
func queuedSettings(t *testing.T) (*App, fyne.Window, *queue, fyne.CanvasObject) {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "x", "work"))
	w := test.NewTempWindow(t, nil)
	q := &queue{}
	app := newApp(a, w, settingsAt(a, dir), quietLog(), q.runner(), noTimers, testKDF)
	q.flush()
	unlockQueued(t, app, q)
	q.flush() // the first load of the list
	ss := app.openSettings(app.session.Store(), app.sessionOps)
	return app, w, q, ss.buildVaultLocation()
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
	app, w, q, box := queuedSettings(t)
	entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(filepath.Join(t.TempDir(), "moved"))

	tapAndConfirm(t, w, box, "Move Vault")
	tapAndConfirm(t, w, box, "Move Vault") // again, while the copy runs
	// Settings opened again shows fresh buttons; the copy still runs.
	again := app.openSettings(app.session.Store(), app.sessionOps).buildVaultLocation()
	entryWithPlaceholder(t, again, "Folder, or user@host:/path on an SSH server").SetText(filepath.Join(t.TempDir(), "other"))
	tapAndConfirm(t, w, again, "Move Vault")

	if len(q.pending) != 1 {
		t.Fatalf("%d copies started, want 1", len(q.pending))
	}
	if !findButton(box, "Move Vault").Disabled() {
		t.Fatal("Move Vault should be disabled while the vault is copied")
	}
	q.flush()
	app.Lock()
}

func TestSettings_SwitchVaultWaitsForAMoveToFinish(t *testing.T) {
	app, w, q, box := queuedSettings(t)
	dir := app.settings.Vault()
	path := entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server")
	path.SetText(filepath.Join(t.TempDir(), "moved"))
	tapAndConfirm(t, w, box, "Move Vault")

	path.SetText(filepath.Join(t.TempDir(), "other"))
	tapAndConfirm(t, w, box, "Switch Vault (no copy)")

	if app.settings.Vault() != dir || app.session.Store() == nil {
		t.Fatal("switching while the vault is being copied must wait, not lock and switch")
	}
	if app.statusBar.Text != waitMessage {
		t.Fatalf("status bar says %q, want it to ask the user to wait", app.statusBar.Text)
	}
	q.flush()
	app.Lock()
}

func TestSettings_PassphraseChangeWaitsForAMoveToFinish(t *testing.T) {
	app, w, q, box := queuedSettings(t)
	entryWithPlaceholder(t, box, "Folder, or user@host:/path on an SSH server").SetText(filepath.Join(t.TempDir(), "moved"))
	tapAndConfirm(t, w, box, "Move Vault")

	card := app.openSettings(app.session.Store(), app.sessionOps).buildChangePassphrase()
	test.Type(entryWithPlaceholder(t, card, "Current passphrase"), testPassphrase)
	test.Type(entryWithPlaceholder(t, card, "New passphrase"), "an even better passphrase")
	test.Type(entryWithPlaceholder(t, card, "Confirm new passphrase"), "an even better passphrase")
	test.Tap(findButton(card, "Change Passphrase"))

	if len(q.pending) != 1 {
		t.Fatalf("%d vault-wide operations running, want only the copy", len(q.pending))
	}
	q.flush()
	app.Lock()
}
