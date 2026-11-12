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

	"github.com/heyjdp/vlt/internal/update"
)

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
	r := newAppRig(t, withVersion("v0.1.0"))
	w := r.window
	r.releases(update.Release{Tag: "v0.1.1", Notes: "TOTP codes"}, nil)

	checkForUpdatesMenu(t, w)
	text := dialogText(t, w)
	for _, want := range []string{"vlt v0.1.1 is available; you have v0.1.0", "TOTP codes", "vault and settings are kept"} {
		if !strings.Contains(text, want) {
			t.Fatalf("dialog says %q, missing %q", text, want)
		}
	}
	test.Tap(dialogButton(t, w, "Open Release Page"))
	if len(r.opened) != 1 || r.opened[0] != "https://github.com/heyjdp/vlt/releases/tag/v0.1.1" {
		t.Fatalf("opened %v", r.opened)
	}
}

func TestCheckForUpdates_CloseOpensNothing(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.0"))
	w := r.window
	r.releases(update.Release{Tag: "v0.1.1"}, nil)

	checkForUpdatesMenu(t, w)
	test.Tap(dialogButton(t, w, "Close"))
	if len(r.opened) != 0 || w.Canvas().Overlays().Top() != nil {
		t.Fatalf("Close should just close the dialog; opened %v", r.opened)
	}
}

func TestCheckForUpdates_SaysWhenUpToDate(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.1-2-gabc1234"))
	w := r.window
	r.releases(update.Release{Tag: "v0.1.1"}, nil)

	checkForUpdatesMenu(t, w)
	if text := dialogText(t, w); !strings.Contains(text, "You have vlt v0.1.1-2-gabc1234. The latest release is v0.1.1.") {
		t.Fatalf("dialog says %q", text)
	}
	if findButton(w.Canvas().Overlays().Top(), "Open Release Page") != nil || len(r.opened) != 0 {
		t.Fatal("an up-to-date build should not offer a release")
	}
}

func TestCheckForUpdates_ADevelopmentBuildIsShownTheLatestRelease(t *testing.T) {
	r := newAppRig(t, withVersion(""))
	w := r.window
	r.releases(update.Release{Tag: "v0.1.1"}, nil)

	checkForUpdatesMenu(t, w)
	if text := dialogText(t, w); !strings.Contains(text, "development build (dev)") || !strings.Contains(text, "vlt v0.1.1") {
		t.Fatalf("dialog says %q", text)
	}
	test.Tap(dialogButton(t, w, "Open Release Page"))
	if len(r.opened) != 1 || r.opened[0] != "https://github.com/heyjdp/vlt/releases/tag/v0.1.1" {
		t.Fatalf("opened %v", r.opened)
	}
}

func TestCheckForUpdates_AFailureIsReportedPlainly(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.0"))
	w := r.window
	r.releases(update.Release{}, fmt.Errorf("update.Latest: %w", update.ErrRateLimited))

	checkForUpdatesMenu(t, w)
	text := dialogText(t, w)
	if !strings.Contains(text, "Couldn't check for updates: GitHub's request limit was reached") || strings.Contains(text, "update.Latest") {
		t.Fatalf("dialog says %q", text)
	}
}

func TestCheckForUpdates_TestAppsNeverReachTheNetwork(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.0"))
	if _, err := r.app.latest(context.Background()); !errors.Is(err, errNoUpdateCheck) {
		t.Fatalf("latest = %v; only NewApp may set the real checker", err)
	}
}

func TestCheckAtStartup_IsOffUnlessTurnedOn(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.0"))
	w := r.window
	calls := r.releases(update.Release{Tag: "v0.1.1"}, nil)

	r.app.checkAtStartup()
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
	r := newAppRig(t, withVersion("v0.1.0"), withSettings(withUpdateChecks))
	w := r.window
	r.releases(update.Release{Tag: "v0.1.1"}, nil)

	r.app.checkAtStartup()
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("the notice must not cover the passphrase entry")
	}
	r.unlock()
	if !strings.Contains(releaseDialogText(t, w), "vlt v0.1.1 is available") {
		t.Fatal("a newer release should be announced once the vault is unlocked")
	}
	test.Tap(dialogButton(t, w, "Close"))
	r.lock()

	r.app.checkAtStartup() // the next start
	r.unlock()
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("the same release should not be announced twice")
	}

	// A withdrawn release: GitHub's latest goes back to an older tag that
	// is still newer than this build. It was covered by the announcement.
	r.lock()
	r.app.version = "v0.0.9"
	r.releases(update.Release{Tag: "v0.1.0"}, nil)
	r.app.checkAtStartup()
	r.unlock()
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("an older release than the last announced should not be announced")
	}

	r.lock()
	r.releases(update.Release{Tag: "v0.1.2"}, nil)
	r.app.checkAtStartup()
	r.unlock()
	if !strings.Contains(releaseDialogText(t, w), "vlt v0.1.2 is available") {
		t.Fatal("a later release should be announced too")
	}
	r.lock()
}

func TestCheckAtStartup_AnnouncesAtOnceWhenAlreadyUnlocked(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.0"), withSettings(withUpdateChecks))
	w := r.window
	r.hold()
	r.releases(update.Release{Tag: "v0.1.1"}, nil)

	r.app.checkAtStartup()
	r.runInline()
	r.unlock()
	r.flush() // the check finishes after the unlock (a slow network)
	if !strings.Contains(releaseDialogText(t, w), "vlt v0.1.1 is available") {
		t.Fatal("a check finishing after the unlock should announce straight away")
	}
	r.lock()
}

func TestCheckAtStartup_SaysNothingAfterAManualCheck(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.0"), withSettings(withUpdateChecks))
	w := r.window
	r.hold()
	r.releases(update.Release{Tag: "v0.1.1"}, nil)

	r.app.checkAtStartup()
	checkForUpdatesMenu(t, w)
	r.flush()
	test.Tap(dialogButton(t, w, "Close")) // the manual check's answer
	r.runInline()
	r.unlock()
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("the start-up check should not repeat what the manual check showed")
	}
	r.lock()
}

func TestStartUpdateChecks_SetsTheCheckerThenChecks(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.0"), withSettings(withUpdateChecks))
	w := r.window
	calls := 0
	r.app.startUpdateChecks(func(context.Context) (update.Release, error) {
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
	r := newAppRig(t, withVersion("v0.1.0"))
	w := r.window
	r.unlock()
	r.hold()
	calls := r.releases(update.Release{Tag: "v0.1.1"}, nil)

	checkForUpdatesMenu(t, w)
	checkForUpdatesMenu(t, w) // a second request while one runs is ignored
	if r.queued() != 1 {
		t.Fatalf("%d checks queued, want 1", r.queued())
	}
	r.lock()
	r.flush()
	if *calls != 1 || w.Canvas().Overlays().Top() != nil {
		t.Fatal("a result arriving after the lock should not be shown over the unlock screen")
	}
	checkForUpdatesMenu(t, w)
	r.flush()
	if !strings.Contains(dialogText(t, w), "v0.1.1 is available") {
		t.Fatal("a new check should run once the last one finished")
	}
}

func TestCheckForUpdates_AnsweringTheDialogCountsAsActivity(t *testing.T) {
	r := newAppRig(t, withVersion("v0.1.0"), withSettings(withLockTimeoutMs(DefaultLockTimeoutMs)))
	w, clock := r.window, r.clock
	r.releases(update.Release{Tag: "v0.1.1"}, nil)
	r.unlock()

	checkForUpdatesMenu(t, w)
	before := len(clock.timers)
	test.Tap(dialogButton(t, w, "Close"))
	if len(clock.timers) == before {
		t.Fatal("closing the dialog should restart the auto-lock countdown")
	}
	r.lock()
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
			r := newAppRig(t, withVersion(tt.version), withSettings(withUpdateChecks))
			w := r.window
			calls := r.releases(tt.rel, tt.err)
			r.app.checkAtStartup()
			if *calls != 1 || w.Canvas().Overlays().Top() != nil {
				t.Fatalf("calls = %d; the startup check should show nothing", *calls)
			}
			if r.prefs().String(prefUpdateNotified) != "" {
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
	r, ss := settingsApp(t, withSettings(withUpdateChecks))
	if !updateCheck(t, ss).Checked {
		t.Fatal("the box should show the saved choice")
	}
	r.lock()
}

func TestSettings_UpdateCheckIsSavedAndCheckNowChecks(t *testing.T) {
	r, ss := settingsApp(t)
	calls := r.releases(update.Release{Tag: "v0.1.1"}, nil)

	check := updateCheck(t, ss)
	if check.Checked {
		t.Fatal("the startup check should be off by default")
	}
	check.SetChecked(true)
	if !r.settings.CheckUpdates() || !r.prefs().Bool(prefCheckUpdates) {
		t.Fatal("turning the startup check on should be saved")
	}
	if !LoadSettings(r.prefs(), env(nil), "/default", quietLog()).CheckUpdates() {
		t.Fatal("the saved choice should be loaded on the next start")
	}

	test.Tap(findButton(ss.Content(), "Check Now"))
	if *calls != 1 {
		t.Fatalf("Check Now asked %d times", *calls)
	}
	check.SetChecked(false)
	if r.prefs().Bool(prefCheckUpdates) {
		t.Fatal("turning it off should be saved")
	}
	r.lock()
}
