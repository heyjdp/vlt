---
id: F49
title: "Store owns its lock file"
aliases:
  - F49
tags:
  - fix
severity: low
status: done
phase: 13
area: store
review_finding: "Architecture review 2026-11-12 (third pass), candidate 1"
files:
  - internal/store/lock.go
  - internal/store/lock_test.go
  - internal/store/vault.go
  - internal/store/vault_test.go
  - internal/store/store.go
  - internal/store/fs.go
  - internal/store/sshfs.go
  - internal/store/sshfs_internal_test.go
  - internal/ui/app.go
  - internal/ui/session.go
  - internal/ui/session_test.go
  - internal/ui/remote_test.go
  - internal/ui/errmsg_test.go
  - DESIGN.md
  - README.md
  - vault-vlt/Architecture/Architecture Overview.md
  - vault-vlt/Architecture/Unlock Flow.md
  - vault-vlt/Architecture/Storage Format.md
depends_on:
  - F26
  - F37
  - F45
created: 2026-11-12
---

# F49: Store owns its lock file

## Problem
- **The lock file's lifecycle crossed the seam.** The UI took `vlt.lock` after `store.Open` and `store.Create` (`lockVault` in `app.go`), and the policy "a lock that cannot be written only warns" lived there too, while the store released the lock in `Close`. Understanding the lock meant reading `app.go`, `session.go` and `lock.go` together.
- **A store invariant was kept outside the store.** "A closed Store never leaves a lock behind" needs a forced takeover (*Open Anyway*) and `Close` never to overlap. The session grew `Busy`/`Idle` and a busy map only for that: a store being taken over had its key zeroed when let go, but its `Close` waited for `Idle`.
- **"Close must not block" was decided twice.** `Store.Close` ran a remote vault's cleanup on its own goroutine, and the session also ran every `Close` through `spawn` ([[F45 Lock without freezing]]).

## Fix
- **`Open` and `Create` take the lock before returning.** A lock held elsewhere does not stop them: the store comes back without the lock, and `Store.OpenElsewhere() (holder LockInfo, elsewhere bool)` reports who holds it (zero if unreadable). A lock that cannot be written, or read for a reason that says nothing about who holds it, is logged with the store's logger and the vault opens. The [[F37 Lock read errors]] rules are unchanged: a damaged lock, or one this user may not read, is open elsewhere with the holder unknown, and a dropped connection is retried.
- **Decision: a method, not an error.** Returning the store together with a non-nil `*InUseError` would make `if err != nil { return }` leak an open store with its key in memory. `OpenElsewhere` keeps `Open`'s contract ("a store, or an error") and costs callers that don't care nothing. `AcquireLock`, `InUseError` and `ErrVaultInUse` leave the exported API.
- **Taking over is `Store.TakeOver(ctx)`.** It and `Close` exclude each other inside the store: `Close` refuses later takeovers at once (`ErrClosed`, nothing written) and waits for one already running before releasing the lock it took. A `sync.WaitGroup` counts takeovers; it is added to only under the lock mutex while `closing` is false, and `Close` sets `closing` under that mutex before waiting, so `Add` never races `Wait`.
- **`Busy`, `Idle` and the busy map are deleted.** The session lets go of a store being taken over like any other. `app.go` checks `session.Pending()` before starting a takeover, as `Busy` did, so an answer after the store was let go starts nothing; then it calls `s.TakeOver` in its background work. `lockVault` is deleted; nothing replaces it in callers.
- **Decision: the session alone keeps `Close` off the caller's goroutine.** `Store.Close` now releases the lock and ends the connection before returning, for every location. The session's step was the one that mattered (a local vault on a network folder blocks too), it zeroes the key under its mutex first (F45), and quitting's bounded wait on `Closed()` is unchanged. `vaultFS.Local`, which only chose between the two paths, is deleted.
- **A test-only `withFS` option** wraps a location's storage so internal tests inject faults into `Open` and `Create`.

## Outcome
Four commits on `refactor/deepen-open-and-lock`, each with build, vet, `go test -race`, golangci-lint and `GOOS=windows go vet ./internal/store` green:
- `refactor(store)`: `Open`/`Create` take the lock, `OpenElsewhere`, `TakeOver`; `lockVault` deleted.
- `feat(store)`: `TakeOver` and `Close` exclude each other.
- `refactor(ui)`: `Busy`/`Idle` and the busy map deleted.
- `refactor(store)`: `Close` synchronous for every location; `vaultFS.Local` deleted.

Nothing changes for the user: the same "Vault Already Open" dialog, the same *Open Anyway* and *Cancel*, the same logging. The session's interface is two methods smaller; the lock file now lives wholly in `store`, tested through `Open`, `OpenElsewhere`, `TakeOver` and `Close` on temporary folders. `TestSession_ABusyStoreLetGoIsClosedOnceIdle` and `TestSession_ABusyStoreNotLetGoIsTheCallersOnceTaken` went with the methods they tested; the errmsg case built from an `*InUseError` went too, since that error no longer reaches the UI.

## Acceptance criteria
- [x] `TestLock_OpenAndCreateRecordThisProcessAndCloseReleasesIt` and the open-elsewhere tests fail when `Open` does not take the lock
- [x] `TestLock_ALockThatCannotBeWrittenIsLoggedAndTheVaultOpens` and `TestLock_ALockThatCannotBeReadIsLoggedNotOpenElsewhere` fail when the warning is dropped, and when any failure counts as open elsewhere
- [x] `TestLock_AnotherComputersLockIsReportedAndCanBeTakenOver` fails when `TakeOver` leaves the vault reported open elsewhere
- [x] `TestLock_CloseDuringATakeOverWaitsForItAndLeavesNoLock` fails when `Close` does not wait for the takeover
- [x] `TestLock_TakeOverAfterCloseFailsAndLeavesTheLockAlone` fails when a closed store still takes over
- [x] `TestApp_OpenAnywayAfterTheWaitingStoreWasLetGoDoesNothing` fails without the `Pending` check
- [x] `TestRemoteStore_CloseReleasesTheLockBeforeReturning` fails when a remote `Close` cleans up in the background
- [x] Quitting during a takeover still waits for its release (`TestApp_QuitDuringATakeoverWaitsForTheStoresRelease`), and the session's bounded wait on `Closed()` still holds (`slowStore` and `stuckStore` tests)
- [x] DESIGN, README test list, Architecture notes

## Related
- [[Roadmap]]
- [[F26 Vault lock file]]
- [[F37 Lock read errors]]
- [[F45 Lock without freezing]]
- [[F33 Vault session module]]
