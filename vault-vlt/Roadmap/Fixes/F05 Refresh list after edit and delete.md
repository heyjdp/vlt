---
id: F05
title: Refresh list after edit and delete
aliases:
  - F05
tags:
  - fix
  - ui
severity: high
status: todo
phase: 2
area: ui
review_finding: "#5"
files:
  - internal/ui/app.go
  - internal/ui/detail.go
  - internal/ui/list.go
depends_on: []
created: 2026-10-27
---

# F05: Refresh list after edit and delete

## Problem
`DetailView.onSave` is only set inside the **Add Password** and **Add Record** button handlers (`internal/ui/app.go:141,148`). After a fresh unlock, selecting a secret from the list and then saving or deleting it calls a `nil` callback, so the list goes stale. A deleted secret stays visible and selectable.

## Fix
Set `detailView.SetOnSave(...)` once in `showMainView` and remove the per-button calls. After a delete, also clear the list's selection (`list.UnselectAll()`).

## Acceptance criteria
- [ ] After a fresh unlock, deleting a selected secret removes it from the list
- [ ] Editing a selected secret updates the row
- [ ] Covered by [[F15 UI tests with fyne test]]

## Related
- [[F10 Keep vault IO off the UI goroutine]]: the refresh must not block the UI
