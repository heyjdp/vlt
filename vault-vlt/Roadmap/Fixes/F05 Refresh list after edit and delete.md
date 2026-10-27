---
id: F05
title: Refresh list after edit and delete
aliases:
  - F05
tags:
  - fix
  - ui
severity: high
status: done
phase: 2
area: ui
review_finding: "#5"
files:
  - internal/ui/app.go
  - internal/ui/detail.go
  - internal/ui/list.go
  - internal/ui/list_test.go
  - internal/ui/app_test.go
depends_on: []
created: 2026-10-27
---

# F05: Refresh list after edit and delete

## Problem
`DetailView.onSave` is only set inside the **Add Password** and **Add Record** button handlers (`internal/ui/app.go:141,148`). After a fresh unlock, selecting a secret from the list and then saving or deleting it calls a `nil` callback, so the list goes stale. A deleted secret stays visible and selectable.

## Resolution
Implemented as part of [[F10 Keep vault IO off the UI goroutine]]. `DetailView.SetCallbacks` is set once in `showMainView`, saves and imports call `SecretList.Upsert`, and deletes call `SecretList.Remove`, which also calls `UnselectAll`. Selection-driven tests and the edge-case fixes below were added during F05's own verification.

## Fix (original plan)
Set `detailView.SetOnSave(...)` once in `showMainView` and remove the per-button calls. After a delete, also clear the list's selection (`list.UnselectAll()`).

## Acceptance criteria
- [x] After a fresh unlock, deleting a selected secret removes its row and clears the highlight: `TestApp_SelectThenDeleteRemovesRowAndClearsSelection`, `TestApp_DeleteRemovesSecretFromList`
- [x] Editing a selected secret updates its row and keeps the search filter: `TestApp_SelectThenEditUpdatesFilteredRow`, `TestApp_SaveFromListSelectionUpdatesList`, `TestApp_NewSecretSavedDuringSearchAppearsIfItMatches`
- [x] New and imported secrets are added: `TestApp_SaveNewSecretAddsItToList`, `TestApp_ImportAddsSecretToList`
- [x] Covered by [[F15 UI tests with fyne test]] (matrix rows ticked)

## Outcome
The core fix shipped in F10's commit. F05's verification found and fixed four edge cases:
- An import replacing the open secret left a stale form that could overwrite it. The form is now re-rendered.
- A delete finishing after the user moved on cleared the other secret's panel. The panel is now cleared only if it still shows the deleted secret, and the list receives a snapshot.
- A save and a delete could interleave. A busy guard now refuses the second.
- The selection highlight drifted when an edit moved a secret out of the filter. The list now tracks the selected secret.

Mutation testing also added tests for legacy untrimmed or differently-cased names, a new secret saved during a search, and a delete during the first load.

## Related
- [[F10 Keep vault IO off the UI goroutine]]: the refresh must not block the UI
