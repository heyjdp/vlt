// Package main is the entry point for the vlt secret manager.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/awnumar/memguard"

	"github.com/yourorg/vlt/internal/ui"
)

var version = "dev" // overridden by ldflags

func main() {
	if err := run(); err != nil {
		slog.Error("vlt failed", "err", err)
		os.Exit(1)
	}
}

// run sets up and runs the application. It returns instead of exiting so
// its deferred cleanup (memguard.Purge) always runs.
func run() error {
	// Initialize memguard for secure memory handling
	memguard.CatchInterrupt()
	defer memguard.Purge()

	// Set up structured logging
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("starting vlt", "version", version)

	// Create the Fyne application; its preferences hold saved settings.
	fyneApp := app.NewWithID("io.yourorg.vlt")
	fyneApp.Settings().SetTheme(&ui.VltTheme{})

	defaultDir, err := defaultVaultDir()
	if err != nil {
		return fmt.Errorf("determine the default vault directory: %w", err)
	}
	config := ui.LoadConfig(fyneApp.Preferences(), os.Getenv, defaultDir, logger)

	window := fyneApp.NewWindow(fmt.Sprintf("vlt %s", version))
	window.Resize(fyne.NewSize(900, 600))

	_ = ui.NewApp(fyneApp, window, config, logger)
	window.ShowAndRun()
	return nil
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
