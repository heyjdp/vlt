---
id: F51
title: "Vault-wide changes"
aliases:
  - F51
tags:
  - fix
severity: low
status: done
phase: 13
area: ui
review_finding: "Architecture review 2026-11-12 (third pass), candidate 3"
files:
  - internal/store/vault.go
  - internal/store/vault_test.go
  - internal/store/fs.go
  - internal/store/atomic.go
  - internal/store/sshfs.go
  - internal/store/sshfs_internal_test.go
  - internal/store/lock.go
  - internal/ui/operations.go
  - internal/ui/operations_test.go
  - internal/ui/settings.go
  - internal/ui/config_test.go
  - internal/ui/app.go
  - DESIGN.md
  - README.md
  - GLOSSARY.md
  - CHANGELOG.md
depends_on:
  - F50
  - F44
  - F45
created: 2026-11-12
---

# F51: Vault-wide changes

## Problem
- **Only the store knew whether `vlt.age` was written, but the UI decided.** `settings.go` guessed from error identity: `context.Canceled` or `crypto.ErrIdentityZeroed` (a crypto sentinel leaking through the store into the UI) meant "not changed", `isServerError` meant "may have been saved". The guess was wrong both ways: a connection lost while reading `vlt.age`, or a server that couldn't be reached, was reported as possibly saved; a local rename whose folder sync then failed was reported as failed although the new key was in place.
- **Three operation fields described one idea.** `kind: opVault`, `keepUnlocked` and `afterEnd` (plus `now` for the switch, and `relockThenInform` in Settings) together meant "a change to the whole vault": one at a time, the auto-lock paused, relock on success, the outcome reported after a lock. Each call site had to pick the right combination.

## Fix
- **`store.ChangePassphrase` reports its outcome.** nil once `vlt.age` is replaced; otherwise a `*store.ChangeError{Change, Err}` with `Change` `Unchanged` or `MaybeChanged`, read with `store.ChangeOf(err)`. The rule lives next to the write: `vaultFS.WriteFileAtomic` marks a failure after which the file may hold the new contents (`mayHaveWritten`/`writeUncertain`): locally, a rename whose directory sync failed; over SFTP, a connection lost once the `posix-rename` was sent. Everything before that (wrong passphrase, cancelled context, a refused write, a connection lost or a server unreachable before the rename) is `Unchanged`. A vault key zeroed by a lock is `store.ErrClosed`, so `crypto` no longer reaches the UI.
- **`operations.changeVault(&vaultChange{…}, work)`.** One form: refused while another vault change runs; the auto-lock paused until the outcome arrives or is dropped; on success `changed` runs, the vault relocks and `changed`'s notice is shown over the unlock screen; an outcome after the lock goes to `outlived`, queued after the lock's dismissal of dialogs. `changeVaultNow(f)` is the switch: refused while a vault change runs, then relocks. `operations` is given the session's `relock` (`App.Lock`), so Settings lost `onRelock`. `start`/`startOwned` and `changeVault` share `launch`.
- **Settings maps the outcome to words.** `passphraseChangeFailure` and `passphraseChangeOutlived` switch on `store.ChangeOf`; the wording and dialogs are those of [[F44 Auto-lock during long operations]] and [[F45 Lock without freezing]].
- **Decision: an error type, not a second return value.** `nil` already means "changed"; a `*ChangeError` keeps `ChangePassphrase` an `error`-returning call (so `changeVault`'s work stays `func(ctx) error`) and keeps `errors.Is` on the cause working. `ChangeOf` counts any other error as `MaybeChanged`: only an explicit `Unchanged` ever claims the old passphrase still works.
- **Decision: why it stopped is still read from the error.** After a lock, "Passphrase Not Changed" needs `Unchanged` *and* a stop by the lock (`context.Canceled` or `store.ErrClosed`); a wrong passphrase or a full disk that coincided with the lock is shown as its error. A `MaybeChanged` outcome is never reported as "not changed", whatever stopped it.
- **Decision: the "server" clause only for a server.** The possibly-saved text keeps its wording; the clause about the server's lost reply is added only when the cause is a server error (`isServerError`), since a local sync failure is now `MaybeChanged` too.
- **Decision: `CopyTo` gets no outcome.** A copy never changes the vault it copies from, and the destination already has its own answer (an unfinished copy is finished by moving again, or refused as stale). A move the vault locked during still says nothing afterwards.
- **Decision: Settings depends on `vaultChanger`.** A two-method interface at the point of use (`ChangePassphrase`, `CopyTo`), so UI tests drive each outcome with a fake instead of timing a lock in the middle of a write.

## Outcome
Two commits on `refactor/deepen-open-and-lock`, each with build, vet, `go test -race` and golangci-lint green:
- `feat(store)`: the `Change` outcome, the write marker on both backends, `ErrClosed` for a zeroed key, Settings worded from the outcome.
- `refactor(ui)`: `changeVault`/`changeVaultNow`; `keepUnlocked`, `afterEnd`, `now` and `relockThenInform` deleted.

For the user: the same dialogs and wording, except that a passphrase change that lost its server connection before the new key was sent now says the passphrase was not changed (it used to say it may have been saved), and a local change whose folder could not be synced says it may have been saved (it used to say it failed). CHANGELOG's entry for the lost-reply fix says so.

## Acceptance criteria
- [x] Store tests for each outcome: changed; unchanged for a wrong passphrase, a refused write, a cancelled context and a zeroed key; maybe changed for a local rename whose folder sync fails (a folder that can be written but not read); over the in-process SFTP server, maybe changed when the rename reached the server and its reply was cut off (and the new passphrase opens the vault), unchanged when the connection dropped before the rename was sent
- [x] Mutation-checked: dropping either backend's marker, marking every remote failure, dropping the zeroed-key mapping, ignoring the marker in `ChangePassphrase`, or making `ChangeOf` trust any `ChangeError` (or default to unchanged) each fails a test
- [x] UI tests drive each outcome through the form with a fake store, unlocked and after a lock; mutation-checked: no pause, no `outlived`, no relock, `changed` after the relock, `changeVaultNow` without the claim or the relock, Settings without `outlived`, and the "stopped by the lock" check without `ErrClosed` or without `Unchanged` each fail a test
- [x] `internal/ui` no longer imports `internal/crypto` in `settings.go`; no caller sets `kind: opVault`
- [x] DESIGN, README test list, GLOSSARY (Vault change), CHANGELOG

## Related
- [[Roadmap]]
- [[F44 Auto-lock during long operations]]
- [[F45 Lock without freezing]]
- [[F50 Opening a vault]]
- [[F49 Store owns its lock file]]
