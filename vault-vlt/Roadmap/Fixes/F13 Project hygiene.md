---
id: F13
title: Project hygiene
aliases:
  - F13
tags:
  - fix
  - docs
severity: low
status: done
phase: 4
area: docs
review_finding: CI / build
files:
  - LICENSE
  - CHANGELOG.md
  - Makefile
  - .github/workflows/ci.yml
  - cmd/vlt/main.go
  - .gitignore
  - README.md
depends_on: []
created: 2026-10-27
---

# F13: Project hygiene

## Checklist
- [x] Add `LICENSE` (MIT, © 2026 Jas). The README now says "MIT — see LICENSE".
- [x] Add `CHANGELOG.md` in Keep a Changelog format (required by `CLAUDE.md` §6.2)
- [x] Replace the `github.com/yourorg/vlt` module path and the `io.yourorg.vlt` app ID: deferred to the owner (see Outcome), then done in [[F22 Publish to GitHub]] as `github.com/heyjdp/vlt` and `io.github.heyjdp.vlt`, with 0.1.0's settings carried over
- [x] Add `claude.resume` to `.gitignore` (already present)
- [x] Format the tree (done with `golangci-lint fmt` in [[F12 Fix CI pipeline]]; CI now enforces it)
- [x] Embed the version with `-X main.version=$(git describe …)` in the Makefile `LDFLAGS` (and the CI build, with full history so tags are visible)
- [x] Run `govulncheck` (done in [[F12 Fix CI pipeline]]: 0 reachable vulnerabilities; CI runs it)

## Outcome
`LICENSE` and `CHANGELOG.md` were added. The changelog uses Keep a Changelog categories, with all of this branch under *Unreleased*. `make build` and the CI build embed `git describe --tags --always --dirty` into `main.version`; plain `go build` gives "dev".

The module path and app ID are **deliberately not renamed**:
- The real organisation or domain is the owner's choice, and the repository has no git remote to infer it from.
- Renaming the module touches every import.
- Renaming the app ID moves Fyne's saved preferences, orphaning the settings [[F06 Persist settings]] now saves unless they're migrated. That migration should land in the same change.

The README flagged both as placeholders until [[F22 Publish to GitHub]] renamed them.

## Related
- [[F12 Fix CI pipeline]]
