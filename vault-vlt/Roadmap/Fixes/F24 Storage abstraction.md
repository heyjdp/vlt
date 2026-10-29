---
id: F24
title: "Storage abstraction"
aliases:
  - F24
tags:
  - fix
severity: medium
status: done
phase: 7
area: store
review_finding: User request (2026-10-29)
files:
  - internal/store/fs.go
  - internal/store/store.go
  - internal/store/vault.go
  - internal/store/migrate.go
depends_on: []
created: 2026-10-29
---

# F24: Storage abstraction

## Problem
The store called `os` and `filepath` directly, about 50 times, so a vault could only be a local directory.

## Fix
- **`vaultFS`** (`fs.go`): the store reaches storage only through this interface. Its names are vault-relative slash paths (`work/github.age`).
  - Operations: `ReadFile`, `ReadDir` (sorted by name), `Stat`, `Remove`, `Rename`, `MkdirAll`, `WriteFileAtomic`, `WriteFileExclusive`, `SyncDir`, `Local`, `Close`.
  - Promises: atomic writes, exclusive creates, modes 0600/0700, `fs.ErrNotExist` and `fs.ErrExist`.
- **`localFS`** implements it with the existing `atomic.go`, so local behaviour doesn't change.
- **Signatures:** `Exists`, `Create` and `Open` take a context and a location string.
- **`CopyTo`** copies between any two locations.
- **`List`** reads up to 8 files in parallel and keeps directory order.

## Acceptance criteria
- [x] Every existing store test passes unchanged in behaviour (paths in `SkippedFiles` are now slash-separated)
- [x] `TestVaultFS_Contract` runs the same checks against both backends
- [x] `go test -race ./...` passes

## Outcome
Verification found that the remote `ReadDir` returned the server's raw order, so a server vault's list came up scrambled; the contract test hid it by sorting. `ReadDir` is now sorted by name on both backends and the contract checks the order (`TestRemoteStore_ListKeepsNameOrderWithManyFiles` adds 30 secrets). Directory walks skip dot-folders (`isGroupDir`). `Rename` joined the interface for the lock's release.

## Related
- [[Roadmap]]
- [[ADR-002 Vaults on a server over the system ssh]]
- [[F25 SFTP backend and server locations]]
