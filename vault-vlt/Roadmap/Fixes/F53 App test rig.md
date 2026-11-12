---
id: F53
title: "App test rig"
aliases:
  - F53
tags:
  - fix
severity: low
status: in-progress
phase: 14
area: tests
review_finding: "Architecture review 2026-11-12 (fourth pass), candidate A"
files:
  - internal/ui/app.go
  - cmd/vlt/main.go
  - internal/ui/apprig_test.go
  - internal/ui/helpers_test.go
  - internal/ui/app_test.go
  - internal/ui/create_test.go
  - internal/ui/remote_test.go
  - internal/ui/flows_test.go
  - internal/ui/activity_test.go
  - internal/ui/updates_test.go
  - internal/ui/about_test.go
  - internal/ui/hygiene_test.go
  - internal/ui/clipboard_test.go
  - internal/ui/fileops_test.go
  - internal/ui/style_test.go
  - internal/ui/screenshots_test.go
  - internal/ui/config_test.go
  - internal/ui/settings_test.go
  - DESIGN.md
  - README.md
depends_on:
  - F52
  - F51
  - F50
created: 2026-11-12
---

# F53: App test rig

## Problem
- **App was built by hand in about 55 tests.** Each repeated the same lines: a test app, a temporary folder, a vault in it, a window, settings, and `newApp(a, w, cfg, quietLog(), runner, timers, testKDF)`: seven positional parameters plus store options. `newVersionedApp` existed only to set the version, yet `updateApp` set `app.version` after construction instead.
- **Nine rigs each fixed different choices**: `newFlowApp`, `newCreateApp`, `newLockingApp`, `settingsApp`, `queuedSettingsWith`, `exportApp`, `updateApp`, `queuedLockApp` and `unlockQueued`, plus `pickerOf`, `setRunner`, `fakeLatest` and `openedURLs`, which swapped App's fields after construction.
- **Tests reached past App's interface.** 254 references to App's fields from the test files (`app.unlockScreen.entry`, `app.createScreen`, `app.main.list.list`, `app.session.Store()`, …), so a change to App's fields broke tests that only wanted "unlock" or "is it locked?". App is the most-changed file.
- **`config_test.go` (about 1,050 lines) mixed two modules**: the Settings module's own tests and 18 tests of the Settings screen.

## Fix
- **`AppDeps` (`internal/ui/app.go`).** One struct replaces the positional parameters and `newVersionedApp`: the Fyne app, window, settings, logger and version (what `main` sets), and, unexported, the seams tests replace: the runner, the timer factory, the picker, the browser and store options. `NewApp(&AppDeps{…})` fills the seams in for production and starts the start-up update check; `newApp(&AppDeps{…})` builds the same app without it. Behaviour is unchanged.
- **One rig (`internal/ui/apprig_test.go`).** `newAppRig(t, opts…)` builds an App over an empty vault in a new folder, with work run inline, a fake clock, a scripted picker, a browser that records links and the cheap work factor, and shows its first screen. Options: `withSecrets`, `withoutVault`, `atLocation` (a prepared folder, a server, a bad location, as written), `withSettings`, `withLoadedSettings` (settings the test loads from the test app's preferences, with environment variables), `withQueuedRunner`, `withVersion`, `withFyneDialogs`, `withFailingBrowser`. It keeps the test app, window, settings, vault folder, clock, queue, picker and opened links.
- **Verbs and queries.** `unlock`, `unlockWith`, `unlockOpening` (a held runner: only the open runs), `createConfirming`, `lock`, `hold`, `runInline`, `flush`, `runNext`, `importFile`, `selectSecret`, `search`, `openSettings`, `stayUnlocked`, `lockNow`, `releases` (the release checker's answer); `shownScreen` (checking, unlock, create, vault error, main view; it fails the test if App holds two screens at once), `locked`, `store`, `busy` and `usable` (every input of the unlock or create screen disabled with progress shown, or the reverse), `screenError`, `passphraseLeft`, `listed`, `detail`, `mainView`, `status`, `lockWarningShown`, `queued`, `prefs`.
- **Migration.** Every App-building test uses the rig; the nine rigs and the field-swapping helpers are gone. `settingsApp` and `queuedSettings` remain as Settings-screen fixtures built on the rig (unlocked, Settings open). Tests that check App's own wiring still reach in where that is the point (`openExistingVault`, `closeVaults`, `quit`, `checkAtStartup`, the clipboard countdown, the session passed to the opener's release check).
- **`settings_test.go`.** The 18 Settings screen tests and their fixtures moved out of `config_test.go`, with `TestPassphraseChangeFailure` (whose function is in `settings.go`); `config_test.go` keeps the Settings module's tests, the preference migration and the two App tests of when the location is saved.
- **Decision: `AppDeps` is exported, with unexported seams.** `main` sets the exported fields and cannot reach the seams, so `NewApp` filling them in cannot be bypassed in production. The release checker stays out of it: `NewApp` sets it with the start-up check in one step (`startUpdateChecks`), and the rig's `releases` replaces it.
- **Decision: the rig's runner is switchable.** `hold` and `runInline` replace `setRunner`, so the app's own work, the opener's and the session's all follow one switch.
- **Decision: the rig's vault folder is `<temp>/vault`,** so a test can delete or replace the vault without touching the temporary folder itself.
- **Decision: the project trees stay without test files,** as before; the rig is described in DESIGN (app.go) and in README's test list.

## Acceptance criteria
- [x] `newApp` takes one `AppDeps`; `newVersionedApp` is gone; `cmd/vlt/main.go` builds `AppDeps`; behaviour unchanged
- [x] One rig builds every App in the ui tests (55 hand-built sites and nine rigs before; one call to `newApp` after)
- [x] References to App's fields from test files: 254 before, 38 after (32 of them in the rig, 6 in tests of App's wiring), by `grep -oE '\bapp\.(field names)\b' internal/ui/*_test.go`
- [x] `config_test.go` holds the Settings module's tests; the Settings screen's are in `settings_test.go`
- [x] `go test -list . ./internal/ui` lists the same 290 tests before and after
- [x] Mutation-checked on the migrated tests: busy progress, the folder check's busy screen, Stay unlocked, dropping the main view, clearing the passphrase entry, Report a Bug, a vault gone before unlock, a stale folder check, committing the location (three tests), a second move, a stale update result, the screen usable after Cancel, dismissing dialogs on lock, the list load, the server option and clearing the clipboard on lock each fail a test
- [x] DESIGN (`AppDeps`, the rig), README (test list)
- [ ] Independent verification

## Related
- [[Roadmap]]
- [[F52 Main view]]
- [[F51 Vault-wide changes]]
- [[F50 Opening a vault]]
