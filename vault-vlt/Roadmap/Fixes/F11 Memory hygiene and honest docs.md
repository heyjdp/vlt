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
See [[Security Model#Where the passphrase actually lives]]. In short:
- The passphrase enters as a `string`, gets copied into a `[]byte`, and is copied again by `string(buf.Bytes())` on every `Recipient()` call. age's `ScryptIdentity` also keeps its own copy, which `Zero()` never wipes.
- `UnlockScreen.entry.Text` still holds the passphrase for the whole session.
- `Lock()` keeps `a.secretList` and `a.detailView`, which hold every decrypted secret.
- The change-passphrase entries are only cleared on success.
- `DESIGN.md:293` claims the passphrase is never kept in memory beyond derivation.

## Fix
- Clear the unlock and change-passphrase entries right after reading them, on **every** path.
- In `Lock()`, nil out `secretList`, `detailView` and `unlockScreen`, and clear the clipboard ([[F08 Correct clipboard clearing]]).
- After [[F01 Scrypt-wrapped identity]], discard the passphrase right after unwrapping. Only the X25519 key stays in a `LockedBuffer`, and it's parsed into an `age.X25519Identity` only when needed.
- Rewrite the README and DESIGN security sections to describe **best-effort** protection and its limits (Go strings, GC, Fyne widgets).

## Acceptance criteria
- [ ] After unlock, the unlock entry is empty
- [ ] After lock, `App` holds no reference to any `*store.Secret` (unit test on the struct fields)
- [ ] The docs no longer overclaim

## Related
- [[Security Model]]
