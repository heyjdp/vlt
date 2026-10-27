---
id: F06
title: Persist settings
aliases:
  - F06
tags:
  - fix
  - ui
severity: high
status: todo
phase: 2
area: ui
review_finding: "#6"
files:
  - cmd/vlt/main.go
  - internal/ui/settings.go
  - internal/ui/detail.go
  - internal/ui/app.go
depends_on: []
created: 2026-10-27
---

# F06: Persist settings

## Problem
`Config` is built from environment variables in `main.go` and only changed in memory by `SettingsScreen`:
- After **Move Vault**, the next launch opens the *old* directory.
- Auto-lock and clipboard TTL changes are lost on restart.
- `DetailView` copies `clipboardTTL` when it's constructed (`internal/ui/detail.go:36`), so a TTL change doesn't apply until the next unlock.

## Fix
- Store non-secret settings with `fyne.App.Preferences()` (keys `vaultDir`, `lockTimeoutMs`, `clipboardTTLs`), or in a small JSON file in the OS config dir.
- Precedence: env var → saved preference → default.
- Pass a `*Config` (or getter) to `DetailView` instead of a copied int.
- Validate ranges: lock timeout from 30 s to 30 min or `0` = off; TTL from 5 s to 120 s.

## Acceptance criteria
- [ ] Moving the vault and relaunching opens the new location
- [ ] A TTL change applies to the next copy without relocking
- [ ] Out-of-range env values are clamped and logged

## Related
- [[F07 Inactivity-based auto-lock]] · [[F08 Correct clipboard clearing]]
