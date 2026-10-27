package main

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestVaultDirFor(t *testing.T) {
	t.Parallel()

	const h = "/home/me"
	legacy := filepath.Join(h, ".local", "share", "vlt", "vault")
	// Absolute on every host (C:\data on Windows), as XDG_DATA_HOME must be.
	dataHome, err := filepath.Abs(filepath.FromSlash("/data"))
	if err != nil {
		t.Fatal(err)
	}
	xdgDir := filepath.Join(dataHome, "vlt", "vault")
	appData := `C:\Users\me\AppData\Roaming`
	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		vaults  []string // directories that hold a vault
		failing string   // a directory whose check fails
		want    string
		wantErr bool
	}{
		{name: "macOS", goos: "darwin", want: filepath.Join(h, "Library", "Application Support", "vlt", "vault")},
		{name: "Windows", goos: "windows", env: map[string]string{"APPDATA": appData}, want: filepath.Join(appData, "vlt", "vault")},
		{name: "Windows without APPDATA", goos: "windows", wantErr: true},
		{name: "Linux default", goos: "linux", want: legacy},
		{name: "Linux XDG_DATA_HOME", goos: "linux", env: map[string]string{"XDG_DATA_HOME": dataHome}, want: xdgDir},
		{name: "relative XDG_DATA_HOME is ignored", goos: "linux", env: map[string]string{"XDG_DATA_HOME": "data"}, want: legacy},
		{name: "XDG_DATA_HOME same as default", goos: "linux", env: map[string]string{"XDG_DATA_HOME": filepath.Join(h, ".local", "share")}, want: legacy},
		{name: "vault only in the old location", goos: "linux", env: map[string]string{"XDG_DATA_HOME": dataHome}, vaults: []string{legacy}, want: legacy},
		{name: "vaults in both, XDG wins", goos: "linux", env: map[string]string{"XDG_DATA_HOME": dataHome}, vaults: []string{legacy, xdgDir}, want: xdgDir},
		{name: "XDG check fails, XDG kept", goos: "linux", env: map[string]string{"XDG_DATA_HOME": dataHome}, vaults: []string{legacy}, failing: xdgDir, want: xdgDir},
		{name: "old-location check fails, XDG kept", goos: "linux", env: map[string]string{"XDG_DATA_HOME": dataHome}, failing: legacy, want: xdgDir},
		{name: "OpenBSD follows XDG too", goos: "openbsd", env: map[string]string{"XDG_DATA_HOME": dataHome}, want: xdgDir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(k string) string { return tt.env[k] }
			home := func() (string, error) { return h, nil }
			exists := func(dir string) (bool, error) {
				if dir == tt.failing {
					return false, errors.New("permission denied")
				}
				for _, v := range tt.vaults {
					if v == dir {
						return true, nil
					}
				}
				return false, nil
			}
			got, err := vaultDirFor(tt.goos, getenv, home, exists, quietLog())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want an error, got %q", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("vaultDirFor = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestVaultDirFor_HomeErrorIsReported(t *testing.T) {
	t.Parallel()

	noHome := func() (string, error) { return "", errors.New("no home") }
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			if _, err := vaultDirFor(goos, func(string) string { return "" }, noHome, nil, quietLog()); err == nil {
				t.Fatal("want an error when the home directory is unknown")
			}
		})
	}
}
