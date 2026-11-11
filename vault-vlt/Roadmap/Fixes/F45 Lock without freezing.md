---
id: F45
title: "Lock without freezing"
aliases:
  - F45
tags:
  - fix
severity: low
status: in-progress
phase: 12
area: ui
review_finding: Go skills audit 2026-11-11, item 8; F44 verification
files:
  - internal/crypto/identity.go
  - internal/crypto/identity_internal_test.go
  - internal/store/vault.go
  - internal/store/vault_test.go
  - internal/store/lock_test.go
  - internal/ui/session.go
  - internal/ui/session_test.go
  - internal/ui/app.go
  - vault-vlt/Architecture/Security Model.md
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F33
  - F44
created: 2026-11-12
---

# F45: Lock without freezing

## Problem
- **Locking did file I/O on the main goroutine, under the session's mutex.** `session.End` (*Lock*, the relock after Settings, *Switch Vault*) called `store.Close` with `ss.mu` held; for a local vault `Close` releases `vlt.lock` before returning (rename aside, directory fsync, read, remove). A "local" vault on a network folder (SMB, NFS, FUSE) that stops answering froze the window, and the auto-lock did the same on the timer's goroutine while every keystroke's `Activity()` waited on the mutex. This broke the rule in `runner.go` that every blocking store call runs in the background. The app also closed stores on the main goroutine when an unlock's screen had been replaced and when the "Vault Already Open" question was answered.
- **A lock during *Change Passphrase* waited for scrypt.** `WrapIdentity` held the identity's mutex for the whole scrypt derivation (about a second at 2^18, up to about four at the 2^20 cap). *Lock* calls `Zero()` on the main goroutine while holding the session's mutex, so it blocked on that derivation: the window froze, every keystroke's `Activity()` waited on the session mutex, and the key stayed in memory until scrypt finished.

## Fix
- **The session lets go of a store in two steps.** `sessionStore` gains `ZeroKey()` (`Store.ZeroKey` zeroes the key and touches no storage). Every store the session lets go of (`End`, the auto-lock, `Begin` over an open session, `Hold` over a waiting store, the new `Discard`, `Close`) has its key zeroed and is detached under `ss.mu`, at once; its `Close` then runs through `spawn` (the app's runner: a new goroutine in production, inline in tests over local stores), outside the mutex and off the main goroutine. `session.closing` keeps the `Closed` channels of every store let go of (pruned of finished ones), and `session.Close` (quit) still waits up to 3 s for all of them.
- **The app's own closes go through the session.** The four places in `app.go` that closed a store on the main goroutine now call `session.Discard`, so quitting also waits for them.
- **A late release spares the next session's lock.** A quick lock and unlock can take the lock again while the old release runs. `releaseLock` renames the lock aside and checks its token, putting back a lock that is not its own, so the new session's lock survives whether it was written after the old lock was renamed aside or before the release began (now pinned by a test). In the narrow case where the new session also locks during the old release's rename-aside window, the old release puts back a lock nobody holds; it is this process's own, so the next unlock on this computer takes it over silently, as after a crash.
- **Decision: keep `Store.Close` synchronous for local vaults.** Making `Close` always background its cleanup would also have fixed it, but would leave callers and tests unable to know when a local release is done without waiting on `Closed`, and would let a test's temporary folder be removed while a release still runs. The session owns the background step instead, with a seam (`spawn`) that tests control.
- **The derivation runs outside the identity's mutex.** `wrapTo` (split out of `WrapIdentity`, taking any age recipient) calls `age.Encrypt`, which runs the recipient's key derivation before the key is needed, without the mutex. Only writing the encoded key into the encrypted stream holds it. If the key was zeroed during the derivation, the wrap fails with `ErrIdentityZeroed` and returns nothing, which *Change Passphrase* already reports after the lock as "Passphrase Not Changed".
- **Decision: no copy of the key.** Copying the key out under the mutex and deriving afterwards would leave a second copy alive for the seconds of the derivation, which `Zero()` would then have to track. Deriving first needs no copy at all.

## Acceptance criteria
- [x] `TestWrapTo_ZeroDuringTheKeyDerivationIsPromptAndTheWrapFails` fails without the fix (Zero waits), and without the zeroed check after the derivation (the wrap succeeds)
- [x] `TestSession_EndingZeroesTheKeyAtOnceAndReleasesTheLockInTheBackground` fails when the store is closed inline (ending blocks), when the key is left for `Close` to zero, and when quitting does not wait for released stores
- [x] `TestStore_ZeroKey_ZeroesTheKeyButLeavesTheLockForClose` fails when `ZeroKey` does nothing
- [x] `TestLock_AReleaseStillRunningLeavesTheNextSessionsLock` fails when the release ignores the token
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG, Security Model

## Related
- [[Roadmap]]
- [[F33 Vault session module]]
- [[F44 Auto-lock during long operations]]
- [[F43 Scrypt work factor cap]]
- [[Security Model]]
