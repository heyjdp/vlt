package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/update"
)

// latestRelease fetches vlt's newest release (update.Checker.Latest in
// production).
type latestRelease func(context.Context) (update.Release, error)

// errNoUpdateCheck is what the app's checker returns until NewApp sets the
// real one, so tests never reach the network.
var errNoUpdateCheck = errors.New("update checks are not available in this build")

// noUpdateCheck is the checker of an app not made by NewApp.
func noUpdateCheck(context.Context) (update.Release, error) {
	return update.Release{}, errNoUpdateCheck
}

// startUpdateChecks sets the release checker and, if the user turned it on,
// checks once in the background (NewApp passes GitHub's checker).
func (a *App) startUpdateChecks(latest latestRelease) {
	a.latest = latest
	a.checkAtStartup()
}

// checkForUpdates asks GitHub for the latest release and says whether it is
// newer than this build. Nothing is downloaded: a newer release is offered
// as a link to its page. A second request while one is running is ignored,
// and a result that arrives after the vault locked is dropped, so no dialog
// from the session appears over the unlock screen.
func (a *App) checkForUpdates() {
	if a.checkingUpdates {
		return
	}
	a.checkingUpdates = true
	a.checkedManually = true // the start-up check has nothing left to announce
	a.mu.Lock()
	gen := a.lockGen
	a.mu.Unlock()

	progress := dialog.NewCustomWithoutButtons("Checking for Updates",
		container.NewVBox(muted("Asking GitHub for the latest release…"), widget.NewProgressBarInfinite()), a.window)
	progress.Show()
	background(a.run, a.latest, func(rel update.Release, err error) {
		a.checkingUpdates = false
		progress.Hide()
		a.mu.Lock()
		locked := a.lockGen != gen
		a.mu.Unlock()
		if locked {
			return
		}
		a.showUpdateResult(rel, err)
	})
}

// showUpdateResult reports the outcome of a check the user asked for.
func (a *App) showUpdateResult(rel update.Release, err error) {
	if err != nil {
		a.log.Warn("update check failed", "err", err)
		dialog.ShowError(errors.New("cannot check for updates: "+updateErrorText(err)), a.window)
		return
	}
	version := a.versionName()
	newer, known := update.IsNewer(version, rel.Tag)
	switch {
	case newer:
		a.showUpdateAvailable(rel)
	case known:
		dialog.ShowInformation("vlt Is Up to Date",
			fmt.Sprintf("You have vlt %s. The latest release is %s.", version, rel.Tag), a.window)
	default:
		a.showRelease(rel, "Latest Release",
			fmt.Sprintf("This is a development build (%s), so it can't be compared. The latest release is vlt %s.", version, rel.Tag))
	}
}

// updateErrorText describes a failed check for people: the update
// package's own errors as they are, anything else (a network error) without
// the function prefix.
func updateErrorText(err error) string {
	for _, known := range []error{update.ErrNoRelease, update.ErrRateLimited} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return strings.TrimPrefix(err.Error(), "update.Latest: ")
}

// showUpdateAvailable offers a release newer than this build.
func (a *App) showUpdateAvailable(rel update.Release) {
	a.showRelease(rel, "Update Available",
		fmt.Sprintf("vlt %s is available; you have %s. Download it from the release page and replace this copy: your vault and settings are kept.",
			rel.Tag, a.versionName()))
}

// showRelease shows a release's notes, with a button to open its page.
func (a *App) showRelease(rel update.Release, title, intro string) {
	message := widget.NewLabel(intro)
	message.Wrapping = fyne.TextWrapWord
	objects := []fyne.CanvasObject{message}
	if rel.Notes != "" {
		notes := widget.NewLabel(rel.Notes)
		notes.Wrapping = fyne.TextWrapWord
		scroll := container.NewVScroll(notes)
		scroll.SetMinSize(fyne.NewSize(0, 180))
		objects = append(objects, subheading("Release notes"), card(scroll))
	}
	d := dialog.NewCustomConfirm(title, "Open Release Page", "Close",
		fixedWidth(container.NewVBox(objects...), 440),
		func(open bool) {
			a.resetLockTimer() // answering a dialog counts as activity
			if open {
				a.openURL(rel.PageURL())
			}
		}, a.window)
	d.Show()
}

// versionName is the build's version, or "dev" when none was set.
func (a *App) versionName() string {
	if a.version == "" {
		return "dev"
	}
	return a.version
}

// checkAtStartup checks for updates in the background if the user turned
// that on in Settings. A newer release than any announced before is held
// until the vault is unlocked (see announceRelease), so the notice never
// takes the focus from the passphrase entry. A failed check is only logged:
// it must never get in the way of unlocking.
func (a *App) checkAtStartup() {
	if !a.config.CheckUpdates {
		return
	}
	background(a.run, a.latest, func(rel update.Release, err error) {
		if err != nil {
			a.log.Info("update check failed", "err", err)
			return
		}
		if newer, _ := update.IsNewer(a.versionName(), rel.Tag); !newer || a.checkedManually {
			return
		}
		// Announce each release once: only one newer than the last announced.
		if last := a.fyneApp.Preferences().String(prefUpdateNotified); last != "" {
			if newer, ok := update.IsNewer(last, rel.Tag); ok && !newer {
				return
			}
		}
		a.log.Info("update available", "latest", rel.Tag, "running", a.versionName())
		a.pendingRelease = &rel
		a.announceRelease()
	})
}

// announceRelease shows the release the start-up check found, once the main
// view is showing; until then it waits, and unlocking calls it again. It
// runs on the main goroutine.
func (a *App) announceRelease() {
	if a.pendingRelease == nil || a.mainContent == nil || a.checkedManually {
		return
	}
	rel := *a.pendingRelease
	a.pendingRelease = nil
	a.fyneApp.Preferences().SetString(prefUpdateNotified, rel.Tag)
	a.showUpdateAvailable(rel)
}
