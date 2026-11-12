---
id: F48
title: "nolintlint"
aliases:
  - F48
tags:
  - fix
severity: low
status: done
phase: 12
area: tooling
review_finding: Go skills audit 2026-11-11, item 11
files:
  - .golangci.yml
  - internal/totp/totp.go
  - internal/store/fs.go
  - internal/store/layout.go
  - internal/store/sshfs.go
  - internal/store/vault.go
  - README.md
depends_on: []
created: 2026-11-12
---

# F48: nolintlint

## Problem
- **Nothing checked the `//nolint` directives.** A suppression could be bare (`//nolint`, hiding every linter), give no reason, or outlive the finding it hid, and lint stayed green.
- **Nine were dead.** `internal/totp/totp.go` had `//nolint:gosec` for G115 (an integer conversion), which `.golangci.yml` already excludes everywhere. Eight `//nolint:wrapcheck` in `internal/store` (`fs.go` ×5, `layout.go`, `sshfs.go`, `vault.go` ×2) named a linter that isn't enabled.

## Fix
- **`nolintlint` is enabled** with `require-specific`, `require-explanation` and `allow-unused: false`: every directive names its linter and says why, and one that suppresses nothing is reported.
- **The dead directives are gone.** Their reasons stay as plain comments (`// *PathError already names the file`), since they still explain why the error is returned unwrapped. The remaining eight directives (gosec ×5, errorlint ×2, nilerr ×1) each still suppress a real finding and keep their reasons.
- **Decision: the wrapcheck ones were removed by hand.** `nolintlint` only reports an unused directive for an enabled linter, so it never flagged them; `.golangci.yml` and the README say to name only enabled linters.

## Outcome
The config verifier confirmed every removed directive was dead (G115 is excluded globally; wrapcheck is not enabled) and that the eight that remain each still suppress a real finding. Nothing here depends on a CI run. One caveat: golangci-lint runs on Linux, so a `//nolint` in an `internal/store/*_windows.go` file would not be checked (there are none today); README now says those files aren't linted.

## Acceptance criteria
- [x] The pinned golangci-lint (v2.15.0) reports 0 issues with `nolintlint` enabled
- [x] No `//nolint:wrapcheck` and no G115 `//nolint:gosec` left
- [x] `go test -race ./...` passes
- [x] README's linter section

## Related
- [[Roadmap]]
- [[F46 Tests on every OS]]
- [[F47 Workflow linters]]
