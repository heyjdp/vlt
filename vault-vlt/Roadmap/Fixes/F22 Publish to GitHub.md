---
id: F22
title: "Publish to GitHub"
aliases:
  - F22
tags:
  - fix
severity: high
status: done
phase: 6
area: ci
review_finding: Release goal
files:
  - go.mod
  - Makefile
  - cmd/vlt/main.go
depends_on: 
  - "[[F21 Release builds for macOS and Linux]]"
created: 2026-10-28
---

# F22: Publish to GitHub

## Problem
The repository had no remote, so CI and the release workflow had never run. The module path and app ID were placeholders (`github.com/yourorg/vlt`, `io.yourorg.vlt`).

## Fix
- **Publish.** The user created github.com/heyjdp/vlt (public). `main` was pushed, CI and the release workflow were taken to green, and the macOS app and AppImage were downloaded and checked.
- **Rename.** The module path is now `github.com/heyjdp/vlt` (`go.mod`, every import, `.golangci.yml`). The app ID is now `io.github.heyjdp.vlt` (Makefile `APP_ID`, `cmd/vlt/main.go`, README paths).
- **Keep 0.1.0's settings.** `ui.MigratePreferences` copies the vault location, auto-lock timeout and clipboard TTL from 0.1.0's `…/fyne/io.yourorg.vlt/preferences.json` on the first start. It only does so while nothing is saved under the new ID, and leaves the old file in place.
- **No personal or local details in the repository.** Tests and docs now use example values: `192.0.2.x` (a documentation address range), user `me`, `example.com`. They no longer use the LAN server's address, the owner's user name, local paths or a work email.

## Acceptance criteria
- [x] CI green on GitHub (github.com/heyjdp/vlt, public, created by the user)
- [x] Release artifacts for macOS and Linux attached: [v0.1.0](https://github.com/heyjdp/vlt/releases/tag/v0.1.0) has `vlt-v0.1.0-macos-arm64.zip` and `vlt-v0.1.0-x86_64.AppImage` with SHA-256 files (checksums verified; the app is version 0.1.0, commit 06a4c7a, minimum macOS 13, ad-hoc signature valid)
- [x] Rename to `github.com/heyjdp/vlt` and `io.github.heyjdp.vlt`, carrying 0.1.0's settings over (`TestMigratePreferences_*`, `TestLegacyPrefsFile_IsTheOldAppIDsFolderNextToThisOne`); `TestAppID_MatchesTheMakefile` keeps the bundle ID and the settings folder in step

## Outcome
- **The first runs on GitHub failed in the vuln job:** "could not import C", because govulncheck type-checks Fyne's cgo packages. The job now installs the GL and X11 headers, as the others do.
- **The rename's migration could undo "auto-lock off".** Verification found it ran on every start, and auto-lock off is saved as 0, which looked unset. With `VLT_VAULT_DIR` set (so no location is ever saved), each start copied the old timeout back. It now runs once, recorded by a `migratedFrom010` marker (`TestMigratePreferences_RunsOnceSoLaterChangesStick`).
- **The first `v0.1.0` tag build failed in `test`.** `TestNewApp_KeepsTheVersion` left the real background runner going after the test, which raced under `-race` depending on test order. Fixed in 06a4c7a. The tag was moved there before anything was published.

## Related
- [[Roadmap]]
