---
id: F21
title: "Release builds for macOS and Linux"
aliases:
  - F21
tags:
  - fix
severity: high
status: in-progress
phase: 6
area: ci
review_finding: Release goal
files:
  - .github/workflows/ci.yml
  - packaging/linux
depends_on: 
  - "[[F20 App metadata, icon and local packaging]]"
created: 2026-10-28
---

# F21: Release builds for macOS and Linux

## Problem
CI builds bare binaries only. There is no installable macOS app or Linux package.

## Fix
Package and release jobs in `.github/workflows/ci.yml`, run on pushes to `main` and on `v*` tags once lint, test and vuln pass:
- **macOS** (`package-macos`, `macos-15`): `make package-mac` → `vlt-<version>-macos-arm64.zip`, ad-hoc signed, for macOS 13+. It isn't notarised, because that needs an Apple Developer ID. On macOS 15+ the first launch needs System Settings → Privacy & Security → *Open Anyway*, or `xattr -dr com.apple.quarantine`.
- **Linux** (`package-linux`, `ubuntu-22.04` for glibc 2.35): `make package-linux` → `vlt-<version>-x86_64.AppImage`, with a pinned, checksum-verified `appimagetool` 1.9.1 and type2 runtime. The desktop entry is validated and the AppImage's contents are checked.
- **Artifacts**: both packages are uploaded to the run with SHA-256 files. On `v*` tags the `release` job publishes them as a GitHub release (it can be re-run). It is the only job with write permission.
- **No Go cache** in the package jobs, so a poisoned cache can't reach a release.

## Acceptance criteria
- [x] Workflow passes `actionlint` and `zizmor`; actions pinned to SHAs; least-privilege permissions
- [ ] The AppImage is assembled and checked in CI (desktop entry validated locally; the script passes `shellcheck`; pinned downloads' SHA-256 verified)
- [ ] First green run on GitHub ([[F22 Publish to GitHub]])

## Related
- [[Roadmap]]
