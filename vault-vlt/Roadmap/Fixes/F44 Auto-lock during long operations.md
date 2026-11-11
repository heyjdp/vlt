---
id: F44
title: "Auto-lock during long operations"
aliases:
  - F44
tags:
  - fix
severity: medium
status: in-progress
phase: 12
area: ui
review_finding: Go skills audit 2026-11-11, item 7
files:
  - internal/store/store.go
  - internal/store/store_internal_test.go
  - internal/ui/app.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F33
  - F41
created: 2026-11-12
---

# F44: Auto-lock during long operations

## Problem
*Move Vault* (`store.CopyTo`) and *Change Passphrase* run on the session's operations runner, so the auto-lock cancels them like any other work. A move to a server with no input for the timeout (30 s at least) was cut off part-way and its result dropped:
- `CopyTo` wrote `vlt.age` and `vlt.json` first, so the destination looked like a whole vault with only some secrets. Moving there again failed with "vault already exists"; switching there opened a vault with secrets missing.
- A passphrase change locked out during its write of `vlt.age` (seconds on a server) could take effect without the user being told.

## Fix
- **`CopyTo` copies `vlt.age` last** (after `vlt.json` and every secret, checking its context before each file). An unfinished copy has no key: `Exists` still reports a vault (nothing is created over it) and `Open` says `ErrKeyMissing`, whose message now suggests moving the vault there again. **Moving again finishes it**: a destination without `vlt.age` or `vlt.migrating.age` whose secret files (and `vlt.json`) are byte for byte this vault's is completed; anything else is still refused (`ErrVaultExists`), since it may be another vault whose key a backup could restore.

## Acceptance criteria
- [ ] `TestStore_CopyTo_AnInterruptedCopyHasNoKeyFile`, `…FinishesAnInterruptedCopyOfThisVault` and `…RefusesAKeylessVaultItDidNotCopy` fail without their fixes
- [ ] `go test -race ./...` and golangci-lint pass
- [ ] README, DESIGN, CHANGELOG, GLOSSARY, Security Model

## Related
- [[Roadmap]]
- [[F33 Vault session module]]
- [[F41 Operations]]
- [[Security Model]]
