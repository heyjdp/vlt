---
id: F04
title: Confirm passphrase on vault creation
aliases:
  - F04
tags:
  - fix
  - ui
  - store
severity: critical
status: done
phase: 1
area: ui
review_finding: "#4"
files:
  - internal/ui/app.go
  - internal/ui/create.go
  - internal/ui/create_test.go
  - internal/ui/unlock.go
  - internal/ui/settings.go
  - internal/store/vault.go
  - internal/store/store.go
depends_on:
  - "[[F01 Scrypt-wrapped identity]]"
created: 2026-10-27
---

# F04: Confirm passphrase on vault creation

## Problem
- `App.onUnlock` (`internal/ui/app.go:92`) creates a new vault with **whatever was typed** when there's no sentinel. A typo becomes the permanent passphrase.
- "Switch Vault" to an empty or wrong directory does the same.
- ~~A directory with `.age` files but no `vlt.age` is adopted by any passphrase~~ (fixed in [[F01 Scrypt-wrapped identity]]: `Exists` counts secret files, and `Open` migrates only if the passphrase decrypts one)
- ~~`Store.CopyTo` overwrites `vlt.age` in a destination that already holds a different vault~~ (fixed in F01: `CopyTo` returns `ErrVaultExists`)

## Fix
- Decide the screen before showing it: if `vlt.age` is missing, show a **Create vault** screen with *passphrase* + *confirm* fields and strength guidance ([[Passphrase Strength]]: recommend 5+ Diceware words).
- ~~If `vlt.age` is missing but `.age` files exist, refuse and report a damaged vault~~ Superseded by F01: such a directory counts as a format-1 vault. The unlock screen is shown, and `Open` migrates only if the passphrase decrypts a secret, so an arbitrary passphrase can never adopt it.
- ~~`CopyTo` refuses a destination that already contains `vlt.age` or any `.age` files~~ (done in F01: `CopyTo` checks `Exists` and returns `ErrVaultExists`)

## As shipped
- `App.showUnlockScreen` asks `store.Exists` and shows one of three screens:
  - the unlock screen (`Open` only)
  - the create-vault screen (`create.go`), which shows the target path, takes the passphrase twice (at least 12 characters excluding surrounding spaces), and clears both entries on submit
  - an error screen with Retry, if the directory cannot be inspected
- `store.Exists` returns `(bool, error)`, so an unreadable directory is never mistaken for "no vault". `Create` writes `vlt.age` exclusively.
- Switch Vault to an empty or missing folder relocks into the create screen. The dialog text says so.
- If a vault disappears after the screen is chosen, the app offers to create one. Leftover files with nothing to unlock show an error.

## Acceptance criteria
- [x] A new vault needs two matching entries: `TestValidateNewPassphrase`, `TestCreateScreen_MismatchShowsErrorAndDoesNotCreate`, `TestApp_MissingVaultShowsCreateScreen`
- [x] A directory with secrets but no `vlt.age` is not adopted. This was superseded by [[F01 Scrypt-wrapped identity]]: such a directory migrates only if the passphrase decrypts a secret (`TestStore_Open_MigratesLegacyVaultWithoutSentinel`, `_LegacyWithoutSentinelRejectsWrongPassphrase`). An unreadable one shows an error (`TestApp_UnreadableDirectoryShowsNeitherScreen`, `TestStore_Exists_ReportsUnreadableGroupInsteadOfNoVault`).
- [x] Moving into an existing vault fails without modifying it: `TestStore_CopyTo_RefusesExistingVault`, `TestStore_Create_RefusesDirectoryWithOnlySecretFiles`
- [x] `fyne/test` coverage: `TestCreateScreen_*`, plus `TestApp_ExistingVaultShowsUnlockScreen`, `_SwitchToEmptyDirectoryShowsCreateScreen`, `_VaultRemovedAfterScreenChosenOffersCreate`, `_LeftoverMarkerWithoutVaultShowsError`

## Outcome
Shipped after code, docs, and tooling verification.
- Verification caught a create-screen layout bug (the button could fall outside the screen) and `Exists` treating read errors as "no vault".
- It also caught a flaky `-race` failure in the first app-level test. `fyne/test` runs `fyne.Do` inline, so the main view's background refresh raced with `Lock`. App tests now stop before the main view. The underlying issue is tracked in [[F10 Keep vault IO off the UI goroutine]].

## Related
- [[Unlock Flow]]
