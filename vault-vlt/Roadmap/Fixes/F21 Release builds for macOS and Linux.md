---
id: F21
title: "Release builds for macOS and Linux"
aliases:
  - F21
tags:
  - fix
severity: high
status: todo
phase: 6
area: ci
review_finding: Release goal
files:
  - .github/workflows/release.yml
  - packaging/linux
depends_on: 
  - "[[F20 App metadata, icon and local packaging]]"
created: 2026-10-28
---

# F21: Release builds for macOS and Linux

## Problem
CI builds bare binaries only. There is no installable macOS app or Linux package.

## Fix
A release workflow that runs after CI passes on `main` (and on version tags):
- **macOS:** `vlt.app` for Apple silicon, ad-hoc signed, zipped. Without an Apple Developer ID it isn't notarised, so the first launch needs *Open* from the context menu or System Settings → Privacy & Security.
- **Linux:** `vlt-x86_64.AppImage`, built on the oldest supported Ubuntu runner for glibc compatibility, with a pinned, checksum-verified `appimagetool`.
- Artifacts uploaded to the workflow run; a GitHub release with both files on `v*` tags.
- SHA-256 checksums published beside them.

## Acceptance criteria
- [ ] Workflow passes `actionlint`; actions pinned to SHAs; least-privilege permissions
- [ ] The AppImage's AppDir is assembled and checked locally (desktop file valid)
- [ ] First green run on GitHub ([[F22 Publish to GitHub]])

## Related
- [[Roadmap]]
