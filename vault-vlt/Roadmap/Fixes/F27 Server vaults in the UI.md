---
id: F27
title: "Server vaults in the UI"
aliases:
  - F27
tags:
  - fix
severity: medium
status: done
phase: 7
area: ui
review_finding: User request (2026-10-29)
files:
  - internal/ui/app.go
  - internal/ui/create.go
  - internal/ui/unlock.go
  - internal/ui/settings.go
  - internal/ui/config.go
  - internal/ui/style.go
  - internal/ui/remote_test.go
  - cmd/vlt/main.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - "[[F25 SFTP backend and server locations]]"
  - "[[F26 Vault lock file]]"
created: 2026-10-29
---

# F27: Server vaults in the UI

## Problem
The UI only knew local folders.

## Fix
- **Create screen:** *Use a Vault on a Server…* opens a dialog that validates the location and switches to it. That shows its unlock screen, or the create screen for that location; nothing is created until a passphrase is confirmed.
- **Settings:** Move and Switch accept a server location, validated and saved in canonical form.
- **Unlocking and creating:** both take the lock. A vault open elsewhere shows who has it and since when, then offers *Open Anyway* (take over) or *Cancel* (close, leave their lock).
- **Server errors:**
  - An unreachable server shows *Cannot reach the vault's server* with ssh's message and *Retry*.
  - Unless `VLT_VAULT_DIR` fixes the location, it also offers *Use the Vault on This Computer* (`Config.DefaultVaultDir`).
- **Naming the vault:** the unlock screen names it (a local path in the home folder as `~/…`); the status bar names a remote vault's server.
- **Quitting** closes the open or waiting vault and waits up to 3 seconds for a remote one to release its lock.

## Acceptance criteria
- [x] `TestApp_UnlockTakesTheLockAndLockReleasesIt`, `TestApp_VaultOpenElsewhereAsksAndOpenAnywayTakesOver`, `TestApp_VaultOpenElsewhereCancelLeavesItLocked`
- [x] `TestApp_UnreachableServerOffersRetryAndTheLocalVault`, `TestApp_UnreachableServerFromTheEnvironmentOffersOnlyRetry`, `TestApp_ServerDialogValidatesAndSwitchesTheLocation` (with a fake `ssh` on `PATH`)
- [x] `TestApp_UnlockScreenNamesTheVault`, `TestSettings_RefusesABadLocation`, `TestDisplayLocation`, `TestUserMessage`
- [x] `TestApp_CancelClosesTheWaitingVault`, `TestApp_UnansweredOpenElsewhereDialogLocksAfterTheTimeout`, `TestApp_QuitReleasesTheLock`
- [x] README *Vaults on a Server*, DESIGN, CHANGELOG, [[ADR-002 Vaults on a server over the system ssh]]
- [x] `go test -race ./...` passes

## Outcome
Verification found and fixed:
- **Quitting while unlocked left `vlt.lock` behind.** On a shared server vault, the other computer was then asked every time. Quit now closes the vault and waits briefly for the release.
- **The waiting vault's key had no timeout.** While "Vault Already Open" waited for an answer, the opened vault's key sat in memory with no auto-lock. It is now closed after the auto-lock timeout, and Cancel's close is tested.
- **The docs overclaimed.** They said "only ciphertext", but `vlt.json` and `vlt.lock` are plain text. They said batch mode "never accepts an unknown host key", but a user config can allow it. They did not mention offline guessing against `vlt.age` by whoever holds the server's files. All three are corrected.
- **Error messages showed internal prefixes** (`store.Exists:`). They are now stripped (`userMessage`).
- **Examples used a real address.** The README examples and the dialog placeholder now use `me@nas.example` and `me@server.example`.

## Related
- [[Roadmap]]
- [[F25 SFTP backend and server locations]]
- [[F26 Vault lock file]]
