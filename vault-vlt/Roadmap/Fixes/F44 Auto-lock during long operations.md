---
id: F44
title: "Auto-lock during long operations"
aliases:
  - F44
tags:
  - fix
severity: medium
status: in-progress
phase: 12
area: ui
review_finding: Go skills audit 2026-11-11, item 7
files:
  - internal/store/store.go
  - internal/store/store_internal_test.go
  - internal/ui/app.go
  - internal/ui/session.go
  - internal/ui/session_test.go
  - internal/ui/operations.go
  - internal/ui/operations_test.go
  - internal/ui/runner.go
  - internal/ui/settings.go
  - internal/ui/config_test.go
  - internal/ui/helpers_test.go
  - GLOSSARY.md
  - vault-vlt/Architecture/Security Model.md
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F33
  - F41
created: 2026-11-12
---

# F44: Auto-lock during long operations

## Problem
*Move Vault* (`store.CopyTo`) and *Change Passphrase* run on the session's operations runner, so the auto-lock cancels them like any other work. A move to a server with no input for the timeout (30 s at least) was cut off part-way and its result dropped:
- `CopyTo` wrote `vlt.age` and `vlt.json` first, so the destination looked like a whole vault with only some secrets. Moving there again failed with "vault already exists"; switching there opened a vault with secrets missing.
- A passphrase change locked out during its write of `vlt.age` (seconds on a server) could take effect without the user being told.

## Fix
- **`CopyTo` copies `vlt.age` last** (after `vlt.json` and every secret, checking its context before each file). An unfinished copy has no key: `Exists` still reports a vault (nothing is created over it) and `Open` says `ErrKeyMissing`, whose message now suggests moving the vault there again. **Moving again finishes it**: a destination without `vlt.age` or `vlt.migrating.age` whose secret files (and `vlt.json`) are byte for byte this vault's is completed; anything else is still refused (`ErrVaultExists`), since it may be another vault whose key a backup could restore.
- **The session can pause the auto-lock.** `session.Pause()` returns a resume function; while any pause is held there is no warning and the vault locks only after `pausedLockAfter` (an hour, or the timeout if longer) without input, and once every pause is resumed the countdown restarts in full. A second resume, or one after the session ended, does nothing (`End` clears the pauses; each resume belongs to its session's generation). `End` (*Lock*, quitting) still ends a paused session at once, zeroing the key and cancelling the work.
- **Operations take the pause.** An `operation` marked `keepUnlocked` pauses the auto-lock through the session for as long as it runs, and resumes it when its outcome arrives or is dropped. *Move Vault* and *Change Passphrase* are marked. *Switch Vault* is instant (it locks anyway); import and export write one file each and leave nothing half-done if cut off (a dropped export is wiped), so they are not.
- **A passphrase change is reported even after a lock.** `operation.afterEnd` receives the outcome (nil for success) when the session ended before it arrived, instead of it being dropped (`backgroundOwned`'s drop hook now gets the error too). *Change Passphrase* uses it: over the unlock screen it says the new passphrase applies, that the lock came first and nothing changed (cancelled, or the key already zeroed), or the error. The dialog is queued after `Lock`'s own `fyne.Do`, which dismisses dialogs.
- **Decision: the upper bound.** A pause that never ends would keep the vault unlocked for good. A server that stops answering already fails the operation within about 45 s (ssh ServerAlive), but a hung local network folder may never return, so a paused session still locks after an hour without input. An hour is above the longest timeout (30 min) and far above any real copy; input during the pause restarts that hour, as it restarts the normal countdown.

## Acceptance criteria
- [x] `TestStore_CopyTo_AnInterruptedCopyHasNoKeyFile`, `…FinishesAnInterruptedCopyOfThisVault` and `…RefusesAKeylessVaultItDidNotCopy` fail without their fixes
- [x] The `TestSession_Pause…`/`…Paused…` tests, `TestOperations_Start_PausesTheAutoLockWhileAnOperationThatKeepsUnlockedRuns` and `TestSettings_TheAutoLockWaitsForAMoveOrPassphraseChange` fail without the pause; `TestSettings_LockingDuringAMoveStopsIt` shows *Lock* still works
- [x] `TestOperations_Start_ReportsAnOutcomeDroppedAtTheLockToAfterEnd`, `TestSettings_APassphraseChangeCutOffByALockIsReported` and `…FinishedAfterTheLockIsReported` fail without the report
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG, GLOSSARY, Security Model

## Related
- [[Roadmap]]
- [[F33 Vault session module]]
- [[F41 Operations]]
- [[Security Model]]
