---
id: F19
title: "Follow XDG_DATA_HOME on Linux"
aliases:
  - F19
tags:
  - fix
severity: low
status: todo
phase: 6
area: config
review_finding: Reinstall verification
files:
  - cmd/vlt/main.go
depends_on: []
created: 2026-10-28
---

# F19: Follow XDG_DATA_HOME on Linux

## Problem
`defaultVaultDir` hard-codes `~/.local/share/vlt/vault` on Linux and the BSDs, ignoring `XDG_DATA_HOME`.

## Fix
Use `$XDG_DATA_HOME/vlt/vault` when `XDG_DATA_HOME` is an absolute path, otherwise `~/.local/share/vlt/vault` (per the XDG Base Directory spec).

## Acceptance criteria
- [ ] Table test over GOOS and environment
- [ ] `go test -race ./...` passes

## Related
- [[Roadmap]]
