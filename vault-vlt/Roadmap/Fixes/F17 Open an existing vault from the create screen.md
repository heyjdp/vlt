---
id: F17
title: "Open an existing vault from the create screen"
aliases:
  - F17
tags:
  - fix
severity: high
status: done
phase: 6
area: ui
review_finding: Reinstall verification
files:
  - internal/ui/create.go
  - internal/ui/app.go
depends_on: []
created: 2026-10-28
---

# F17: Open an existing vault from the create screen

## Problem
When no vault exists at the configured location, the app shows only "Create a new vault" (`internal/ui/create.go`). Settings, where the location can be changed, is reachable only after unlocking.

## Impact
If the preferences file is lost (cleaner app, new Mac, restored home folder) and the vault was in a custom folder, the user sees an empty create screen and may believe their secrets are gone. Recovering needs `VLT_VAULT_DIR` or a throwaway vault.

## Fix
Add **Open Existing Vault…** to the create screen: pick a folder, check it holds a vault (`store.Exists`), switch to it and show the unlock screen. The location is saved once the vault unlocks, as with a switch in Settings. A folder without a vault is refused with a clear message. Not offered when `VLT_VAULT_DIR` sets the location.

## Acceptance criteria
- [x] Picking a folder with a vault shows the unlock screen for it and saves the location once it is unlocked: `TestApp_OpenExistingVaultUnlocksAndRemembersIt`
- [x] Picking a folder without a vault shows an error and stays on the create screen: `TestApp_OpenFolderWithoutVaultIsRefused`, `TestApp_OpenUnreadableFolderShowsTheError`
- [x] When the location comes from `VLT_VAULT_DIR`, the button isn't offered (as in Settings): `TestApp_OpenExistingIsNotOfferedWhenTheLocationIsFromTheEnvironment`
- [x] Busy and stale results: `TestCreateScreen_BusyDisablesOpen`, `TestApp_OpenCheckDisablesTheScreenUntilItFinishes`, `TestApp_StaleOpenResultIsDropped`, `TestCreateScreen_OpenButtonShowsAFolderDialog`
- [x] `go test -race ./...` passes

## Outcome
> [!danger] Found during verification: opening the folder above a vault broke the vault
> `store.Exists(parent)` counted the inner vault's `vlt.age` as a secret in a group called `vault`. Unlocking the parent took the format-1 path, proved the passphrase against that file, and "migrated" it, re-encrypting the inner vault's key to a new key. The real vault then rejected its own passphrase. Settings' *Switch Vault* could already do this; the folder picker made it easy.
>
> Fix, in two layers:
> - `secretFiles` skips subdirectories holding `vlt.json` or `vlt.migrating.age`, so a format-2 vault one level down never counts as secrets of its parent (`TestStore_FolderAboveAVaultIsNotAVault`, `TestStore_Exists_TellsNestedVaultsFromGroups`). The create screen points to the inner folder instead (`TestApp_OpenFolderAboveAVaultPointsToIt`).
> - Content, not layout, decides the rest. `Open` accepts the passphrase only if a probed file decrypts to a secret's JSON envelope, and migration never re-encrypts a file that decrypts to anything else (`TestOpen_FolderAboveAFormat1VaultIsNotMigrated`, `TestMigration_LeavesANestedVaultsKeyAlone`).
>
> A first version also treated "`vlt.age` beside a subfolder" as a nested vault. Re-verification showed it could silently skip a real group (a secret named "vlt" plus a NAS-created folder such as `@eaDir`) during migration, stranding its secrets under the old passphrase, so it was dropped (`TestMigration_MigratesAGroupHoldingASecretNamedVlt`).

Also from verification:
- The folder check now disables Create and Open while it runs, so a vault can't be created mid-check.
- A vault whose `vlt.age` is missing shows a plain explanation instead of `store.Open: … vault key file vlt.age is missing`.
- Docs: the lock state diagram, the unlock flow and DESIGN.md describe the new path.

## Related
- [[Roadmap]]
