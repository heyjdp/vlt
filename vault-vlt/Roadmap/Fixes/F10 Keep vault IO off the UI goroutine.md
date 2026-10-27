---
id: F10
title: Keep vault IO off the UI goroutine
aliases:
  - F10
tags:
  - fix
  - ui
  - performance
severity: high
status: todo
phase: 2
area: ui
review_finding: "#1"
files:
  - internal/ui/app.go
  - internal/ui/list.go
  - internal/ui/detail.go
  - internal/ui/fileops.go
  - internal/store/store.go
depends_on:
  - "[[F01 Scrypt-wrapped identity]]"
created: 2026-10-27
---

# F10: Keep vault I/O off the UI goroutine

## Problem
These all run synchronously on the Fyne main goroutine:
- `onUnlock`: `store.Open` (one scrypt, or one per secret during the one-time migration of a format-1 vault) or `store.Create`
- `saveSecret` and import: encrypt
- the `onSave` → `SecretList.Refresh` → `Store.List` chain

`Store.List` ignores its context (`_ = ctx`, `internal/store/store.go:261`), so `Lock()` can't cancel a running list. Even after [[F01 Scrypt-wrapped identity]], unlock still costs one scrypt (about 0.75 s), and that shouldn't freeze the window.

## Fix
- Run store calls in goroutines and marshal results back with `fyne.Do` (pattern from `CLAUDE.md` §Fyne GUI Guidelines).
- Disable the triggering button and show a progress indicator while the call is in flight.
- `Store.List` checks `ctx.Err()` on each iteration. All `Store` methods honour `ctx`.
- After a single save, update that one row instead of re-listing.
- Cancel in-flight work in `Lock()` (`cancelCtx` already exists).

## Acceptance criteria
- [ ] No `store.*` or `crypto.*` call on the main goroutine (audit by grep and review)
- [ ] `List` returns `context.Canceled` promptly when cancelled
- [ ] `go test -race` is clean, including new UI tests

## Related
- [[Scrypt Cost Model]] · [[F05 Refresh list after edit and delete]]
