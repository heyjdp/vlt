---
id: F20
title: "App metadata, icon and local packaging"
aliases:
  - F20
tags:
  - fix
severity: medium
status: done
phase: 6
area: build
review_finding: Release goal
files:
  - Icon.png
  - Makefile
  - packaging/macos/build-app.sh
  - packaging/macos/Info.plist
depends_on: []
created: 2026-10-28
---

# F20: App metadata, icon and local packaging

## Problem
`make package-*` passes no app ID and the repository has no `Icon.png` or `FyneApp.toml`, so `fyne package` fails.

## Fix
- An original `Icon.png` (1024×1024, a safe door with a combination dial) and a 256px copy for Linux.
- `make package-mac` runs `packaging/macos/build-app.sh`, which builds `vlt.app` from an `Info.plist` template using only tools that ship with macOS (`sips`, `iconutil`, `plutil`, `codesign`, `ditto`), ad-hoc signs it and zips it.
- On a Mac, the Makefile sets the deployment target to macOS 13, Go 1.27's minimum.
- Windows packaging waits; it isn't a target platform yet.

## Acceptance criteria
- [x] `make package-mac` produces a `vlt.app` whose `Info.plist` has the app ID and version, and whose binary's `minos` matches `LSMinimumSystemVersion` (13.0)
- [x] The app is never launched by automation

## Outcome
The first version used the `fyne` CLI (v1.7.3, pinned) and `FyneApp.toml`. Verification found three problems:
- **The binary needed the build machine's macOS.** cgo's linker targeted macOS 26 locally (15 on CI), so the app would refuse to start on older Macs while `Info.plist` claimed 10.11 (and then 12). Go 1.27 actually needs macOS 13. The fix sets `MACOSX_DEPLOYMENT_TARGET` and `CGO_CFLAGS`/`CGO_LDFLAGS` (which also key the build cache), and CI checks `minos` against the plist.
- **`fyne package` dirtied the repository.** It rewrote `FyneApp.toml`'s build number on every run, and it refused to run from a scratch directory without Go code.
- **The App Store category was empty.**

Replacing it with a 50-line script removed the CLI pin and `FyneApp.toml`.

## Related
- [[Roadmap]]
