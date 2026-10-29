package ui

import (
	"fmt"
	"log/slog"
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
