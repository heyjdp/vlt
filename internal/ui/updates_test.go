package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/update"
)

// fakeLatest replaces the app's release checker with one that answers rel
// or err, and returns how many times it was asked.
func fakeLatest(app *App, rel update.Release, err error) *int {
	calls := 0
	app.latest = func(context.Context) (update.Release, error) {
		calls++
		return rel, err
	}
	return &calls
}

// updateApp is a locked app at version, with a vault and a fake browser.
func updateApp(t *testing.T, version string, opts ...func(*Settings)) (*App, fyne.Window, *[]string) {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(&AppDeps{FyneApp: a, Window: w, Settings: settingsAt(a, dir, opts...), Log: quietLog(), run: inlineRunner(), timers: noTimers, storeOpts: []store.Option{testKDF}})
	app.version = version
	return app, w, openedURLs(app)
}

// checkForUpdatesMenu runs Help → Check for Updates….
func checkForUpdatesMenu(t *testing.T, w fyne.Window) {
	t.Helper()
	for _, m := range w.MainMenu().Items {
		for _, it := range m.Items {
			if m.Label == "Help" && it.Label == "Check for Updates…" {
				it.Action()
				return
			}
		}
	}
	t.Fatal("no Help → Check for Updates…")
}

// dialogText returns all label text in the topmost dialog.
func dialogText(t *testing.T, w fyne.Window) string {
	t.Helper()
	top := w.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("expected a dialog")
	}
	var b strings.Builder
	findDeep(top, func(o fyne.CanvasObject) bool {
		if l, ok := o.(*widget.Label); ok {
			b.WriteString(l.Text + "\n")
		}
		return false
	})
	return b.String()
}

func TestCheckForUpdates_OffersANewerReleaseAndOpensItsPage(t *testing.T) {
	app, w, opened := updateApp(t, "v0.1.0")
	fakeLatest(app, update.Release{Tag: "v0.1.1", Notes: "TOTP codes"}, nil)

	checkForUpdatesMenu(t, w)
	text := dialogText(t, w)
	for _, want := range []string{"vlt v0.1.1 is available; you have v0.1.0", "TOTP codes", "vault and settings are kept"} {
		if !strings.Contains(text, want) {
			t.Fatalf("dialog says %q, missing %q", text, want)
		}
	}
	test.Tap(dialogButton(t, w, "Open Release Page"))
	if len(*opened) != 1 || (*opened)[0] != "https://github.com/heyjdp/vlt/releases/tag/v0.1.1" {
		t.Fatalf("opened %v", *opened)
	}
}

func TestCheckForUpdates_CloseOpensNothing(t *testing.T) {
	app, w, opened := updateApp(t, "v0.1.0")
	fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	checkForUpdatesMenu(t, w)
	test.Tap(dialogButton(t, w, "Close"))
	if len(*opened) != 0 || w.Canvas().Overlays().Top() != nil {
		t.Fatalf("Close should just close the dialog; opened %v", *opened)
	}
}

func TestCheckForUpdates_SaysWhenUpToDate(t *testing.T) {
	app, w, opened := updateApp(t, "v0.1.1-2-gabc1234")
	fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	checkForUpdatesMenu(t, w)
	if text := dialogText(t, w); !strings.Contains(text, "You have vlt v0.1.1-2-gabc1234. The latest release is v0.1.1.") {
		t.Fatalf("dialog says %q", text)
	}
	if findButton(w.Canvas().Overlays().Top(), "Open Release Page") != nil || len(*opened) != 0 {
		t.Fatal("an up-to-date build should not offer a release")
	}
}

func TestCheckForUpdates_ADevelopmentBuildIsShownTheLatestRelease(t *testing.T) {
	app, w, opened := updateApp(t, "")
	fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	checkForUpdatesMenu(t, w)
	if text := dialogText(t, w); !strings.Contains(text, "development build (dev)") || !strings.Contains(text, "vlt v0.1.1") {
		t.Fatalf("dialog says %q", text)
	}
	test.Tap(dialogButton(t, w, "Open Release Page"))
	if len(*opened) != 1 || (*opened)[0] != "https://github.com/heyjdp/vlt/releases/tag/v0.1.1" {
		t.Fatalf("opened %v", *opened)
	}
}

func TestCheckForUpdates_AFailureIsReportedPlainly(t *testing.T) {
	app, w, _ := updateApp(t, "v0.1.0")
	fakeLatest(app, update.Release{}, fmt.Errorf("update.Latest: %w", update.ErrRateLimited))

	checkForUpdatesMenu(t, w)
	text := dialogText(t, w)
	if !strings.Contains(text, "Couldn't check for updates: GitHub's request limit was reached") || strings.Contains(text, "update.Latest") {
		t.Fatalf("dialog says %q", text)
	}
}

func TestCheckForUpdates_TestAppsNeverReachTheNetwork(t *testing.T) {
	app, _, _ := updateApp(t, "v0.1.0")
	if _, err := app.latest(context.Background()); !errors.Is(err, errNoUpdateCheck) {
		t.Fatalf("latest = %v; only NewApp may set the real checker", err)
	}
}

func TestCheckAtStartup_IsOffUnlessTurnedOn(t *testing.T) {
	app, w, _ := updateApp(t, "v0.1.0")
	calls := fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	app.checkAtStartup()
	if *calls != 0 || w.Canvas().Overlays().Top() != nil {
		t.Fatal("the startup check must not run unless turned on")
	}
}

// releaseDialogText is the text of the open dialog, or "" when none is open.
func releaseDialogText(t *testing.T, w fyne.Window) string {
	t.Helper()
	if w.Canvas().Overlays().Top() == nil {
		return ""
	}
	return dialogText(t, w)
}

func TestCheckAtStartup_WaitsForTheUnlockThenTellsAboutEachNewReleaseOnce(t *testing.T) {
	app, w, _ := updateApp(t, "v0.1.0", withUpdateChecks)
	fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	app.checkAtStartup()
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("the notice must not cover the passphrase entry")
	}
	unlock(app)
	if !strings.Contains(releaseDialogText(t, w), "vlt v0.1.1 is available") {
		t.Fatal("a newer release should be announced once the vault is unlocked")
	}
	test.Tap(dialogButton(t, w, "Close"))
	app.Lock()

	app.checkAtStartup() // the next start
	unlock(app)
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("the same release should not be announced twice")
	}

	// A withdrawn release: GitHub's latest goes back to an older tag that
	// is still newer than this build. It was covered by the announcement.
	app.Lock()
	app.version = "v0.0.9"
	fakeLatest(app, update.Release{Tag: "v0.1.0"}, nil)
	app.checkAtStartup()
	unlock(app)
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("an older release than the last announced should not be announced")
	}

	app.Lock()
	fakeLatest(app, update.Release{Tag: "v0.1.2"}, nil)
	app.checkAtStartup()
	unlock(app)
	if !strings.Contains(releaseDialogText(t, w), "vlt v0.1.2 is available") {
		t.Fatal("a later release should be announced too")
	}
	app.Lock()
}

func TestCheckAtStartup_AnnouncesAtOnceWhenAlreadyUnlocked(t *testing.T) {
	app, w, _ := updateApp(t, "v0.1.0", withUpdateChecks)
	q := &queue{}
	setRunner(app, q.runner())
	fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	app.checkAtStartup()
	setRunner(app, inlineRunner())
	unlock(app)
	q.flush() // the check finishes after the unlock (a slow network)
	if !strings.Contains(releaseDialogText(t, w), "vlt v0.1.1 is available") {
		t.Fatal("a check finishing after the unlock should announce straight away")
	}
	app.Lock()
}

func TestCheckAtStartup_SaysNothingAfterAManualCheck(t *testing.T) {
	app, w, _ := updateApp(t, "v0.1.0", withUpdateChecks)
	q := &queue{}
	setRunner(app, q.runner())
	fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	app.checkAtStartup()
	checkForUpdatesMenu(t, w)
	q.flush()
	test.Tap(dialogButton(t, w, "Close")) // the manual check's answer
	setRunner(app, inlineRunner())
	unlock(app)
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("the start-up check should not repeat what the manual check showed")
	}
	app.Lock()
}

func TestStartUpdateChecks_SetsTheCheckerThenChecks(t *testing.T) {
	app, w, _ := updateApp(t, "v0.1.0", withUpdateChecks)
	calls := 0
	app.startUpdateChecks(func(context.Context) (update.Release, error) {
		calls++
		return update.Release{Tag: "v0.1.1"}, nil
	})
	if calls != 1 {
		t.Fatalf("the start-up check asked %d times, want once with the given checker", calls)
	}
	checkForUpdatesMenu(t, w)
	if calls != 2 || !strings.Contains(dialogText(t, w), "v0.1.1 is available") {
		t.Fatal("Check for Updates should use the given checker")
	}
}

func TestCheckForUpdates_AResultAfterALockIsDropped(t *testing.T) {
	app, w, _ := updateApp(t, "v0.1.0")
	unlock(app)
	q := &queue{}
	setRunner(app, q.runner())
	calls := fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	checkForUpdatesMenu(t, w)
	checkForUpdatesMenu(t, w) // a second request while one runs is ignored
	if len(q.pending) != 1 {
		t.Fatalf("%d checks queued, want 1", len(q.pending))
	}
	app.Lock()
	q.flush()
	if *calls != 1 || w.Canvas().Overlays().Top() != nil {
		t.Fatal("a result arriving after the lock should not be shown over the unlock screen")
	}
	checkForUpdatesMenu(t, w)
	q.flush()
	if !strings.Contains(dialogText(t, w), "v0.1.1 is available") {
		t.Fatal("a new check should run once the last one finished")
	}
}

func TestCheckForUpdates_AnsweringTheDialogCountsAsActivity(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	w := test.NewTempWindow(t, nil)
	clock := &fakeClock{}
	app := newApp(&AppDeps{FyneApp: a, Window: w, Settings: settingsAt(a, dir, withLockTimeoutMs(DefaultLockTimeoutMs)), Log: quietLog(), run: inlineRunner(), timers: clock.factory, storeOpts: []store.Option{testKDF}})
	app.version = "v0.1.0"
	fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)
	unlock(app)

	checkForUpdatesMenu(t, w)
	before := len(clock.timers)
	test.Tap(dialogButton(t, w, "Close"))
	if len(clock.timers) == before {
		t.Fatal("closing the dialog should restart the auto-lock countdown")
	}
	app.Lock()
}

func TestCheckAtStartup_IsSilentWhenUpToDateUnknownOrFailing(t *testing.T) {
	for _, tt := range []struct {
		name, version string
		rel           update.Release
		err           error
	}{
		{"up to date", "v0.1.1", update.Release{Tag: "v0.1.1"}, nil},
		{"development build", "", update.Release{Tag: "v0.1.1"}, nil},
		{"offline", "v0.1.0", update.Release{}, errors.New("dial tcp: no route to host")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, w, _ := updateApp(t, tt.version, withUpdateChecks)
			calls := fakeLatest(app, tt.rel, tt.err)
			app.checkAtStartup()
			if *calls != 1 || w.Canvas().Overlays().Top() != nil {
				t.Fatalf("calls = %d; the startup check should show nothing", *calls)
			}
			if app.fyneApp.Preferences().String(prefUpdateNotified) != "" {
				t.Fatal("nothing was announced, so nothing should be recorded")
			}
		})
	}
}

// updateCheck returns the Settings screen's start-up check box.
func updateCheck(t *testing.T, ss *SettingsScreen) *widget.Check {
	t.Helper()
	found := findDeep(ss.Content(), func(o fyne.CanvasObject) bool {
		c, ok := o.(*widget.Check)
		return ok && c.Text == "Check for updates when vlt starts"
	})
	if found == nil {
		t.Fatal("Settings has no update check box")
	}
	return found.(*widget.Check)
}

func TestSettings_ShowsTheSavedUpdateChoice(t *testing.T) {
	app, ss := settingsApp(t, noTimers, withUpdateChecks)
	if !updateCheck(t, ss).Checked {
		t.Fatal("the box should show the saved choice")
	}
	app.Lock()
}

func TestSettings_UpdateCheckIsSavedAndCheckNowChecks(t *testing.T) {
	app, ss := settingsApp(t, noTimers)
	calls := fakeLatest(app, update.Release{Tag: "v0.1.1"}, nil)

	check := updateCheck(t, ss)
	if check.Checked {
		t.Fatal("the startup check should be off by default")
	}
	check.SetChecked(true)
	if !app.settings.CheckUpdates() || !app.fyneApp.Preferences().Bool(prefCheckUpdates) {
		t.Fatal("turning the startup check on should be saved")
	}
	if !LoadSettings(app.fyneApp.Preferences(), env(nil), "/default", quietLog()).CheckUpdates() {
		t.Fatal("the saved choice should be loaded on the next start")
	}

	test.Tap(findButton(ss.Content(), "Check Now"))
	if *calls != 1 {
		t.Fatalf("Check Now asked %d times", *calls)
	}
	check.SetChecked(false)
	if app.fyneApp.Preferences().Bool(prefCheckUpdates) {
		t.Fatal("turning it off should be saved")
	}
	app.Lock()
}

// setRunner makes the app run its background work, including the update
// checks its operations start, on r.
func setRunner(app *App, r runner) {
	app.run = r
	app.ops.run = r
}
