---
id: F13
title: Project hygiene
aliases:
  - F13
tags:
  - fix
  - docs
severity: low
status: todo
phase: 4
area: docs
review_finding: CI / build
files:
  - go.mod
  - cmd/vlt/main.go
  - .gitignore
  - README.md
depends_on: []
created: 2026-10-27
---

# F13: Project hygiene

## Checklist
- [ ] Add `LICENSE`. The README says MIT (uncommitted change), but there's no license file.
- [ ] Add `CHANGELOG.md` in Keep a Changelog format (required by `CLAUDE.md` §6.2)
- [ ] Replace the `github.com/yourorg/vlt` module path and the `io.yourorg.vlt` app ID (migrate saved preferences from the old app ID; see [[F06 Persist settings]])
- [x] Add `claude.resume` to `.gitignore` (already present)
- [x] Format the tree (done with `golangci-lint fmt` in [[F12 Fix CI pipeline]]; CI now enforces it)
- [ ] Embed the version with `-X main.version=$(git describe …)` in the Makefile `LDFLAGS`
- [x] Run `govulncheck` (done in [[F12 Fix CI pipeline]]: 0 reachable vulnerabilities; CI runs it)

## Related
- [[F12 Fix CI pipeline]]
