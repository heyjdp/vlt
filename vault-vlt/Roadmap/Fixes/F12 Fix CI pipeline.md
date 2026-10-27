---
id: F12
title: Fix CI pipeline
aliases:
  - F12
tags:
  - fix
  - ci
severity: medium
status: done
phase: 4
area: ci
review_finding: CI / build
files:
  - .github/workflows/ci.yml
  - .golangci.yml
  - .lefthook.yml
  - Makefile
  - go.mod
  - go.sum
  - cmd/vlt/main.go
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

## As shipped
- `ci.yml` has four jobs: **lint** (golangci-lint v2.15.0, which also checks formatting), **test** (`go vet` and `go test -race`), **vuln** (govulncheck v1.9.0), and **build** (native Linux, macOS and Windows runners with cgo).
- `setup-go` reads the Go version from `go.mod`, including the toolchain directive.
- Actions are pinned to commit SHAs (checkout v7.0.1, setup-go v7.0.0, golangci-lint-action v9.3.0), with `persist-credentials: false`, read-only permissions, and PR-only cancellation.
- `.golangci.yml` is migrated to v2 with `linters.default: standard` plus eight correctness and security linters. The spec suggested `all`, which is mostly stylistic and not maintainable as a gate.
  - govet `shadow` and `fieldalignment` are disabled.
  - Narrow gosec exclusions apply to vault paths and the secret envelope.
  - There's no `std-error-handling` preset, so unchecked `Close` calls are reported.

## Extra scope
- govulncheck found 13 vulnerabilities reachable from vlt's code. Fixed through `go get`:
  - `toolchain go1.27.3`, which patches the standard library
  - `golang.org/x/image` v0.45.0
  - fixes for unreachable advisories in `x/net`, `x/crypto`, `x/text` and `goldmark`
  
  0 are reachable now. The one remaining module advisory (GO-2026-5932, `x/crypto/openpgp`) has no fix, and vlt doesn't import that package.
- golangci-lint before v2.14 can't read Go 1.27.3's export data, hence the v2.15.0 pin.
- `main` became `run() error`, so the deferred `memguard.Purge()` always runs (gocritic `exitAfterDefer`).
- `atomic.go` now wraps the link error with `%w` (errorlint), so `errors.Is` can match it as well as the fallback error.
- `make fmt` and the lefthook pre-commit hook use `golangci-lint fmt`.
- The README and DESIGN cross-compilation instructions, which can't work with cgo, were replaced with native builds and `fyne-cross`.

## Acceptance criteria
- [x] `golangci-lint config verify` passes
- [ ] CI is green on all three OSes. **Not verifiable locally**: the repository has no remote. Every job was simulated locally (lint, vet, `-race` tests, govulncheck, a native macOS build), actionlint and zizmor are clean, and verification confirmed the Windows runner image has MinGW gcc on PATH. The first push will confirm.
- [x] The lint step runs. It fails on any issue by default; this will be shown on the first CI run.

## Related
- [[F13 Project hygiene]] · [[Architecture Overview#Dependencies]]
