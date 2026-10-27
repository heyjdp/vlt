---
id: F22
title: "Publish to GitHub"
aliases:
  - F22
tags:
  - fix
severity: high
status: todo
phase: 6
area: ci
review_finding: Release goal
files:
  - go.mod
  - FyneApp.toml
depends_on: 
  - "[[F21 Release builds for macOS and Linux]]"
created: 2026-10-28
---

# F22: Publish to GitHub

## Problem
The repository has no remote, so CI and the release workflow have never run, and the module path and app ID still use the `yourorg` placeholder.

## Fix
- Create the GitHub repository (private) under the owner's account.
- Rename the module path to `github.com/<owner>/vlt` and the app ID to `io.github.<owner>.vlt`.
- Push `main`, watch CI and the release workflow to green, and download the macOS app and AppImage.

## Acceptance criteria
- [ ] CI green on GitHub
- [ ] Release artifacts for macOS and Linux attached to the run

## Related
- [[Roadmap]]
