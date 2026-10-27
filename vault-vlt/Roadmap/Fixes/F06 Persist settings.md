---
id: F06
title: Persist settings
aliases:
  - F06
tags:
  - fix
  - ui
severity: high
status: done
phase: 2
area: ui
review_finding: "#6"
files:
  - cmd/vlt/main.go
  - internal/ui/config.go
  - internal/ui/config_test.go
  - internal/ui/settings.go
  - internal/ui/app.go
depends_on: []
created: 2026-10-27
---

# F06: Persist settings

## Problem
`Config` is built from environment variables in `main.go` and only changed in memory by `SettingsScreen`:
- After **Move Vault**, the next launch opens the *old* directory.
- Auto-lock and clipboard TTL changes are lost on restart.
- ~~`DetailView` copies `clipboardTTL` when it's constructed, so a TTL change doesn't apply until the next unlock~~ (fixed in [[F08 Correct clipboard clearing]]: the TTL is read at each copy)

## Fix
- Store non-secret settings with `fyne.App.Preferences()` (keys `vaultDir`, `lockTimeoutMs`, `clipboardTTLs`), or in a small JSON file in the OS config dir.
- Precedence: env var → saved preference → default.
- Pass a `*Config` (or getter) to `DetailView` instead of a copied int.
- Validate ranges: lock timeout from 30 s to 30 min or `0` = off; TTL from 5 s to 120 s.

## As shipped
- Settings are stored in Fyne's per-app preferences.
- `LoadConfig` (`config.go`) resolves env var → saved preference → default and clamps values. `main.go` creates the Fyne app before loading config.
- Each setting is saved on its own, only when the user changes it:
  - Sliders save when a drag ends; the "Auto-lock" checkbox saves when ticked or unticked.
  - A new vault location is saved by `App` after the next successful unlock or create there.
- Settings provided by environment variables are flagged in `Config.FromEnv`. They're disabled and labelled in Settings and never saved.
- An "Auto-lock" checkbox turns auto-lock off and on. Re-enabling it arms the countdown immediately.
- The clipboard TTL applying without relocking was done in [[F08 Correct clipboard clearing]].

## Acceptance criteria
- [x] Moving the vault and relaunching opens the new location: `TestSettings_MovedVaultIsRememberedAfterUnlock`, `_SwitchedVaultIsSavedOnlyOnceOpened`, `TestLoadConfig_SavedSettingsSurviveRestart`. Verification also probed a real process restart.
- [x] A TTL change applies to the next copy without relocking (F08: `TestApp_ClipboardTTLChangeAppliesToNextCopy`)
- [x] Out-of-range env values are clamped and logged: `TestLoadConfig_ClampsOutOfRangeValuesAndLogs`

## Outcome
Verification found that saving the whole config on every change wrote environment-provided values (including the vault path) into saved preferences. It also found that a mistyped Switch Vault path would stick across restarts. Both are fixed by per-setting saves and a deferred vault-path save.

It also added the auto-lock on/off control and tests for the lock slider, Move Vault and the app wiring (`openSettings`).

Known limit: Fyne batches preference writes within 100 ms. A crash or kill in that window can lose the latest change; a normal quit flushes it.

> [!warning] For [[F13 Project hygiene]]
> Saved settings live under the app ID `io.yourorg.vlt`. Renaming the ID orphans them unless they're migrated.

## Related
- [[F07 Inactivity-based auto-lock]] · [[F08 Correct clipboard clearing]]
