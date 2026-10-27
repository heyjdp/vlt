---
id: F04
title: Confirm passphrase on vault creation
aliases:
  - F04
tags:
  - fix
  - ui
  - store
severity: critical
status: todo
phase: 1
area: ui
review_finding: "#4"
files:
  - internal/ui/app.go
  - internal/ui/unlock.go
  - internal/ui/settings.go
  - internal/store/store.go
depends_on:
  - "[[F01 Scrypt-wrapped identity]]"
created: 2026-10-27
---

# F04: Confirm passphrase on vault creation

## Problem
- `App.onUnlock` (`internal/ui/app.go:92`) creates a new vault with **whatever was typed** when there's no sentinel. A typo becomes the permanent passphrase.
- "Switch Vault" to an empty or wrong directory does the same.
- `Store.Verify` returns `nil` when the sentinel is missing, even if the directory already holds `.age` files. Any passphrase then "unlocks" it, and the existing files are silently skipped.
- `Store.CopyTo` overwrites `vlt.age` in a destination that already holds a different vault.

## Fix
- Decide the screen before showing it: if `vlt.age` is missing, show a **Create vault** screen with *passphrase* + *confirm* fields and strength guidance ([[Passphrase Strength]]: recommend 5+ Diceware words).
- If `vlt.age` is missing but `.age` files exist, refuse and report a damaged vault instead of creating one.
- `CopyTo` refuses a destination that already contains `vlt.age` or any `.age` files.

## Acceptance criteria
- [ ] A new vault needs two matching entries
- [ ] A directory with secrets but no `vlt.age` is reported, not adopted
- [ ] Moving into an existing vault fails without modifying it
- [ ] `fyne/test` coverage ([[F15 UI tests with fyne test]])

## Related
- [[Unlock Flow]]
