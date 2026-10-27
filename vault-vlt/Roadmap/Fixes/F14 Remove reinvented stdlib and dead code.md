---
id: F14
title: Remove reinvented stdlib and dead code
aliases:
  - F14
tags:
  - fix
  - refactor
severity: low
status: done
phase: 5
area: ui
review_finding: Code quality
files:
  - internal/ui/detail.go
  - internal/ui/detail_test.go
  - internal/ui/app.go
  - internal/store/store.go
  - internal/store/secret.go
  - internal/store/secret_test.go
  - internal/store/atomic.go
  - internal/crypto/identity.go
  - .golangci.yml
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
- ~~`SettingsScreen.identity` is stored but never used~~ (removed in F01)
- ~~`App.ShowError` is unused~~ (removed; `App.fyneApp` is used since F06 and F08)
- ~~`_ = ctx` in `App.showMainView` and `Store.List`~~ (gone since F10)
- ~~`Store.RecreateSentinel` is just an alias of `CreateSentinel`~~ (removed in [[F01 Scrypt-wrapped identity]])
- ~~`store.ValidSecretTypes`, `crypto.Identity.IsZeroed`~~ (removed; tests assert `ErrIdentityZeroed` instead)
- ~~`Store.Groups()` swallows errors~~ (removed instead: since F10, group suggestions come from `SecretList.Groups`)

## Smaller items
- ~~The TOTP secret uses `widget.NewEntry`~~ (now a password entry)
- ~~`showPasswordDetail` and `showRecordDetail` duplicate about 60 lines~~ (shared `showForm` with `formField`/`copyAction`)

## Acceptance criteria
- [x] `golangci-lint run` is clean with `unused`, `unparam` and `dupl`. The last two are now enabled in `.golangci.yml`. Fixing `unparam` replaced the always-identical permission arguments with `filePerm`/`dirPerm`. Fixing `dupl` turned three marshal tests into the table-driven `TestSecret_JSONRoundTrip`.
- [x] Unit test for `parseTags` covering Unicode whitespace: `TestParseTags`

## Outcome
Mutation testing in verification showed the first form tests were weak: 9 of 18 mutants survived. Tests now cover:
- every record and password field being saved (each field changed before saving)
- masking of the password, value and TOTP fields
- copy buttons copying the right value
- new forms having no Delete and disabled copy buttons, and existing forms having a read-only name
- the on-disk JSON key names (`TestSecret_JSONKeysAreStable`)
- the 0700 directory mode

`showForm` is 45 lines after splitting out `formButtons` and `existingFooter`. Splitting further would hurt clarity, which the 40-line soft limit allows.

## Related
- [[2026-10-27 Code Review]]
