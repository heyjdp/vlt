---
id: F26
title: "Vault lock file"
aliases:
  - F26
tags:
  - fix
severity: low
status: done
phase: 7
area: store
review_finding: User request (2026-10-29)
files:
  - internal/store/lock.go
  - internal/store/process_unix.go
  - internal/store/process_windows.go
  - internal/store/lock_test.go
depends_on:
  - "[[F24 Storage abstraction]]"
created: 2026-10-29
---

# F26: Vault lock file

## Problem
Two copies of vlt with the same vault open can make conflicting edits. Two computers on a server vault make this likely. If both save the same secret, the last save wins.

## Fix
- **Taking the lock:** `Store.AcquireLock` creates `vlt.lock` exclusively. It records host, user, PID, the time and a random token.
- **Releasing it:** `Close` removes the lock only if it still holds that token.
- **Someone else has it:** a lock held elsewhere comes back as `*InUseError` (`errors.Is(err, ErrVaultInUse)`), and `force` takes it over.
- **Stale locks:** a lock from this host whose process is gone, or from this same process, is taken over silently. `processAlive` uses `kill(pid, 0)` on Unix and `FindProcess` on Windows.
- **Advisory only:** the lock holds no secrets, `CopyTo` doesn't copy it, and failing to write it never blocks an unlock.

## Acceptance criteria
- [x] `TestLock_*`: recorded and released, another computer's lock reported and force-taken, a live local process is in use, a crashed one is taken over, an unreadable lock is in use, someone else's lock is never removed, the lock is not part of the vault
- [x] `go test -race ./...` passes

## Outcome
Verification found and fixed:
- **Release was check-then-remove,** so a lock written in between could be deleted. The lock is now renamed aside atomically, then checked, and put back if it's someone else's (`TestLock_ReleaseLeavesNoTempFiles`).
- **Acquire overwrote a lock that disappeared** between the create and the read. It now retries the exclusive create.
- **A remote close's lock release had no CI test.** `TestRemoteStore_CloseReleasesTheLock` covers it now, and `TestLock_ThisProcessesOwnLeftoverLockIsTakenOver` covers the same-process shortcut.

Accepted:
- **Hosts are told apart by name only.** Two computers with the same host name count as one. This is documented.
- **A Windows quirk.** Another user's elevated process looks gone, because `FindProcess` gets access denied.

## Related
- [[Roadmap]]
- [[ADR-002 Vaults on a server over the system ssh]]
- [[F27 Server vaults in the UI]]
