package ui

import (
	"errors"
	"net/url"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestReadBuildDetails(t *testing.T) {
	t.Parallel()
	info := &debug.BuildInfo{
		Deps: []*debug.Module{{Path: "fyne.io/fyne/v2", Version: "v2.7.3"}},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "2411ba7c0ffee1234567890"},
			{Key: "vcs.time", Value: "2026-10-29T15:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	d := readBuildDetails("v0.1.0", info, true)
	if d.Version != "v0.1.0" || d.Commit != "2411ba7c0ffe-modified" || d.Built != "2026-10-29T15:00:00Z" || d.Fyne != "v2.7.3" {
		t.Fatalf("details = %+v", d)
	}
	if d.Platform != runtime.GOOS+"/"+runtime.GOARCH || d.Go != runtime.Version() {
		t.Fatalf("platform = %q, go = %q", d.Platform, d.Go)
	}

	// No build info (some test binaries), and no version from ldflags.
	d = readBuildDetails("", nil, false)
	if d.Version != "dev" || d.Commit != "" {
		t.Fatalf("details without build info = %+v", d)
	}

	// A dirty tree with no revision recorded adds nothing on its own.
	d = readBuildDetails("v0.1.0", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}}, true)
	if d.Commit != "" {
		t.Fatalf("commit = %q, want none", d.Commit)
	}
}

func TestBuildDetails_String(t *testing.T) {
	t.Parallel()
	d := buildDetails{
		Version: "v0.1.0", Commit: "2411ba7c0ffe", Built: "2026-10-29T15:00:00Z",
		Go: "go1.27.3", Platform: "darwin/arm64", Fyne: "v2.7.3", Vault: "on a server", Locked: true,
	}
	want := "vlt v0.1.0\nCommit: 2411ba7c0ffe (2026-10-29T15:00:00Z)\nPlatform: darwin/arm64, go1.27.3, Fyne v2.7.3\nVault: on a server, locked\n"
	if got := d.String(); got != want {
		t.Fatalf("String() =\n%s\nwant\n%s", got, want)
	}
}

func TestIssueURL_CarriesTheDetails(t *testing.T) {
	t.Parallel()
	details := "vlt v0.1.0 & co + #1 100%\n"
	link := issueURL(details)
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "github.com" || u.Path != "/heyjdp/vlt/issues/new" {
		t.Fatalf("link = %s", link)
	}
	if body := u.Query().Get("body"); !strings.Contains(body, details) || !strings.Contains(body, "don't paste secrets") {
		t.Fatalf("body = %q", body)
	}
}

// aboutText returns the build details label in the open About dialog.
func aboutText(t *testing.T, w fyne.Window) string {
	t.Helper()
	top := w.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("expected the About dialog")
	}
	label := findDeep(top, func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && strings.HasPrefix(l.Text, "vlt ")
	})
	if label == nil {
		t.Fatal("the About dialog shows no build details")
	}
	return label.(*widget.Label).Text
}

func TestAbout_IsReachableWhileLockedAndCopiesTheDetails(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	app.version = "v0.1.0"

	about := findButton(w.Content(), "About")
	if about == nil {
		t.Fatal("the unlock screen should have an About button")
	}
	test.Tap(about)
	text := aboutText(t, w)
	for _, want := range []string{"vlt v0.1.0", runtime.GOOS + "/" + runtime.GOARCH, "Vault: local, locked"} {
		if !strings.Contains(text, want) {
			t.Fatalf("About shows %q, missing %q", text, want)
		}
	}
	if strings.Contains(text, dir) {
		t.Fatal("About must not show the vault's path")
	}
	test.Tap(findButton(w.Canvas().Overlays().Top(), "Copy Details"))
	if got, want := a.Clipboard().Content(), app.aboutDetails().String(); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
	if findButton(w.Canvas().Overlays().Top(), "Copied") == nil {
		t.Fatal("the button should confirm the copy")
	}
}

func TestAbout_IsInTheMainViewAndTheHelpMenu(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	test.Tap(findButton(w.Content(), "About"))
	if !strings.Contains(aboutText(t, w), "Vault: local, unlocked") {
		t.Fatal("About in the main view should say the vault is unlocked")
	}
	app.Lock()

	var items []string
	for _, m := range w.MainMenu().Items {
		if m.Label == "Help" {
			for _, it := range m.Items {
				items = append(items, it.Label)
			}
		}
	}
	// "About" exactly: Fyne moves only that label into the macOS app menu.
	if strings.Join(items, ",") != "About,Report a Bug…" {
		t.Fatalf("Help menu = %v", items)
	}
}

// openedURLs replaces the app's browser with one that records links.
func openedURLs(app *App) *[]string {
	var opened []string
	app.open = func(u *url.URL) error {
		opened = append(opened, u.String())
		return nil
	}
	return &opened
}

func TestAbout_ReportABugOpensAnIssueWithTheDetails(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	app.version = "v0.1.0"
	opened := openedURLs(app)

	test.Tap(findButton(w.Content(), "About"))
	test.Tap(findButton(w.Canvas().Overlays().Top(), "Report a Bug…"))
	for _, m := range w.MainMenu().Items {
		for _, it := range m.Items {
			if m.Label == "Help" && it.Label == "Report a Bug…" {
				it.Action()
			}
		}
	}
	if len(*opened) != 2 {
		t.Fatalf("opened %v, want the issue page twice (dialog and menu)", *opened)
	}
	for _, link := range *opened {
		u, err := url.Parse(link)
		if err != nil || u.Path != "/heyjdp/vlt/issues/new" || !strings.Contains(u.Query().Get("body"), app.aboutDetails().String()) {
			t.Fatalf("opened %s", link)
		}
	}
}

func TestAbout_TheAboutMenuItemOpensTheDialog(t *testing.T) {
	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: t.TempDir()}, quietLog(), inlineRunner(), noTimers, testKDF)
	app.version = "v0.1.0"
	for _, m := range w.MainMenu().Items {
		for _, it := range m.Items {
			if it.Label == "About" {
				it.Action()
			}
		}
	}
	if !strings.HasPrefix(aboutText(t, w), "vlt v0.1.0") {
		t.Fatal("the About menu item should open the dialog")
	}
}

func TestAbout_AFailedBrowserIsReported(t *testing.T) {
	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: t.TempDir()}, quietLog(), inlineRunner(), noTimers, testKDF)
	app.open = func(*url.URL) error { return errors.New("no browser") }

	app.openURL(issueURL("vlt dev\n"))
	top := w.Canvas().Overlays().Top()
	if top == nil || findDeep(top, func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && strings.Contains(l.Text, "no browser") && strings.Contains(l.Text, projectURL)
	}) == nil {
		t.Fatal("a failure to open the browser should be shown, with the project's address")
	}
}

func TestAbout_AServerVaultIsNotNamed(t *testing.T) {
	fakeSSH(t)
	a := test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: "vltuser@nas.example:/home/vltuser/vault"}, quietLog(), inlineRunner(), noTimers, testKDF)

	text := app.aboutDetails().String()
	if !strings.Contains(text, "Vault: on a server, locked") {
		t.Fatalf("details = %q", text)
	}
	for _, leak := range []string{"vltuser", "nas.example", "/home"} {
		if strings.Contains(text, leak) {
			t.Fatalf("details name the vault (%q): %q", leak, text)
		}
	}
}

// NewApp itself starts real background work, which would outlive the test,
// so this goes through newVersionedApp, which NewApp calls with the version.
func TestNewApp_KeepsTheVersion(t *testing.T) {
	a := test.NewTempApp(t)
	app := newVersionedApp("v0.1.0", a, test.NewTempWindow(t, nil), Config{VaultDir: t.TempDir()}, quietLog(), inlineRunner(), noTimers, testKDF)
	if app.version != "v0.1.0" || !strings.HasPrefix(app.aboutDetails().String(), "vlt v0.1.0\n") {
		t.Fatalf("version = %q", app.version)
	}
}
