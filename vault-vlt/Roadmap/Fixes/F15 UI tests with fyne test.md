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
Use `fyne.io/fyne/v2/test`, which renders headlessly with no display. Put a small interface in front of the store at the point of use in `ui`, so tests can use an in-memory fake (`internal/testutil.FakeVault`) instead of real scrypt.

Use a lock-timeout config in milliseconds plus an injectable clock to test auto-lock without sleeping.

## Test matrix
| Flow | Test |
|---|---|
| Create vault | Mismatched confirm shows an error; matching creates `vlt.age` |
| Unlock | Wrong passphrase shows "Incorrect passphrase"; the entry is cleared afterwards |
| Edit | Selecting and editing a secret updates its list row ([[F05 Refresh list after edit and delete]]) |
| Delete | Selecting and deleting a secret removes its row |
| Auto-lock | Typing postpones it; idleness locks; no secrets referenced after lock |
| Clipboard | Copy A then B, B lasts the full TTL ([[F08 Correct clipboard clearing]]) |
| Import | A failing reader saves nothing ([[F09 Fail import on read errors]]) |

## Acceptance criteria
- [ ] All of the above run in CI under `-race`
- [ ] `internal/ui` coverage ≥ 60 %

## Related
- [[Roadmap]]
