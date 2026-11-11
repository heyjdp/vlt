---
id: F37
title: "Lock read errors"
aliases:
  - F37
tags:
  - fix
severity: medium
status: done
phase: 10
area: store
review_finding: Go skills audit 2026-11-11, item 4
files:
  - internal/store/lock.go
  - internal/store/lock_test.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on: []
created: 2026-11-11
---

# F37: Lock read errors

## Problem
When `vlt.lock` already exists, `tryLock` reads it to learn who holds it. Every read error other than "not found" became `&InUseError{}`: a lock file that doesn't parse or that this user may not read (rightly: someone holds it), but also a dropped connection (`ErrConnectionLost`) or any I/O error. The user saw "Vault Already Open … Open Anyway" when the real problem was the server or the disk, and *Open Anyway*'s forced write then met the same fault.

## Fix
- **`readLock`** marks a parse failure with `errBadLock`.
- **`tryLock`**: a lock that won't parse (`errBadLock`) or that this user may not read (`fs.ErrPermission`, most likely another user's lock) is still in use, holder unknown. A dropped connection is retried within the existing attempts (the next call reconnects), and if it persists it is returned. Any other read error is returned as `store.AcquireLock: %w`, so `errors.Is` finds the cause. The UI already logs such errors and opens the vault, as it does when the lock can't be written: the lock only warns.
- **Tests**: a dropped connection that persists and an I/O error while reading a planted lock give the read error, not `ErrVaultInUse`; a connection dropped once still finds the planted lock; a permission error gives an `*InUseError` with no holder; the existing test (renamed `TestLock_ADamagedLockIsInUse`) keeps a damaged lock in use.

## Outcome
Verification confirmed the classification: pkg/sftp maps a denied read to `os.ErrPermission`, a local `chmod 000` lock matches `fs.ErrPermission`, an empty lock file is a parse failure (a holder caught mid-write), and `releaseLock` parses the lock itself, so it is unchanged. Five mutations were all caught. It suggested:
- **Retrying a dropped connection** rather than opening with no lock: done, with a test.
- **"read vlt.lock" twice in the message**: both file systems already name the file; the extra prefix is gone.
- **Docs**: the vault now opens without recording that it is open when the lock can't be read, which the CHANGELOG and the Security Model now say; [[F26 Vault lock file]] points here.

Still open: after a connection that stays down, the list's load error is only logged, so the vault looks empty. That is [[F38 Secret list errors]].

## Acceptance criteria
- [x] `TestLock_ALockThatCannotBeReadIsAnErrorNotInUse` and the retry test fail before the fix; the permission and damaged-lock tests guard the cases that stay in use
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG, Security Model

## Related
- [[Roadmap]]
- [[F26 Vault lock file]]
- [[Security Model]]
