package ui

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/testutil"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestLoadConfig_DefaultsWhenNothingIsSet(t *testing.T) {
	a := test.NewTempApp(t)

	cfg := LoadConfig(a.Preferences(), env(nil), "/default/vault", quietLog())

	want := Config{VaultDir: "/default/vault", DefaultVaultDir: "/default/vault", LockTimeoutMs: DefaultLockTimeoutMs, ClipboardTTLs: DefaultClipboardTTLs}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadConfig_SavedSettingsSurviveRestart(t *testing.T) {
	a := test.NewTempApp(t)
	prefs := a.Preferences()
	SaveVaultDir(prefs, "/moved/vault")
	SaveLockTimeout(prefs, 600_000)
	SaveClipboardTTL(prefs, 15)

	got := LoadConfig(prefs, env(nil), "/default/vault", quietLog())

	want := Config{VaultDir: "/moved/vault", DefaultVaultDir: "/default/vault", LockTimeoutMs: 600_000, ClipboardTTLs: 15}
	if got != want {
		t.Fatalf("after restart got %+v, want %+v", got, want)
	}
}

func TestLoadConfig_EnvironmentOverridesSavedSettings(t *testing.T) {
	a := test.NewTempApp(t)
	prefs := a.Preferences()
	SaveVaultDir(prefs, "/saved")
	SaveLockTimeout(prefs, 600_000)
	SaveClipboardTTL(prefs, 15)

	got := LoadConfig(prefs, env(map[string]string{
		envVaultDir: "/from/env", envLockTimeout: "120000", envClipboardTTL: "60",
	}), "/default", quietLog())

	want := Config{
		VaultDir: "/from/env", DefaultVaultDir: "/default", LockTimeoutMs: 120_000, ClipboardTTLs: 60,
		FromEnv: EnvSettings{VaultDir: true, LockTimeout: true, ClipboardTTL: true},
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadConfig_InvalidEnvironmentFallsBackToSavedValue(t *testing.T) {
	a := test.NewTempApp(t)
	SaveLockTimeout(a.Preferences(), 600_000)

	got := LoadConfig(a.Preferences(), env(map[string]string{envLockTimeout: "five minutes"}), "/default", quietLog())

	if got.LockTimeoutMs != 600_000 || got.FromEnv.LockTimeout {
		t.Fatalf("an unparsable env var should fall back to the saved value, got %+v", got)
	}
}

func TestLoadConfig_ClampsOutOfRangeValuesAndLogs(t *testing.T) {
	tests := []struct {
		name        string
		lock, ttl   string
		wantLock    int
		wantTTLSecs int
		wantWarning bool
	}{
		{"too small", "1000", "1", MinLockTimeoutMs, MinClipboardTTLs, true},
		{"too large", "999999999", "500", MaxLockTimeoutMs, MaxClipboardTTLs, true},
		{"auto-lock off", "0", "30", 0, 30, false},
		{"negative means off", "-5", "30", 0, 30, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := test.NewTempApp(t)
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, nil))

			got := LoadConfig(a.Preferences(), env(map[string]string{envLockTimeout: tt.lock, envClipboardTTL: tt.ttl}), "/d", log)

			if got.LockTimeoutMs != tt.wantLock || got.ClipboardTTLs != tt.wantTTLSecs {
				t.Fatalf("got lock=%d ttl=%d, want lock=%d ttl=%d", got.LockTimeoutMs, got.ClipboardTTLs, tt.wantLock, tt.wantTTLSecs)
			}
			if logged := strings.Contains(logs.String(), "clamped"); logged != tt.wantWarning {
				t.Fatalf("clamp warning logged = %v, want %v; log: %s", logged, tt.wantWarning, logs.String())
			}
		})
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

// settingsApp unlocks an app built from cfg and opens Settings through the
// app's own wiring.
func settingsApp(t *testing.T, cfg Config, clock timerFactory) (*App, *SettingsScreen) {
	t.Helper()
	a := test.NewTempApp(t)
	newVaultWith(t, cfg.VaultDir, testutil.PasswordSecret("gh", "x", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), cfg, quietLog(), inlineRunner(), clock, testKDF)
	unlock(app)
	ss := app.openSettings(app.store, inlineRunner())
	return app, ss
}

// lockControls returns the auto-lock checkbox and slider.
func lockControls(ss *SettingsScreen) (*widget.Check, *widget.Slider) {
	box := ss.buildLockTimeout()
	return findType[*widget.Check](box), findType[*widget.Slider](box)
}

func TestSettings_EnvironmentValuesAreNotSavedBySliderChanges(t *testing.T) {
	a := test.NewTempApp(t)
	cfg := LoadConfig(a.Preferences(), env(map[string]string{envVaultDir: t.TempDir()}), "/default", quietLog())
	newVaultWith(t, cfg.VaultDir)
	app := newApp(a, test.NewTempWindow(t, nil), cfg, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)
	ss := app.openSettings(app.store, inlineRunner())

	ttl := findType[*widget.Slider](ss.buildClipboardTTL())
	ttl.SetValue(60)
	ttl.OnChangeEnded(60)

	restarted := LoadConfig(a.Preferences(), env(nil), "/default", quietLog())
	if restarted.VaultDir != "/default" {
		t.Fatalf("an env-provided vault dir leaked into saved settings: %q", restarted.VaultDir)
	}
	if restarted.ClipboardTTLs != 60 {
		t.Fatalf("the slider change itself should be saved, got %d", restarted.ClipboardTTLs)
	}
	app.Lock()
}

func TestSettings_SwitchedVaultIsSavedOnlyOnceOpened(t *testing.T) {
	app, ss := settingsApp(t, Config{VaultDir: t.TempDir()}, noTimers)
	prefs := app.fyneApp.Preferences()
	newDir := filepath.Join(t.TempDir(), "other")

	ss.switchVault(newDir, widget.NewLabel(""))
	if got := LoadConfig(prefs, env(nil), "/default", quietLog()).VaultDir; got == newDir {
		t.Fatal("a switched-to path must not be saved before a vault is opened or created there")
	}

	if app.createScreen == nil {
		t.Fatal("switching to an empty folder should show the create screen")
	}
	test.Type(app.createScreen.entry, "correct horse battery")
	test.Type(app.createScreen.confirm, "correct horse battery")
	test.Tap(app.createScreen.createBtn)

	if got := LoadConfig(prefs, env(nil), "/default", quietLog()).VaultDir; got != newDir {
		t.Fatalf("after creating a vault there, the new path should be saved; got %q", got)
	}
	app.Lock()
}

func TestSettings_MovedVaultIsRememberedAfterUnlock(t *testing.T) {
	app, ss := settingsApp(t, Config{VaultDir: t.TempDir()}, noTimers)
	prefs := app.fyneApp.Preferences()
	newDir := filepath.Join(t.TempDir(), "moved")

	ss.moveVault(newDir, widget.NewLabel(""), widget.NewLabel(""))
	unlock(app) // the relock after a move; unlock at the new location

	if got := LoadConfig(prefs, env(nil), "/default", quietLog()).VaultDir; got != newDir {
		t.Fatalf("after moving and unlocking, the vault dir should be saved as %q, got %q", newDir, got)
	}
	app.Lock()
}

func TestSettings_LockTimeoutSliderIsSaved(t *testing.T) {
	app, ss := settingsApp(t, Config{VaultDir: t.TempDir(), LockTimeoutMs: DefaultLockTimeoutMs}, noTimers)

	_, slider := lockControls(ss)
	slider.SetValue(600_000)
	slider.OnChangeEnded(600_000)

	if got := app.fyneApp.Preferences().Int(prefLockTimeout); got != 600_000 {
		t.Fatalf("saved lock timeout = %d, want 600000", got)
	}
	app.Lock()
}

func TestSettings_AutoLockCanBeTurnedOffAndOn(t *testing.T) {
	clock := &fakeClock{}
	app, ss := settingsApp(t, Config{VaultDir: t.TempDir(), LockTimeoutMs: DefaultLockTimeoutMs}, clock.factory)
	app.tracker.instrument(app.detailView.Content())
	check, slider := lockControls(ss)
	app.tracker.instrument(check)

	check.SetChecked(false)
	if app.config.LockTimeoutMs != 0 || !slider.Disabled() {
		t.Fatal("unticking Auto-lock should turn it off and disable the slider")
	}
	if app.fyneApp.Preferences().Int(prefLockTimeout) != 0 {
		t.Fatal("turning auto-lock off should be saved")
	}
	if clock.active(time.Duration(DefaultLockTimeoutMs)*time.Millisecond) != nil {
		t.Fatal("turning auto-lock off should stop the countdown")
	}

	check.SetChecked(true)
	if app.config.LockTimeoutMs != DefaultLockTimeoutMs || slider.Disabled() {
		t.Fatal("ticking Auto-lock should restore the slider's timeout")
	}
	if clock.active(time.Duration(DefaultLockTimeoutMs)*time.Millisecond) == nil {
		t.Fatal("re-enabling auto-lock should arm the countdown straight away")
	}
	app.Lock()
}

func TestSettings_EnvironmentControlledSettingsAreDisabled(t *testing.T) {
	ss := &SettingsScreen{config: &Config{
		LockTimeoutMs: 120_000, ClipboardTTLs: 60,
		FromEnv: EnvSettings{LockTimeout: true, ClipboardTTL: true},
	}, save: func(string) {}}

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
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	prefs := a.Preferences()
	if prefs.String(prefVaultDir) != "" {
		t.Fatal("nothing should be saved before the vault is unlocked")
	}

	unlock(app)

	if got := prefs.String(prefVaultDir); got != dir {
		t.Fatalf("saved location = %q, want %q", got, dir)
	}
	app.Lock()
}

func TestApp_LocationFromEnvironmentIsNeverSaved(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	cfg := Config{VaultDir: dir, FromEnv: EnvSettings{VaultDir: true}}
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
	got := LoadConfig(a.Preferences(), func(string) string { return "" }, "/default", quietLog())
	if got.VaultDir != "/Users/me/vaults/main" || got.LockTimeoutMs != 600_000 || got.ClipboardTTLs != 15 {
		t.Fatalf("after migrating: %+v", got)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("the old settings file should be left in place")
	}
}

func TestMigratePreferences_NeverOverwritesSettingsUnderTheNewID(t *testing.T) {
	a := test.NewTempApp(t)
	SaveVaultDir(a.Preferences(), "/new/vault")
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
	SaveLockTimeout(a.Preferences(), 0) // the user turns auto-lock off
	MigratePreferences(a.Preferences(), legacy, quietLog())
	if got := a.Preferences().Int(prefLockTimeout); got != 0 {
		t.Fatalf("a later start brought the old timeout back: %d", got)
	}
}

func TestMigratePreferences_ANumberSavedUnderTheNewIDStopsIt(t *testing.T) {
	a := test.NewTempApp(t)
	SaveClipboardTTL(a.Preferences(), 45)
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
	if got := LoadConfig(a.Preferences(), func(string) string { return "" }, "/default", quietLog()); got.VaultDir != "/default" {
		t.Fatalf("VaultDir = %q, want the default", got.VaultDir)
	}
}
