---
id: F19
title: "Follow XDG_DATA_HOME on Linux"
aliases:
  - F19
tags:
  - fix
severity: low
status: done
phase: 6
area: config
review_finding: Reinstall verification
files:
  - cmd/vlt/main.go
depends_on: []
created: 2026-10-28
---

# F19: Follow XDG_DATA_HOME on Linux

## Problem
`defaultVaultDir` hard-codes `~/.local/share/vlt/vault` on Linux and the BSDs, ignoring `XDG_DATA_HOME`.

## Fix
Use `$XDG_DATA_HOME/vlt/vault` when `XDG_DATA_HOME` is an absolute path, otherwise `~/.local/share/vlt/vault` (per the XDG Base Directory spec). Earlier versions always used `~/.local/share`, so a vault found there is kept when the XDG location holds none; an unreadable XDG location is never abandoned for the old one.

## Acceptance criteria
- [x] Table test over GOOS and environment: `TestVaultDirFor`, `TestVaultDirFor_HomeErrorIsReported`
- [x] `go test -race ./...` passes

## Outcome
Verification found and fixed:
- **Launches could disagree.** `XDG_DATA_HOME` is often set in a shell profile but not in the desktop session. A vault created from a terminal would then be missing when vlt is started from the app menu. The vault location is now saved after every unlock or create (unless `VLT_VAULT_DIR` sets it), so the default is only computed until a vault first opens (`TestApp_DefaultLocationIsSavedAfterUnlock`, `TestApp_LocationFromEnvironmentIsNeverSaved`). This replaced the "pending location" flag from F06/F17.
- The fallback to the old location, and any errors while checking, are logged.
- The tests used `/data` as an absolute path, which isn't absolute on Windows.

Accepted:
- **Flatpak.** It would sandbox `XDG_DATA_HOME`, but no Flatpak is planned.
- **The vault check in `main`.** It runs before the window, but only when `XDG_DATA_HOME` points somewhere other than `~/.local/share`, and it is a few `stat` calls.

## Related
- [[Roadmap]]
