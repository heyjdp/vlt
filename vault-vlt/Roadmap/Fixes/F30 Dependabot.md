---
id: F30
title: "Dependabot"
aliases:
  - F30
tags:
  - fix
severity: low
status: done
phase: 8
area: ci
review_finding: User request (2026-11-01)
files:
  - .github/dependabot.yml
  - .github/workflows/ci.yml
  - README.md
  - CHANGELOG.md
depends_on: []
created: 2026-11-01
---

# F30: Dependabot

## Problem
Nothing proposed dependency updates. Fixes in age, Fyne, `x/crypto`, `pkg/sftp` and the pinned actions arrived only when someone remembered to look. `govulncheck` ran only when something was pushed or a pull request opened, so a vulnerability published while nothing was pushed went unreported.

## Fix
- **`.github/dependabot.yml`** proposes updates every Monday, each a pull request that CI checks:
  - Go modules: minor and patch updates grouped, each major update on its own.
  - GitHub Actions: all grouped. Dependabot keeps the SHA pins and their version comments.
  - A 7-day `cooldown`, so a release is a week old before it is proposed. Security updates are not delayed.
- **CI** also runs every Monday, so `govulncheck` reports new advisories without a push.
- **README *Dependency updates*:**
  - what Dependabot does;
  - that security updates need *Dependabot alerts* and *security updates* turned on in the repository settings;
  - which pins stay manual (tool versions in `ci.yml`, the Go toolchain, the `appimagetool` and runtime versions and checksums).

## Acceptance criteria
- [x] `actionlint` and `zizmor` report no findings
- [x] README documents the setup and its limits

## Outcome
Verification found and fixed:
- **The first Go update would fail on Linux.** A simulated Dependabot PR moved Fyne from v2.7.3 to v2.8.2. That release moves to GLFW 3.4, which also builds a Wayland backend and needs `libwayland-dev`, which none of the CI jobs installed. Every apt line in `ci.yml` and the README's Linux requirements now include it. The update builds, tests, lints and scans clean on macOS. Once it lands, the AppImage will also need the system's libwayland libraries, which desktop systems have.
- **A scheduled run could replace a waiting push run on `main`.** GitHub keeps one pending run per concurrency group. The group now includes the event name.
- **Small fixes:**
  - a stale comment about which runs skip packaging;
  - the 60-day rule for scheduled workflows, now in the README;
  - commit prefixes changed to conventional `build(deps)` and `ci(deps)`;
  - a CHANGELOG *Security* entry.
- **Confirmed:**
  - The file validates against the SchemaStore schema.
  - Cooldown does not delay security updates.
  - Dependabot rewrites SHA pins together with their version comments, and every pin matches its tag and is current.
  - Dependabot does not touch the `go`/`toolchain` lines.
  - Dependabot PRs run lint, test, vuln and build with a read-only token, and no secrets are needed.
  - A scheduled run publishes nothing.

## Related
- [[Roadmap]]
- [[F29 Check for updates]]
- [[F31 Release checklist]]
