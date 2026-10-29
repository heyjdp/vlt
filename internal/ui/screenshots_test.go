package ui

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/heyjdp/vlt/internal/store"
)

// envScreenshots names the directory TestScreenshots writes to; the test is
// skipped when it is unset. `make screenshots` sets it to docs/screenshots.
const envScreenshots = "VLT_SCREENSHOTS"

// TestScreenshots renders the main screens, in dark and light mode, with
// Fyne's software renderer (no display needed) into PNGs for the README.
// The vault holds made-up sample secrets.
func TestScreenshots(t *testing.T) {
	out := os.Getenv(envScreenshots)
	if out == "" {
		t.Skip(envScreenshots + " is not set")
	}
	if !filepath.IsAbs(out) {
		t.Fatal(envScreenshots + " must be an absolute path (go test runs in the package directory)")
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct {
		name string
		v    fyne.ThemeVariant
	}{{"dark", theme.VariantDark}, {"light", theme.VariantLight}} {
		a := test.NewTempApp(t)
		a.Settings().SetTheme(fixedVariant{&VltTheme{}, variant.v})
		w := test.NewTempWindow(t, nil)
		size := fyne.NewSize(900, 600)
		w.Resize(size)

		// The unlock screen names the vault; show it under a home folder.
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, "Library", "Application Support", "vlt", "vault")
		newVaultWith(t, dir, sampleSecrets()...)
		app := newApp(a, w, Config{VaultDir: dir, LockTimeoutMs: DefaultLockTimeoutMs, ClipboardTTLs: 30},
			quietLog(), inlineRunner(), noTimers, testKDF)
		w.Resize(size)
		capture(t, w, filepath.Join(out, variant.name+"-unlock.png"))

		unlock(app)
		w.Resize(size)
		for i, s := range app.secretList.filtered {
			if s.Type == store.TypeRecord {
				app.secretList.list.Select(i)
				break
			}
		}
		capture(t, w, filepath.Join(out, variant.name+"-main.png"))
		app.Lock()
	}
}

func sampleSecrets() []*store.Secret {
	return []*store.Secret{
		{Version: 1, Type: store.TypePassword, Name: "GitHub Token", Group: "Work", Value: "sample", Tags: []string{"dev"}},
		{
			Version: 1, Type: store.TypeRecord, Name: "AWS Console", Group: "Work",
			URL: "https://console.aws.amazon.com", Username: "admin", Password: "sample", Tags: []string{"cloud"},
		},
		{Version: 1, Type: store.TypePassword, Name: "Home Wi-Fi", Group: "Home", Value: "sample"},
		{
			Version: 1, Type: store.TypeFile, Name: "Deploy Key", Group: "Work",
			OriginalFilename: "id_ed25519", MIMEType: "application/octet-stream", Value: "c2FtcGxl", Encoding: "base64",
		},
		{Version: 1, Type: store.TypeRecord, Name: "Bank", Group: "Personal Finance", URL: "https://bank.example", Username: "me", Password: "sample"},
	}
}

func capture(t *testing.T, w fyne.Window, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// fixedVariant shows a theme in one variant whatever the system setting.
type fixedVariant struct {
	fyne.Theme
	variant fyne.ThemeVariant
}

func (f fixedVariant) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return f.Theme.Color(name, f.variant)
}
