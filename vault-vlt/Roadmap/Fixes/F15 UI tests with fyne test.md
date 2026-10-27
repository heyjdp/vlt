---
id: F15
title: UI tests with fyne/test
aliases:
  - F15
tags:
  - fix
  - testing
severity: medium
status: todo
phase: 5
area: ui
review_finding: Tests
files:
  - internal/ui/
  - internal/testutil/fixtures.go
depends_on:
  - "[[F04 Confirm passphrase on vault creation]]"
  - "[[F05 Refresh list after edit and delete]]"
  - "[[F07 Inactivity-based auto-lock]]"
  - "[[F12 Fix CI pipeline]]"
created: 2026-10-27
---

# F15: UI tests with fyne/test

## Problem
`internal/ui` has **zero tests**, yet most of the bugs in the [[2026-10-27 Code Review]] are in that package.

## Fix
Use `fyne.io/fyne/v2/test`, which renders headlessly with no display. Since F10, tests inject an inline or queued `runner` and use real vaults created with a low scrypt work factor (`store.WithWorkFactor(10)`), so no fake store is needed.

Auto-lock is tested with an injectable `timerFactory` whose fake timers are fired by hand (since F07).

## Test matrix
| Flow | Test |
|---|---|
| Create vault | ✅ Mismatched confirm shows an error; matching creates `vlt.age` (F04, F10) |
| Unlock | Wrong passphrase shows "Incorrect passphrase"; the entry is cleared afterwards |
| Edit | ✅ Editing a secret selected from a search-filtered list updates its row (`TestApp_SelectThenEditUpdatesFilteredRow`, `TestApp_SaveFromListSelectionUpdatesList`) |
| Delete | ✅ Deleting a selected secret removes its row and clears the selection (`TestApp_SelectThenDeleteRemovesRowAndClearsSelection`, `TestApp_DeleteRemovesSecretFromList`). The confirmation dialog itself (`confirmDelete`) is not yet driven by a test |
| Auto-lock | ✅ Typing postpones it; idleness locks; warning works (`internal/ui/activity_test.go`). No secrets referenced after lock: see [[F11 Memory hygiene and honest docs]] |
| Clipboard | ✅ Copy A then B, B lasts the full TTL; unrelated content kept; lock clears (`internal/ui/clipboard_test.go`) |
| Detail form | ✅ Every field saved, secrets masked, copy buttons copy the right value, new vs existing form (`internal/ui/detail_test.go`) |
| Import | ✅ A failing reader saves nothing and shows an error (`TestApp_FailingImportSavesNothing`, `_FailingImportShowsErrorNotGroupForm`). ✅ Import adds to the list (`TestApp_ImportAddsSecretToList`) |

## Acceptance criteria
- [ ] All of the above run in CI under `-race`
- [ ] `internal/ui` coverage ≥ 60 %

## Related
- [[Roadmap]]
