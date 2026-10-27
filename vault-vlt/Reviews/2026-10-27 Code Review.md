---
title: 2026-10-27 Code Review
tags:
  - review
date: 2026-10-27
commit: 4f3d6de
scope: whole repository
reviewer: Claude Code
---

# Code Review: 2026-10-27

A full read of the repository at commit `4f3d6de` (about 2,500 lines of Go plus config and docs), with the toolchain run alongside.

> [!summary]
> Clean layering, solid store tests, and safe path handling: slugs only emit `[a-z0-9-]`, so path traversal is impossible. However, **one design flaw makes the app unusable as the vault grows**, there are **several data-loss paths**, and the memory-hygiene claims in the docs are stronger than what the code does.

## Toolchain results

| Check | Result |
|---|---|
| `go vet ./...` | ✅ clean |
| `go test -race ./...` | ✅ passes. `internal/ui` has **no tests** |
| `gofmt -l` | ❌ `internal/ui/app.go`, `internal/ui/fileops.go` |
| `golangci-lint config verify` | ❌ invalid v2 config (`enable-all`, `linters-settings`, `issues.exclude-rules`) |
| `govulncheck` | ⚪ not installed locally, not run |

## Findings

### Critical

| # | Finding | Fix |
|---|---|---|
| 1 | Per-file scrypt makes unlock and save cost $O(n)$ scrypts; the list refresh runs on the UI goroutine | [[F01 Scrypt-wrapped identity]], [[F10 Keep vault IO off the UI goroutine]] |
| 2 | Changing the passphrase can strand secrets: skipped files, half-re-encrypted vault, sentinel mismatch | [[F01 Scrypt-wrapped identity]], [[F02 Atomic file writes]] |
| 3 | Slug collisions and creates both overwrite existing secrets silently | [[F03 Prevent silent overwrites]] |
| 4 | First unlock creates the vault with an unconfirmed passphrase; `CopyTo` clobbers a foreign sentinel | [[F04 Confirm passphrase on vault creation]] |

### High

| # | Finding | Fix |
|---|---|---|
| 5 | The list doesn't refresh after editing or deleting a secret selected from it (`onSave` is only wired by the Add buttons) | [[F05 Refresh list after edit and delete]] |
| 6 | Settings (vault location, timeouts) aren't persisted | [[F06 Persist settings]] |
| 7 | Auto-lock fires mid-edit and discards changes | [[F07 Inactivity-based auto-lock]] |
| 8 | Clipboard clear is unconditional, and overlapping copies cut each other's TTL short | [[F08 Correct clipboard clearing]] |
| 9 | File import treats any read error as EOF and saves a truncated file | [[F09 Fail import on read errors]] |

### Medium and low

| Area | Finding | Fix |
|---|---|---|
| Memory | Passphrase copies live outside memguard; plaintext secrets are kept after lock; docs overclaim | [[F11 Memory hygiene and honest docs]] |
| CI | Go version mismatch, impossible `CGO_ENABLED=0` Fyne builds, no lint step | [[F12 Fix CI pipeline]] |
| Project | No LICENSE/CHANGELOG, `yourorg` placeholders, `claude.resume` untracked, unformatted files | [[F13 Project hygiene]] |
| Code | Hand-rolled `strings.Split`/`TrimSpace`/`Join`, dead code, ignored `ctx` | [[F14 Remove reinvented stdlib and dead code]] |
| Tests | No `ui` tests even though most bugs are in `ui` | [[F15 UI tests with fyne test]] |

## Outcome

Progress is tracked in the [[Roadmap]]. Completed so far:
- [[F02 Atomic file writes]]: every vault write is atomic
- [[F01 Scrypt-wrapped identity]]: one scrypt per unlock, single-file passphrase change, crash-safe migration
- [[F03 Prevent silent overwrites]]: `Create`/`Update` split and `ErrSecretExists`
- [[F04 Confirm passphrase on vault creation]]: separate create screen with confirmation; unlock only opens
- [[F10 Keep vault IO off the UI goroutine]]: all vault I/O runs in the background on a session-scoped runner
- [[F05 Refresh list after edit and delete]]: saves, imports and deletes update the list in place; the highlight follows the selected secret
- [[F07 Inactivity-based auto-lock]]: real inactivity, a 30 s warning, generation-checked timers
- [[F08 Correct clipboard clearing]]: per-copy countdown, clears only vlt's value, cleared on lock and quit
- [[F09 Fail import on read errors]]: read errors and oversized files fail the import; file I/O runs in the background; dialogs are dismissed on lock

These findings became the five-phase [[Roadmap]]. The storage-format change ([[ADR-001 Scrypt-wrapped X25519 identity]]) goes first because it's cheapest before real vaults exist that would need migrating.
