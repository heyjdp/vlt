---
id: F43
title: "Scrypt work factor cap"
aliases:
  - F43
tags:
  - fix
severity: low
status: done
phase: 12
area: crypto
review_finding: Go skills audit 2026-11-11, item 6
files:
  - internal/crypto/identity.go
  - internal/crypto/identity_test.go
  - internal/crypto/workfactor_internal_test.go
  - internal/store/vault.go
  - internal/store/migrate_test.go
  - internal/ui/errmsg_test.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
  - vault-vlt/Architecture/Security Model.md
  - vault-vlt/Decisions/ADR-001 Scrypt-wrapped X25519 identity.md
depends_on: []
created: 2026-11-12
---

# F43: Scrypt work factor cap

## Problem
`crypto.DecryptWithPassphrase` used `age.NewScryptIdentity` with age's default cap of 2^22: about 4 GiB of memory and some 15 s per derivation. Whoever can write `vlt.age` (a hostile server, a shared folder) could raise its work factor so that every unlock exhausted memory or crashed a smaller machine, and a migration, which runs one derivation per legacy file, far more. vlt only ever writes 2^18.

## Fix
- **`crypto.MaxWorkFactor`** = 20 (1 GiB, a few seconds), above `DefaultWorkFactor` (18).
- **`checkWorkFactor`** reads the scrypt stanza (`-> scrypt <salt> <logN>`) from the age header before anything else and refuses a factor above the cap with **`crypto.ErrWorkFactorTooHigh`**, so no derivation runs. age's own cap is set to the same value as a second guard; its error has no type, so it can't say why on its own.
- **Tests**: factors 21, 22 and 30 written into a real scrypt file's header are refused with `ErrWorkFactorTooHigh` in well under a second; `MaxWorkFactor` is never below `DefaultWorkFactor`; opening a real vault whose `vlt.age` asks for 2^22 says so in plain words.

## Outcome
Verification found no way past the check (mangled stanza lines are refused by it or by age before deriving; `---` can't appear in base64) and no legitimate vault over the cap: every vlt version wrote 2^18. It found:
- **`WrapIdentity` accepted any factor**, so `store.WithWorkFactor(21…)` made a vault that could never be reopened (and above 30 age panicked). It now refuses a factor above the cap.
- **An old vault without `vlt.age`** read every over-cap file as a wrong passphrase while probing for one it could open. That probe now returns the cap error.
- **The boundary was untested**: `>=` for `>` passed. Internal tests now pin 18 and 20 accepted, 21 refused, and that what `WrapIdentity` writes passes.
- Docs: the CHANGELOG entry covers upgrading old vaults and says what you see; README and ADR-001 mention the cap; a stale comment on `WrapIdentity` and a test name that overclaimed were fixed.

## Acceptance criteria
- [x] `TestDecryptWithPassphrase_RefusesAWorkFactorAboveTheCap`, `TestCheckWorkFactor_TheCapIsInclusive` and `TestStore_Open_LegacyFileOverTheWorkFactorCapSaysSo` fail without their fixes
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG, Security Model, ADR-001

## Related
- [[Roadmap]]
- [[ADR-001 Scrypt-wrapped X25519 identity]]
- [[Security Model]]
