---
id: F52
title: "Main view"
aliases:
  - F52
tags:
  - fix
severity: low
status: done
phase: 13
area: ui
review_finding: "Architecture review 2026-11-12 (third pass), candidate 4"
files:
  - internal/ui/mainview.go
  - internal/ui/mainview_test.go
  - internal/ui/app.go
  - internal/ui/list.go
  - internal/ui/detail.go
  - internal/ui/fileops.go
  - internal/ui/updates.go
  - internal/ui/list_test.go
  - internal/ui/detail_test.go
  - internal/ui/totp_test.go
  - internal/ui/fileops_test.go
  - internal/ui/app_test.go
  - DESIGN.md
  - README.md
depends_on:
  - F51
  - F41
  - F11
  - F33
created: 2026-11-12
---

# F52: Main view

## Problem
- **App assembled the main view by setting fields.** `showMainView` (about 75 lines) built the secret list, the detail panel and file operations, then wired them to each other after construction: `detailView.newTimer`, `onRender`, `SetCallbacks`, `SetFileOps`, the activity tracker. How a save reaches the list, or an import re-renders a form, could only be read off `app.go`, `list.go`, `detail.go` and `fileops.go` together.
- **The lock's wipe was spread over App.** `dropSession` had to know each part's way of letting go (`sessionOps.end`, `detailView.clear`, `secretList.clear`) and which fields to drop (list, detail panel, main content, tracker, session operations).
- **Panel tests paid for a whole App.** Every list, detail, one-time code and file operation test built an App, created a vault, unlocked through the unlock screen and then reached into App's fields (`openRecordAt59`, `openOnly`, `exportApp`): about 120 reach-ins across those four test files.

## Fix
- **`mainView` (`internal/ui/mainview.go`).** Built per session by `newMainView(&mainViewDeps{…})` from the open store, the session's operations, the clipboard's copy, the status bar, the logger, a timer factory and a clock (one-time codes), the activity callback, the status row (footer), and the Lock and Settings actions. It builds the list, the detail panel and file operations, wires them at construction, lays them out under the header and starts the first load. Its interface for App is `content()` and `wipe()`; `openSettings()` is the Settings button's action, also used by the Settings tests.
- **Wiring through constructors.** `newSecretList(d, show)` takes the function that shows a selected secret (no `*DetailView`); `newDetailView(d, detailHooks{groups, saved, deleted, export, rendered})`; `newFileOps(d, groups, onSaved)`. `SetCallbacks`, `SetFileOps` and the detail panel's "file operations are not available" branch (unreachable once wiring happens at construction) are gone.
- **One wipe.** `wipe` ends the operations (an export waiting on its save dialog wipes its plaintext; later answers are dropped), clears the detail panel (stops a one-time code and wipes its key, blanks the form's entries, empties the shown secret), clears the list (empties every loaded secret struct) and forgets the tracked widgets. App's `dropSession` wipes and drops the view and removes the key handlers; `Lock` still clears the clipboard and dismisses dialogs, as before.
- **App.** `showMainView` builds the session's operations and the view, shows it, sets "Unlocked" and the key handlers. App no longer holds the list, detail panel, main content, tracker or session operations; `settingsScreen(s, ops)` builds Settings for the view. `announceRelease` asks `a.main == nil`.
- **Decision: the view is given the session's operations and owns them.** App builds them (it knows the session's pause and relock), the view ends them on wipe, and the Settings closure captures the same operations, so a vault change from Settings and a save share one re-entry policy. The list loads on the operations' runner rather than a second runner parameter, so the two can't disagree about the session context.
- **Decision: the footer is a dependency.** The status row (with *About*) stays part of the view's tree, so the tracker instruments it exactly as before; App still owns the status bar on every other screen.
- **Decision: key handlers stay with App.** The window's typed-key and typed-rune handlers belong to the window, which App owns; App sets them with the view and removes them on lock.
- **Decision: no guarded-timer type.** The clipboard countdown and the one-time code tick share a generation-plus-timer shape (dropped candidate F), but nothing in this change touched either, so folding them would have been a separate refactor.
- **Decision: deps as a struct pointer shared by the panel constructors.** The panels are now parts of the view; taking `*mainViewDeps` keeps their constructors short and makes their dependencies the view's.

## Outcome
Two commits on `refactor/deepen-open-and-lock`, each with build, vet, `go test -race` and golangci-lint green:
- `refactor(ui)`: `mainView`, constructor wiring, App shrunk to building and wiping the view; tests reach the panels through `app.main`.
- `test(ui)`: `viewFixture` builds a main view over a temporary vault opened directly (inline or held work, hand-fired timers, settable clock, scripted file chooser, a lock that cancels the session context and wipes); list, detail, one-time code and file operation tests use it, and new view tests cover the wiring and the wipe.

App field reach-ins in the panel tests: totp 62 → 0, detail 28 → 0, list 30 → 0, fileops 17 → 4 (one App-level lock test kept). Nothing changes for the user: same layout, actions, dialogs, wording and lock guarantees.

## Acceptance criteria
- [x] App builds the main view in one call and wipes it in one call; no post-construction field setting from App
- [x] Panel tests build a main view without an App or an unlock; App-level tests remain for unlock → main view and lock → wipe (list and form emptied, export wiped, clipboard cleared, dialogs dismissed, key handlers removed)
- [x] Mutation-checked: removing any line of `wipe` (ending operations, clearing the detail panel, clearing the list, forgetting tracked widgets), the saved/deleted hooks, either half of `imported`, the tracker's instrumenting of the view or of a re-rendered panel, the first load, or showing Settings each fails a test
- [x] DESIGN (new module, app/detail/totp bullets, project tree, security note), README (project tree, test list)

## Related
- [[Roadmap]]
- [[F41 Operations]]
- [[F11 Memory hygiene and honest docs]]
- [[F51 Vault-wide changes]]
- [[F50 Opening a vault]]
- [[F33 Vault session module]]
