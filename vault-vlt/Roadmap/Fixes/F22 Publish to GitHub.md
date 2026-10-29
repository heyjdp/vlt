---
id: F22
title: "Publish to GitHub"
aliases:
  - F22
tags:
  - fix
severity: high
status: in-progress
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
The repository has no remote, so CI and the release workflow have never run, and the module path and app ID still use the `yourorg` placeholder.

## Fix
- Create the GitHub repository (private) under the owner's account.
- Rename the module path to `github.com/<owner>/vlt` and the app ID to `io.github.<owner>.vlt` (Makefile and `cmd/vlt/main.go`; README paths).
- Push `main`, watch CI and the release workflow to green, and download the macOS app and AppImage.

## Acceptance criteria
- [x] CI green on GitHub (github.com/heyjdp/vlt, public, created by the user)
- [x] Release artifacts for macOS and Linux attached: [v0.1.0](https://github.com/heyjdp/vlt/releases/tag/v0.1.0) has `vlt-v0.1.0-macos-arm64.zip` and `vlt-v0.1.0-x86_64.AppImage` with SHA-256 files (checksums verified; the app is version 0.1.0, commit 06a4c7a, minimum macOS 13, ad-hoc signature valid)
- [ ] Rename `github.com/yourorg/vlt` and `io.yourorg.vlt` (the app ID moves the settings file, so 0.1.0 users would lose their saved location and preferences)

## Outcome
- **The first runs on GitHub failed in the vuln job:** "could not import C", because govulncheck type-checks Fyne's cgo packages. The job now installs the GL and X11 headers, as the others do.
- **The first `v0.1.0` tag build failed in `test`.** `TestNewApp_KeepsTheVersion` left the real background runner going after the test, which raced under `-race` depending on test order. Fixed in 06a4c7a. The tag was moved there before anything was published.

## Related
- [[Roadmap]]
