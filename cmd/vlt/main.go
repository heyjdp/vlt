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

	"github.com/yourorg/vlt/internal/store"
	"github.com/yourorg/vlt/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=..." (see the
// Makefile); plain go build/run leaves it as "dev".
var version = "dev"

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

// existsFunc reports whether a directory holds a vault (store.Exists).
type existsFunc func(dir string) (bool, error)

// defaultVaultDir returns the platform-appropriate default vault directory.
func defaultVaultDir() (string, error) {
	return vaultDirFor(runtime.GOOS, os.Getenv, os.UserHomeDir, store.Exists, slog.Default())
}

// vaultDirFor returns the default vault directory for goos:
//   - macOS: ~/Library/Application Support/vlt/vault
//   - Windows: %APPDATA%\vlt\vault
//   - others: $XDG_DATA_HOME/vlt/vault, where XDG_DATA_HOME defaults to
//     ~/.local/share (a relative XDG_DATA_HOME is ignored, as the XDG Base
//     Directory spec requires). Earlier versions always used
//     ~/.local/share, so a vault found there is kept when the XDG location
//     holds none.
func vaultDirFor(goos string, getenv func(string) string, home func() (string, error), exists existsFunc, log *slog.Logger) (string, error) {
	switch goos {
	case "darwin":
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("main.vaultDirFor: %w", err)
		}
		return filepath.Join(h, "Library", "Application Support", "vlt", "vault"), nil

	case "windows":
		appData := getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("main.vaultDirFor: APPDATA not set")
		}
		return filepath.Join(appData, "vlt", "vault"), nil

	default: // linux, openbsd, etc.
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("main.vaultDirFor: %w", err)
		}
		legacy := filepath.Join(h, ".local", "share", "vlt", "vault")
		xdg := getenv("XDG_DATA_HOME")
		if !filepath.IsAbs(xdg) {
			return legacy, nil
		}
		dir := filepath.Join(xdg, "vlt", "vault")
		if dir == legacy {
			return dir, nil // no need to look for a vault in the old location
		}
		// Prefer the XDG location unless only the old one holds a vault.
		// An error means "cannot tell", which never moves the user away
		// from the XDG location.
		found, err := exists(dir)
		if err != nil {
			log.Warn("cannot check the XDG vault location; using it anyway", "dir", dir, "err", err)
			return dir, nil
		}
		if found {
			return dir, nil
		}
		old, err := exists(legacy)
		if err != nil {
			log.Warn("cannot check the old vault location", "dir", legacy, "err", err)
			return dir, nil
		}
		if old {
			log.Info("using the vault in the old location; the XDG location holds none", "dir", legacy, "xdg", dir)
			return legacy, nil
		}
		return dir, nil
	}
}
