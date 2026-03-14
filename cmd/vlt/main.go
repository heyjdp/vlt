// Package main is the entry point for the vlt secret manager.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/awnumar/memguard"

	"github.com/yourorg/vlt/internal/ui"
)

var version = "dev" // overridden by ldflags

func main() {
	// Initialize memguard for secure memory handling
	memguard.CatchInterrupt()
	defer memguard.Purge()

	// Set up structured logging
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("starting vlt", "version", version)

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// Create Fyne application
	fyneApp := app.NewWithID("io.yourorg.vlt")
	window := fyneApp.NewWindow(fmt.Sprintf("vlt %s", version))
	window.Resize(fyne.NewSize(900, 600))

	// Create and run the application
	_ = ui.NewApp(fyneApp, window, *config, logger)

	window.ShowAndRun()
}

// loadConfig reads configuration from environment variables with sensible defaults.
func loadConfig() (*ui.Config, error) {
	dir := os.Getenv("VLT_VAULT_DIR")
	if dir == "" {
		var err error
		dir, err = defaultVaultDir()
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}

	return &ui.Config{
		VaultDir:      dir,
		LockTimeoutMs: envIntOrDefault("VLT_LOCK_TIMEOUT_MS", 300_000),
		ClipboardTTLs: envIntOrDefault("VLT_CLIPBOARD_TTL_S", 30),
	}, nil
}

// defaultVaultDir returns the platform-appropriate default vault directory.
func defaultVaultDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("defaultVaultDir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "vlt", "vault"), nil

	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("defaultVaultDir: APPDATA not set")
		}
		return filepath.Join(appData, "vlt", "vault"), nil

	default: // linux, openbsd, etc.
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("defaultVaultDir: %w", err)
		}
		return filepath.Join(home, ".local", "share", "vlt", "vault"), nil
	}
}

// envIntOrDefault reads an environment variable as an int, returning a default if unset or invalid.
func envIntOrDefault(key string, defaultVal int) int {
	s := os.Getenv(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		slog.Warn("invalid env var, using default", "key", key, "value", s, "default", defaultVal)
		return defaultVal
	}
	return v
}
