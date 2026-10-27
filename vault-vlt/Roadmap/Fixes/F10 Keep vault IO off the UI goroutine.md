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
status: done
phase: 2
area: ui
review_finding: "#1"
files:
  - internal/ui/runner.go
  - internal/ui/app.go
  - internal/ui/app_test.go
  - internal/ui/settings.go
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
- `onUnlock`: `store.Open` (one scrypt, or one per secret during the one-time migration of a format-1 vault); `onCreate`: `store.Create` (one scrypt)
- `saveSecret` and import: encrypt
- the `onSave` → `SecretList.Refresh` → `Store.List` chain

`Store.List` ignores its context (`_ = ctx`, `internal/store/store.go:261`), so `Lock()` can't cancel a running list. Even after [[F01 Scrypt-wrapped identity]], unlock still costs one scrypt (about 0.75 s), and that shouldn't freeze the window.

## Note from F04 verification
Under `fyne/test`, `fyne.Do` runs callbacks inline on the caller's goroutine. The `go a.secretList.Refresh(ctx)` in `showMainView` therefore races with test-driven `Lock()` under `-race`. The fix should give background work an owner that `Lock` waits on or cancels, so app-level tests can enter the main view.

## Fix
- Run store calls in goroutines and marshal results back with `fyne.Do` (pattern from `CLAUDE.md` §Fyne GUI Guidelines).
- Disable the triggering button and show a progress indicator while the call is in flight.
- `Store.List` checks `ctx.Err()` on each iteration. All `Store` methods honour `ctx`.
- After a single save, update that one row instead of re-listing.
- Cancel in-flight work in `Lock()` (`cancelCtx` already exists).

## As shipped
- `runner.go`: `background(r, work, done)` runs `work(ctx)` via `r.spawn` (a goroutine in production; inline or queued in tests) and delivers `done` with `fyne.Do`.
  - After unlock, every main-view component shares a runner bound to the **session context**.
  - `Lock` cancels that context. Store calls receive it, and any result that arrives after the lock is dropped.
- Unlock and create run in the background with a progress bar and disabled inputs. A result for a replaced screen is discarded and its store closed. A generation counter ignores late vault checks.
- The session context is registered before the auto-lock timer starts, closing the window where `Lock` could miss it.
- Saves, deletes and imports update one list row instead of re-listing. Changes made while the first load is in flight are replayed onto it.
- Group suggestions come from the in-memory list, never from reading the directory on the main goroutine.
- All `Store` methods take and check `ctx` (`Create`, `Get`, `Update`, `Delete`, `List` per file, `CopyTo` per file, `ChangePassphrase` before writing).
- `Store.SetDir` was removed; it was unsynchronised, and the move path relocks immediately anyway.

## Acceptance criteria
- [x] No `store.*` or `crypto.*` I/O call on the main goroutine: grep audit, plus `TestApp_UnlockRunsOffTheMainGoroutine` and `TestApp_CreateRunsOffTheMainGoroutine` (queued runner)
- [x] `List` returns `context.Canceled` promptly: `TestStore_List_StopsWhenContextCancelled`, `TestApp_LockCancelsInitialListLoad`
- [x] `go test -race` is clean, including the new UI tests (20 repeated runs in verification)

## Outcome
Verification found ordering defects, all fixed:
- results arriving after lock still acting on the UI
- a save made during the first load being lost
- a delete/save data race on the shared `*Secret`
- overlapping unlock screens
- a timer window around `cancelCtx`

Mutation testing showed nine surviving mutants. Each now has a test.

The F04 note (`fyne/test` runs `fyne.Do` inline) was solved with an injectable runner rather than making `Lock` wait.

> [!todo] Follow-up outside F10's scope
> Import still reads up to 10 MB and base64-encodes it on the main goroutine, and export writes on it. These are file I/O, not store calls. They're handled with [[F09 Fail import on read errors]].

## Related
- [[Scrypt Cost Model]] · [[F05 Refresh list after edit and delete]]
