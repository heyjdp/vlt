---
id: F17
title: "Open an existing vault from the create screen"
aliases:
  - F17
tags:
  - fix
severity: high
status: todo
phase: 6
area: ui
review_finding: Reinstall verification
files:
  - internal/ui/create.go
  - internal/ui/app.go
depends_on: []
created: 2026-10-28
---

# F17: Open an existing vault from the create screen

## Problem
When no vault exists at the configured location, the app shows only "Create a new vault" (`internal/ui/create.go`). Settings, where the location can be changed, is reachable only after unlocking.

## Impact
If the preferences file is lost (cleaner app, new Mac, restored home folder) and the vault was in a custom folder, the user sees an empty create screen and may believe their secrets are gone. Recovering needs `VLT_VAULT_DIR` or a throwaway vault.

## Fix
Add **Open existing vault…** to the create screen: pick a folder, check it holds a vault (`store.Exists`), save it as the vault location, and show the unlock screen. A folder without a vault is refused with a clear message.

## Acceptance criteria
- [ ] Picking a folder with a vault shows the unlock screen for it and saves the location
- [ ] Picking a folder without a vault shows an error and stays on the create screen
- [ ] When the location comes from `VLT_VAULT_DIR`, the choice is used but not saved
- [ ] `go test -race ./...` passes

## Related
- [[Roadmap]]
