package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"time"

	"fyne.io/fyne/v2"

	"github.com/heyjdp/vlt/internal/store"
)

// Settings is the settings module: where the vault is, the auto-lock
// timeout, the clipboard TTL and the start-up update check. LoadSettings
// resolves each from an environment variable, the value saved in Fyne's
// preferences, or the default. Its setters clamp the value, refuse a setting
// an environment variable fixes (the variable would override it on the next
// launch, so it is never saved either), and save the change.
//
// The vault location is proposed, then committed. The proposed location is
// the one the app shows and opens; ProposeVault changes it (a switch, a
// move, a folder or server chosen on the create screen). CommitVault saves
// it once a vault has been opened or created there, so a mistyped location
// never sticks. Locations are compared by value, so the same place written
// two ways (ssh:// or host:path) is the same location.
//
// Settings is used on the main goroutine only.
type Settings struct {
	prefs fyne.Preferences
	log   *slog.Logger
	// env names the environment variable that fixed each setting it set.
	env map[Setting]string

	vault store.Location // proposed: shown and opened
	saved store.Location // committed: in prefs (zero when nothing is)
	def   store.Location // the default local location (zero when none)
	// unusable is set while the location given by VLT_VAULT_DIR or the
	// saved settings does not parse; vault is then zero.
	unusable *unusableLocation

	lockTimeout  time.Duration // 0 disables auto-lock
	clipboardTTL time.Duration
	// checkUpdates asks GitHub for the latest release when vlt starts. It
	// is off unless the user turns it on in Settings.
	checkUpdates bool
}

// unusableLocation is a location, as given, that does not parse.
type unusableLocation struct {
	text string
	err  error
}

// Setting names a setting an environment variable can fix.
type Setting int

// The settings an environment variable can fix.
const (
	SettingVault Setting = iota + 1
	SettingLockTimeout
	SettingClipboardTTL
)

// Setting limits. The auto-lock timeout may also be 0 (off).
const (
	DefaultLockTimeoutMs = 5 * 60 * 1000
	MinLockTimeoutMs     = 30 * 1000
	MaxLockTimeoutMs     = 30 * 60 * 1000

	DefaultClipboardTTLs = 30
	MinClipboardTTLs     = 5
	MaxClipboardTTLs     = 120
)

// Environment variables and preference keys for each setting.
const (
	envVaultDir     = "VLT_VAULT_DIR"
	envLockTimeout  = "VLT_LOCK_TIMEOUT_MS"
	envClipboardTTL = "VLT_CLIPBOARD_TTL_S"

	prefVaultDir     = "vaultDir"
	prefLockTimeout  = "lockTimeoutMs"
	prefClipboardTTL = "clipboardTTLs"
	prefCheckUpdates = "checkUpdates"
	// prefUpdateNotified is the latest release the startup check has told
	// the user about, so each one is announced once.
	prefUpdateNotified = "updateNotified"
	// prefMigrated records that MigratePreferences has run.
	prefMigrated = "migratedFrom010"
)

// LoadSettings resolves each setting from, in order of precedence, an
// environment variable, the value saved in prefs, and the default; changes
// are saved to prefs. defaultVault is the local default location, offered
// when the vault cannot be read. Out-of-range values are clamped and
// unparsable numbers ignored, with a warning logged either way. A location
// that does not parse is kept as given (see UnusableVault), so opening it
// shows why.
func LoadSettings(prefs fyne.Preferences, getenv func(string) string, defaultVault string, log *slog.Logger) *Settings {
	s := &Settings{prefs: prefs, log: log, env: map[Setting]string{}, checkUpdates: prefs.Bool(prefCheckUpdates)}
	if loc, err := store.ParseLocation(defaultVault); err == nil {
		s.def = loc
	}
	saved := prefs.String(prefVaultDir)
	if loc, err := store.ParseLocation(saved); err == nil {
		s.saved = loc
	}
	location := saved
	if location == "" {
		location = defaultVault
	}
	if fromEnv := getenv(envVaultDir); fromEnv != "" {
		location = fromEnv
		s.env[SettingVault] = envVaultDir
	}
	s.useLocation(location)

	lockMs := prefs.IntWithFallback(prefLockTimeout, DefaultLockTimeoutMs)
	if v, ok := envInt(getenv, envLockTimeout, log); ok {
		lockMs = v
		s.env[SettingLockTimeout] = envLockTimeout
	}
	ttl := prefs.IntWithFallback(prefClipboardTTL, DefaultClipboardTTLs)
	if v, ok := envInt(getenv, envClipboardTTL, log); ok {
		ttl = v
		s.env[SettingClipboardTTL] = envClipboardTTL
	}
	s.lockTimeout = time.Duration(clampLockTimeout(lockMs, log)) * time.Millisecond
	s.clipboardTTL = time.Duration(clampClipboardTTL(ttl, log)) * time.Second
	return s
}

// useLocation makes text, as given, the proposed location.
func (s *Settings) useLocation(text string) {
	s.vault, s.unusable = store.Location{}, nil
	loc, err := store.ParseLocation(text)
	if err != nil {
		s.unusable = &unusableLocation{text: text, err: err}
		return
	}
	s.vault = loc
}

// SetByEnv reports whether an environment variable fixes the setting, and
// which: the setting is then shown as such and cannot be changed.
func (s *Settings) SetByEnv(setting Setting) (variable string, ok bool) {
	variable, ok = s.env[setting]
	return variable, ok
}

// fixed reports whether an environment variable fixes the setting, logging
// that a change to it is ignored.
func (s *Settings) fixed(setting Setting) bool {
	variable, ok := s.SetByEnv(setting)
	if ok {
		s.log.Warn("ignoring a change to a setting fixed by an environment variable", "variable", variable)
	}
	return ok
}

// Vault returns the proposed vault location: the one to show and open. It
// is zero while the location given does not parse (see UnusableVault) or
// when there is none.
func (s *Settings) Vault() store.Location { return s.vault }

// UnusableVault returns the location given by VLT_VAULT_DIR or the saved
// settings, as written, and why it cannot be used, while it does not
// parse. err is nil otherwise.
func (s *Settings) UnusableVault() (text string, err error) {
	if s.unusable == nil {
		return "", nil
	}
	return s.unusable.text, s.unusable.err
}

// DefaultVault returns the default local location when the vault may
// switch to it: there is one, it is not the proposed location, and
// VLT_VAULT_DIR does not fix the location.
func (s *Settings) DefaultVault() (store.Location, bool) {
	_, fixed := s.SetByEnv(SettingVault)
	if fixed || s.def == (store.Location{}) || (s.unusable == nil && s.def == s.vault) {
		return store.Location{}, false
	}
	return s.def, true
}

// ProposeVault makes loc the location to show and open. Nothing is saved
// until CommitVault. It is refused when VLT_VAULT_DIR fixes the location.
func (s *Settings) ProposeVault(loc store.Location) {
	if s.fixed(SettingVault) {
		return
	}
	s.vault, s.unusable = loc, nil
}

// CommitVault saves the proposed location; the app calls it once a vault
// has been opened or created there, so a mistyped location never sticks.
// The default location is saved too: it depends on the environment
// (XDG_DATA_HOME), which can differ between a terminal and the desktop's
// app menu. A location set by VLT_VAULT_DIR is never saved.
func (s *Settings) CommitVault() {
	if _, fixed := s.SetByEnv(SettingVault); fixed || s.unusable != nil || s.vault == s.saved {
		return
	}
	s.prefs.SetString(prefVaultDir, s.vault.String())
	s.saved = s.vault
}

// LockTimeout returns the auto-lock timeout; 0 disables auto-lock.
func (s *Settings) LockTimeout() time.Duration { return s.lockTimeout }

// SetLockTimeout changes and saves the auto-lock timeout, clamped to
// 30 s–30 min; 0 or less turns auto-lock off. It is refused when
// VLT_LOCK_TIMEOUT_MS sets the timeout.
func (s *Settings) SetLockTimeout(d time.Duration) {
	if s.fixed(SettingLockTimeout) {
		return
	}
	ms := clampLockTimeout(int(d.Milliseconds()), s.log)
	s.lockTimeout = time.Duration(ms) * time.Millisecond
	s.prefs.SetInt(prefLockTimeout, ms)
}

// ClipboardTTL returns how long a copied secret stays on the clipboard.
func (s *Settings) ClipboardTTL() time.Duration { return s.clipboardTTL }

// SetClipboardTTL changes and saves the clipboard TTL, clamped to
// 5–120 s. It is refused when VLT_CLIPBOARD_TTL_S sets the TTL.
func (s *Settings) SetClipboardTTL(d time.Duration) {
	if s.fixed(SettingClipboardTTL) {
		return
	}
	secs := clampClipboardTTL(int(d/time.Second), s.log)
	s.clipboardTTL = time.Duration(secs) * time.Second
	s.prefs.SetInt(prefClipboardTTL, secs)
}

// CheckUpdates reports whether vlt checks for a newer release at start-up.
func (s *Settings) CheckUpdates() bool { return s.checkUpdates }

// SetCheckUpdates turns the start-up update check on or off, and saves it.
func (s *Settings) SetCheckUpdates(on bool) {
	s.checkUpdates = on
	s.prefs.SetBool(prefCheckUpdates, on)
}

// AnnouncedRelease returns the latest release the start-up check has told
// the user about ("" if none), so each release is announced once.
func (s *Settings) AnnouncedRelease() string { return s.prefs.String(prefUpdateNotified) }

// SetAnnouncedRelease records, and saves, that the user was told of tag.
func (s *Settings) SetAnnouncedRelease(tag string) { s.prefs.SetString(prefUpdateNotified, tag) }

// MigratePreferences carries the saved settings of vlt 0.1.0, which used a
// placeholder app ID, over to prefs. Fyne keeps settings per app ID, so
// without this an upgrade would forget the vault's location (and, if it was
// not the default, show the create screen). It runs once: afterwards
// prefMigrated is set, so a later start never copies an old value over one
// changed since (auto-lock off is saved as 0, which looks unset). It also
// skips copying if prefs already holds a vlt setting, and does nothing more
// if legacyFile is missing or unreadable. The old file is left in place.
func MigratePreferences(prefs fyne.Preferences, legacyFile string, log *slog.Logger) {
	if prefs.Bool(prefMigrated) {
		return
	}
	prefs.SetBool(prefMigrated, true)
	for _, key := range []string{prefVaultDir, prefLockTimeout, prefClipboardTTL} {
		if prefs.String(key) != "" || prefs.Int(key) != 0 {
			return // already set up under this app ID
		}
	}
	data, err := os.ReadFile(legacyFile) //nolint:gosec // a fixed file in Fyne's settings folder
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn("cannot read the settings of vlt 0.1.0", "err", err)
		}
		return
	}
	var old map[string]any
	if err := json.Unmarshal(data, &old); err != nil {
		log.Warn("cannot parse the settings of vlt 0.1.0", "err", err)
		return
	}
	migrated := 0
	if dir, ok := old[prefVaultDir].(string); ok && dir != "" {
		prefs.SetString(prefVaultDir, dir)
		migrated++
	}
	for _, key := range []string{prefLockTimeout, prefClipboardTTL} {
		if v, ok := old[key].(float64); ok { // JSON numbers decode as float64
			prefs.SetInt(key, int(v))
			migrated++
		}
	}
	if migrated > 0 {
		log.Info("carried over the settings of vlt 0.1.0", "settings", migrated, "from", legacyFile)
	}
}

// envInt reads an integer environment variable, ignoring (with a warning)
// one that is set but not a number.
func envInt(getenv func(string) string, key string, log *slog.Logger) (int, bool) {
	s := getenv(key)
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		log.Warn("ignoring invalid environment variable", "key", key, "value", s)
		return 0, false
	}
	return v, true
}

// clampLockTimeout keeps the auto-lock timeout within its limits; 0 (off)
// is allowed, and negative values mean off.
func clampLockTimeout(ms int, log *slog.Logger) int {
	if ms <= 0 {
		return 0
	}
	return clamp("auto-lock timeout (ms)", ms, MinLockTimeoutMs, MaxLockTimeoutMs, log)
}

// clampClipboardTTL keeps the clipboard TTL (seconds) within its limits.
func clampClipboardTTL(secs int, log *slog.Logger) int {
	return clamp("clipboard TTL (s)", secs, MinClipboardTTLs, MaxClipboardTTLs, log)
}

// clamp limits v to [lo, hi], logging when it changes.
func clamp(name string, v, lo, hi int, log *slog.Logger) int {
	c := min(max(v, lo), hi)
	if c != v {
		log.Warn("setting out of range; clamped", "setting", name, "value", v, "used", c)
	}
	return c
}

// formatLockTimeout describes the auto-lock timeout for the Settings screen.
func formatLockTimeout(ms int) string {
	switch {
	case ms <= 0:
		return "Auto-lock: off"
	case ms < 60_000:
		return fmt.Sprintf("Auto-lock timeout: %d s", ms/1000)
	default:
		return fmt.Sprintf("Auto-lock timeout: %g min", float64(ms)/60_000)
	}
}
