---
id: F14
title: Remove reinvented stdlib and dead code
aliases:
  - F14
tags:
  - fix
  - refactor
severity: low
status: todo
phase: 5
area: ui
review_finding: Code quality
files:
  - internal/ui/detail.go
  - internal/ui/app.go
  - internal/ui/settings.go
  - internal/store/store.go
  - internal/store/secret.go
depends_on: []
created: 2026-10-27
---

# F14: Remove reinvented stdlib and dead code

## Reinvented stdlib
`internal/ui/detail.go:403-475` hand-rolls about 70 lines that the standard library already provides:

| Hand-rolled | Replace with |
|---|---|
| `split`, `indexOf` | `strings.Split` |
| `trimSpace` | `strings.TrimSpace` (also handles Unicode whitespace) |
| `joinTags` (string `+=` in a loop) | `strings.Join(tags, ", ")` |
| `splitAndTrim` | loop over `strings.Split` + `strings.TrimSpace` |

## Dead or misleading code
- `SettingsScreen.identity` is stored but never used
- `App.fyneApp` and `App.ShowError` are unused
- `_ = ctx` in `App.showMainView` and `Store.List`
- `Store.RecreateSentinel` is just an alias of `CreateSentinel` (both go away with [[F01 Scrypt-wrapped identity]])
- `store.ValidSecretTypes`, `crypto.Identity.IsZeroed`: keep only if used outside tests
- `Store.Groups()` swallows errors; return `([]string, error)`

## Smaller items
- The TOTP secret uses `widget.NewEntry`; switch it to `NewPasswordEntry`
- `showPasswordDetail` and `showRecordDetail` duplicate about 60 lines; extract a shared form builder

## Acceptance criteria
- [ ] `golangci-lint run` is clean (`unused`, `unparam`, `dupl`) once [[F12 Fix CI pipeline]] lands
- [ ] Unit test for `parseTags` covering Unicode whitespace

## Related
- [[2026-10-27 Code Review]]
