package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"

	"fyne.io/fyne/v2"
)

// Config holds the user-adjustable settings.
type Config struct {
	VaultDir string // the vault's location: a directory, or a folder on an SSH server
	// DefaultVaultDir is the local default location, offered when a vault on
	// a server cannot be reached.
	DefaultVaultDir string
	LockTimeoutMs   int // auto-lock after this many ms of inactivity; 0 disables it
	ClipboardTTLs   int // seconds before a copied secret is cleared
	// FromEnv records which settings were set by environment variables.
	// Those are not saved and cannot be changed in Settings, because the
	// variable would override the change on the next launch anyway.
	FromEnv EnvSettings
}

// EnvSettings flags the settings that came from environment variables.
type EnvSettings struct {
	VaultDir, LockTimeout, ClipboardTTL bool
}

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
	// prefMigrated records that MigratePreferences has run.
	prefMigrated = "migratedFrom010"
)

// LoadConfig resolves each setting from, in order of precedence, an
// environment variable, the value saved by the Settings screen in prefs, and
// the default. Out-of-range values are clamped and invalid ones ignored,
// with a warning logged either way.
func LoadConfig(prefs fyne.Preferences, getenv func(string) string, defaultVaultDir string, log *slog.Logger) Config {
	cfg := Config{
		VaultDir:        prefs.StringWithFallback(prefVaultDir, defaultVaultDir),
		DefaultVaultDir: defaultVaultDir,
		LockTimeoutMs:   prefs.IntWithFallback(prefLockTimeout, DefaultLockTimeoutMs),
		ClipboardTTLs:   prefs.IntWithFallback(prefClipboardTTL, DefaultClipboardTTLs),
	}
	if dir := getenv(envVaultDir); dir != "" {
		cfg.VaultDir = dir
		cfg.FromEnv.VaultDir = true
	}
	if v, ok := envInt(getenv, envLockTimeout, log); ok {
		cfg.LockTimeoutMs = v
		cfg.FromEnv.LockTimeout = true
	}
	if v, ok := envInt(getenv, envClipboardTTL, log); ok {
		cfg.ClipboardTTLs = v
		cfg.FromEnv.ClipboardTTL = true
	}

	cfg.LockTimeoutMs = clampLockTimeout(cfg.LockTimeoutMs, log)
	cfg.ClipboardTTLs = clamp("clipboard TTL (s)", cfg.ClipboardTTLs, MinClipboardTTLs, MaxClipboardTTLs, log)
	return cfg
}

// Each setting is saved on its own, only when the user changes it, so a
// value that came from an environment variable is never written as if the
// user had chosen it.

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

// SaveVaultDir stores the vault directory in prefs.
func SaveVaultDir(prefs fyne.Preferences, dir string) { prefs.SetString(prefVaultDir, dir) }

// SaveLockTimeout stores the auto-lock timeout (ms; 0 = off) in prefs.
func SaveLockTimeout(prefs fyne.Preferences, ms int) { prefs.SetInt(prefLockTimeout, ms) }

// SaveClipboardTTL stores the clipboard TTL (seconds) in prefs.
func SaveClipboardTTL(prefs fyne.Preferences, s int) { prefs.SetInt(prefClipboardTTL, s) }

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
