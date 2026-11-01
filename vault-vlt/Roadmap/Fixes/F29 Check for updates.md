---
id: F29
title: "Check for updates"
aliases:
  - F29
tags:
  - fix
severity: low
status: done
phase: 8
area: ui
review_finding: User request (2026-11-01)
files:
  - internal/update/update.go
  - internal/update/update_test.go
  - internal/ui/updates.go
  - internal/ui/updates_test.go
  - internal/ui/app.go
  - internal/ui/config.go
  - internal/ui/settings.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
  - Makefile
  - .github/workflows/ci.yml
depends_on: []
created: 2026-11-01
---

# F29: Check for updates

## Problem
Users had no way to learn about a new release except by visiting GitHub. A self-updater was ruled out: a secret manager that downloads and runs code is a target, and the packages are only ad-hoc signed, so an updater would have nothing to verify a download against.

## Fix
- **`internal/update`:**
  - `Checker.Latest` sends one GET to GitHub's latest-release API, with a 15 s timeout, and reads at most 1 MiB.
  - The tag must be `vX.Y.Z`. The release page link is built from the tag, never taken from the answer.
  - `IsNewer` compares the version `git describe` gives this build with the tag. It handles commits since a tag, `-dirty`, pre-releases and development builds.
- **UI:**
  - *Help → Check for Updates…* and Settings' *Check Now* show a progress dialog. Then they show one of: the newer release with its notes and *Open Release Page*; "up to date"; or, for a development build, which release is the latest. Errors are shown plainly.
  - *Check for updates when vlt starts* is off by default. When on, it announces each newer release once (`updateNotified`) and only logs failures.
  - Only `NewApp` sets the real checker, so tests never reach the network.
- **Docs:**
  - README: *Updating vlt* (replace the app; vault formats are forward-safe; checksums; why there is no self-update) and a *Network* paragraph under *Your Data*.
  - DESIGN: package notes and a security note.
  - CHANGELOG.

## Acceptance criteria
- [x] `TestIsNewer`, `TestLatest_*`
- [x] `TestCheckForUpdates_*`, `TestCheckAtStartup_*`, `TestSettings_UpdateCheckIsSavedAndCheckNowChecks`
- [x] README, DESIGN, CHANGELOG
- [x] `go test -race ./...` and golangci-lint pass

## Outcome
Verification found and fixed:
- **The start-up notice could steal the passphrase.** A modal that appeared over the unlock screen mid-typing took the keyboard focus. The notice now waits until the vault is unlocked (`App.pendingRelease`, `announceRelease`).
- **A manual check could outlive a lock.** Its result appeared over the unlock screen, against the rule that no dialog outlives the session. `App.lockGen` now counts locks, and a result arriving after one is dropped. A second request while a check runs is ignored, and nothing is announced at start-up after a manual check.
- **Every 403 read as "rate limited".** That is now reported only for 429, or for 403 with `X-RateLimit-Remaining: 0`.
- **Fragile error text.** The UI matched the update package's error text; it now checks the sentinel errors with `errors.Is`.
- **A withdrawn release could be announced again.** The start-up check now compares against the last announced tag instead of testing for equality.
- **Hardening:**
  - Control and format characters (such as bidi overrides) are stripped from the notes.
  - Redirects are followed only over HTTPS to `api.github.com`.
  - `make build` and CI's build step take the version from `vX.Y.Z` tags only (`--match 'v[0-9]*'`).
- **Tests that missed things.** 13 of 56 mutants survived at first. Tests were added for:
  - `NewApp`'s wiring (`startUpdateChecks`);
  - tags with a leading `../` or `x`;
  - the pinned limits, the notes-length boundary, the API-version header;
  - the Settings box showing a saved choice, and that it is placed on the screen;
  - dialog answers counting as activity;
  - the exact release URL.
- **Docs:**
  - Two versions running side by side were overclaimed as safe.
  - Added: macOS blocking each new download, where to run the checksum commands, Safari unzipping downloads, `chmod +x` for a new AppImage.
  - The Settings text now says the check runs only when turned on.
  - Updated: the Configuration paragraph, the layered-design diagram, the Architecture Overview and Security Model notes, and Phase 8's colour.

## Related
- [[Roadmap]]
- [[F28 About dialog for bug reports]]
- [[F31 Release checklist]]
