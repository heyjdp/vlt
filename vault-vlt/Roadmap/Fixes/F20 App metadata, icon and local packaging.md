---
id: F20
title: "App metadata, icon and local packaging"
aliases:
  - F20
tags:
  - fix
severity: medium
status: todo
phase: 6
area: build
review_finding: Release goal
files:
  - FyneApp.toml
  - Icon.png
  - Makefile
depends_on: []
created: 2026-10-28
---

# F20: App metadata, icon and local packaging

## Problem
`make package-*` passes no app ID and the repository has no `Icon.png` or `FyneApp.toml`, so `fyne package` fails.

## Fix
- `FyneApp.toml` with the app name, ID, version and icon, so `fyne package` needs no flags.
- An original `Icon.png` (512×512 or larger).
- `make package-mac` builds `vlt.app` with a pinned `fyne` CLI. Windows packaging waits (not a target platform yet).

## Acceptance criteria
- [ ] `make package-mac` produces a `vlt.app` whose `Info.plist` has the app ID and version
- [ ] The app is never launched by automation

## Related
- [[Roadmap]]
