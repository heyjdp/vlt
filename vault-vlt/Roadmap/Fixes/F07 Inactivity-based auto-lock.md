---
id: F07
title: Inactivity-based auto-lock
aliases:
  - F07
tags:
  - fix
  - ui
  - security
severity: high
status: todo
phase: 2
area: ui
review_finding: "#7"
files:
  - internal/ui/app.go
depends_on: []
created: 2026-10-27
---

# F07: Inactivity-based auto-lock

## Problem
`resetLockTimer` (`internal/ui/app.go:236`) is only called from toolbar buttons. Typing, list selection, scrolling and dialogs don't reset it. With the default 5-minute timeout, the vault locks while you're editing a secret, and `Lock()` replaces the window content, **discarding unsaved edits**.

Formally, the lock fires at $t_\text{last toolbar click} + \tau$ rather than $t_\text{last input} + \tau$.

## Fix
- Reset on all input: wrap the window content in a thin custom widget that implements `fyne.Tappable`/`desktop.Hoverable`, and hook `window.Canvas().SetOnTypedKey`/`SetOnTypedRune`. Fyne 2.6+ also lets you register a desktop canvas key-down handler.
- Before locking with a dirty form, either warn ("Locking in 30 s, unsaved changes") or keep the draft in memory for after unlock. Warning is simpler and safer.
- Read the timeout from the live config ([[F06 Persist settings]]).

## Acceptance criteria
- [ ] Typing in any entry postpones the lock
- [ ] A test with a short timeout locks after idleness and doesn't lock during simulated typing
- [ ] Covered by [[F15 UI tests with fyne test]]

## Related
- [[Security Model]]
