---
id: F42
title: "Settings module"
aliases:
  - F42
tags:
  - fix
severity: low
status: done
phase: 11
area: ui
review_finding: "Architecture review 2026-11-11 (second pass), candidate D"
files:
  - internal/ui/config.go
  - internal/ui/config_test.go
  - internal/ui/app.go
  - internal/ui/settings.go
  - internal/ui/updates.go
  - internal/ui/about.go
  - internal/ui/helpers_test.go
  - internal/ui/remote_test.go
  - internal/ui/*_test.go
  - cmd/vlt/main.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F35
  - F41
created: 2026-11-11
---

# F42: Settings module

## Problem
- **The vault location was a string handled by hand in five files.** `Config.VaultDir` was assigned at five sites (`app.go`, `settings.go`), parsed with `store.ParseLocation` at six (`about.go`, `app.go`, `settings.go`), and saved at one, an `if` in `App.unlocked`. Settings compared locations with `sameLocation` ([[F35 Location round trip]]), but the error screen in `app.go` compared the current location with the default using `!=`.
- **The environment rule lived twice.** "A setting from an environment variable is never saved" was enforced by disabling widgets in `settings.go` and by `if !FromEnv.VaultDir` checks in `app.go` (the save, the create screen's offers, the error screen's offer).
- **Saving was shallow.** Three one-line `Save*` pass-throughs, a `saveSetting` switch on preference-key strings wired into the Settings screen, and `updates.go` reading and writing the `updateNotified` preference itself.
- **A location from `VLT_VAULT_DIR`, or saved by an older version, was shown as given** (`ssh://me@nas/srv/vault`), while one chosen in Settings showed in the form vlt writes (`me@nas:/srv/vault`).

## Fix
- **`fix(ui)` first, so the rest is structural**: `LoadConfig` kept a location that parses in its `Location.String` form (one that doesn't was kept as given, so opening it still shows why), and the error screen compared with `sameLocation`. CHANGELOG *Fixed* entry.
- **`Settings` (`config.go`)**, one module behind one interface:
  - `LoadSettings(prefs, getenv, defaultVault, log)` resolves env → saved → default and clamps, as `LoadConfig` did. `MigratePreferences` is unchanged.
  - The location is a `store.Location`, compared by value. `Vault()` is the **proposed** location (shown and opened); `ProposeVault(loc)` changes it (switch, move, *Open Existing Vault…*, a server location, *Use the Vault on This Computer*) and is refused when `VLT_VAULT_DIR` fixes the location. `CommitVault()`, called by `App` after every successful unlock or create, saves it unless `VLT_VAULT_DIR` set it, and records it as the **committed** location; the default location is committed too (it depends on `XDG_DATA_HOME`).
  - `UnusableVault()` keeps a location from the environment or the saved settings that doesn't parse as written, with the reason, so the error screen is unchanged. `DefaultVault()` returns the local default only when it may be offered (there is one, it isn't the current location, the location isn't fixed).
  - `SetLockTimeout`, `SetClipboardTTL`, `SetCheckUpdates` clamp, refuse a setting an environment variable fixes (with a log warning), and save. `SetByEnv(setting)` names the variable; Settings and the create screen ask it before disabling a control. `AnnouncedRelease`/`SetAnnouncedRelease` replace `updates.go`'s preference access.
- **Callers migrated**: `app.go` (screens, unlock/create, server and folder choices, error screen, clipboard TTL and auto-lock timeout as method values, `lockTimeout` removed), `settings.go` (Move/Switch take a `store.Location`; sliders apply and save when a change ends), `updates.go`, `about.go`, `cmd/vlt/main.go`. Removed: `Config`, `EnvSettings`, `SaveVaultDir`, `SaveLockTimeout`, `SaveClipboardTTL`, `App.saveSetting`, `SettingsScreen.save`, `sameLocation`.
- **Decisions**:
  - An unparsable location is kept as given rather than ignored with a warning (like an unparsable number): ignoring it would silently open, and then save, another vault. The error screen stays as it was, checked by a characterisation test against the previous commit.
  - Sliders apply their value when a change ends (Fyne ends one on release, tap and key press) instead of on every step; the label still follows. Saving per step would write the preferences file on every step.
  - One structural commit for the module and its callers, rather than two, because a string-location interface in between would have been thrown away.

## Outcome
- Commits: `fix(ui): read the vault location in the form vlt writes`, `refactor(ui): a settings module owns the vault location and every setting`.
- Tests: the module on the test app's in-memory preferences, without a vault: precedence, clamping on load and in every setter, settings fixed by the environment neither changed nor saved, a proposed location saved only once committed, the default committed too, comparison by value of differently written locations, an unparsable location kept as given and replaced by a proposal, a round trip of every setting through a restart. App tests: the error screen for an unparsable saved location (exact text, the default offered, saved once created), and the default not offered when it is the current location written another way. App tests build settings with `settingsAt`, whose zero values match the old `Config{VaultDir: dir}` literals (auto-lock off), so their timers are unchanged.
- Mutation checks: ignoring the environment in `CommitVault` or in the setters, saving on `ProposeVault`, dropping either setter's clamp, offering the default when it is current, ignoring `VLT_VAULT_DIR`, not saving the update choice or the announced release, dropping an unusable location, not committing after unlock, or showing the parsed instead of the written location each fail a test. Marking an empty location unusable survived as an equivalent mutant (the same "empty" error), so that branch was deleted.

## Acceptance criteria
- [x] The vault location is a `store.Location`, proposed and committed, committed only after a successful unlock or create
- [x] Locations are compared by value everywhere; no string `!=`
- [x] The environment rule lives in one place; the UI asks `SetByEnv` to disable a control
- [x] The `Save*` pass-throughs and the `saveSetting` switch are gone; `updates.go` doesn't touch preferences
- [x] Precedence, clamping and the 0.1.0 migration kept; module tests need no vault
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG

## Related
- [[Roadmap]]
- [[F35 Location round trip]]
- [[F41 Operations]]
- [[F19 Follow XDG_DATA_HOME on Linux]]
- [[F17 Open an existing vault from the create screen]]
