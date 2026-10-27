---
id: F12
title: Fix CI pipeline
aliases:
  - F12
tags:
  - fix
  - ci
severity: medium
status: todo
phase: 4
area: ci
review_finding: CI / build
files:
  - .github/workflows/ci.yml
  - .golangci.yml
  - Makefile
depends_on: []
created: 2026-10-27
---

# F12: Fix CI pipeline

## Problem
| Issue | Where |
|---|---|
| CI pins Go `1.23`, but `go.mod` declares `go 1.26.1` | `ci.yml` (`setup-go`) |
| The build matrix sets `CGO_ENABLED=0`, but Fyne's desktop driver needs cgo (go-gl/glfw), so all three targets fail | `ci.yml` (`build-matrix`) |
| No lint step, even though `.golangci.yml` exists | `ci.yml` |
| `.golangci.yml` is invalid for v2: `enable-all`, `linters-settings`, `issues.exclude-rules` | `.golangci.yml` |
| Tools installed `@latest` with no pinned versions | `ci.yml` |

## Fix
- `actions/setup-go` with `go-version-file: go.mod`.
- Build each OS on its native runner (`ubuntu-latest`, `macos-latest`, `windows-latest`) with cgo on, or use `fyne-cross` for release builds.
- Add `golangci/golangci-lint-action` with a pinned version.
- Migrate the config with `golangci-lint migrate`, which maps to `linters.default: all`, `linters.settings`, `linters.exclusions.rules`, and a `formatters` section for gofumpt/goimports.
- Pin action SHAs (zizmor will flag unpinned ones) and tool versions.

## Acceptance criteria
- [ ] `golangci-lint config verify` passes
- [ ] CI is green on all three OSes
- [ ] The lint step runs and fails on new issues

## Related
- [[F13 Project hygiene]] · [[Architecture Overview#Dependencies]]
