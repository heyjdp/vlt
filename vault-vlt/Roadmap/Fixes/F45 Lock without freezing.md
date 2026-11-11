---
id: F45
title: "Lock without freezing"
aliases:
  - F45
tags:
  - fix
severity: low
status: in-progress
phase: 12
area: ui
review_finding: Go skills audit 2026-11-11, item 8; F44 verification
files:
  - internal/crypto/identity.go
  - internal/crypto/identity_internal_test.go
  - vault-vlt/Architecture/Security Model.md
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F33
  - F44
created: 2026-11-12
---

# F45: Lock without freezing

## Problem
- **A lock during *Change Passphrase* waited for scrypt.** `WrapIdentity` held the identity's mutex for the whole scrypt derivation (about a second at 2^18, up to about four at the 2^20 cap). *Lock* calls `Zero()` on the main goroutine while holding the session's mutex, so it blocked on that derivation: the window froze, every keystroke's `Activity()` waited on the session mutex, and the key stayed in memory until scrypt finished.

## Fix
- **The derivation runs outside the identity's mutex.** `wrapTo` (split out of `WrapIdentity`, taking any age recipient) calls `age.Encrypt`, which runs the recipient's key derivation before the key is needed, without the mutex. Only writing the encoded key into the encrypted stream holds it. If the key was zeroed during the derivation, the wrap fails with `ErrIdentityZeroed` and returns nothing, which *Change Passphrase* already reports after the lock as "Passphrase Not Changed".
- **Decision: no copy of the key.** Copying the key out under the mutex and deriving afterwards would leave a second copy alive for the seconds of the derivation, which `Zero()` would then have to track. Deriving first needs no copy at all.

## Acceptance criteria
- [ ] `TestWrapTo_ZeroDuringTheKeyDerivationIsPromptAndTheWrapFails` fails without the fix (Zero waits), and without the zeroed check after the derivation (the wrap succeeds)
- [ ] `go test -race ./...` and golangci-lint pass
- [ ] README, DESIGN, CHANGELOG, Security Model

## Related
- [[Roadmap]]
- [[F33 Vault session module]]
- [[F44 Auto-lock during long operations]]
- [[F43 Scrypt work factor cap]]
- [[Security Model]]
