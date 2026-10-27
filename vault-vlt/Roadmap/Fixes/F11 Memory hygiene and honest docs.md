---
id: F11
title: Memory hygiene and honest docs
aliases:
  - F11
tags:
  - fix
  - security
  - docs
severity: medium
status: todo
phase: 3
area: crypto
review_finding: Memory hygiene
files:
  - internal/crypto/identity.go
  - internal/ui/app.go
  - internal/ui/unlock.go
  - internal/ui/settings.go
  - README.md
  - DESIGN.md
depends_on:
  - "[[F01 Scrypt-wrapped identity]]"
created: 2026-10-27
---

# F11: Memory hygiene and honest docs

## Problem
See [[Security Model#Where key material lives (after F01)]]. In short:
- Since [[F01 Scrypt-wrapped identity]], the passphrase is used once to unwrap `vlt.age` and is not held for the session. The long-lived secret is the X25519 vault key.
- The vault key's encoded form is in a `LockedBuffer`, but age's parsed `X25519Identity` lives on the Go heap for the session, along with one string copy made while parsing (and `x.String()` in `GenerateIdentity`). `Zero()` cannot wipe these.
- `UnlockScreen.entry.Text` still holds the passphrase for the whole session.
- `Lock()` keeps `a.secretList` and `a.detailView`, which hold every decrypted secret.
- The change-passphrase entries are only cleared on success.
- `DESIGN.md:293` claims the passphrase is never kept in memory beyond derivation.

## Fix
- Clear the unlock and change-passphrase entries right after reading them, on **every** path.
- In `Lock()`, nil out `secretList`, `detailView` and `unlockScreen`. The clipboard is already cleared on lock by [[F08 Correct clipboard clearing]].
- ✅ The passphrase is discarded after unwrapping (F01). Remaining: decide whether to parse the key per operation (more transient copies) or keep one parsed copy (current), and document the trade-off.
- Rewrite the README and DESIGN security sections to describe **best-effort** protection and its limits (Go strings, GC, Fyne widgets).

## Acceptance criteria
- [ ] After unlock, the unlock entry is empty
- [ ] After lock, `App` holds no reference to any `*store.Secret` (unit test on the struct fields)
- [ ] The docs no longer overclaim

## Related
- [[Security Model]]
