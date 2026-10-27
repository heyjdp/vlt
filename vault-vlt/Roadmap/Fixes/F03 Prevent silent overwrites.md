---
id: F03
title: Prevent silent overwrites
aliases:
  - F03
tags:
  - fix
  - store
severity: critical
status: todo
phase: 1
area: store
review_finding: "#3"
files:
  - internal/store/store.go
  - internal/store/secret.go
  - internal/ui/fileops.go
  - internal/ui/detail.go
depends_on: []
created: 2026-10-27
---

# F03: Prevent silent overwrites

## Problem
The file path is $\text{group}/\operatorname{slug}(\text{name}).\text{age}$, and `slug` isn't injective ([[Storage Format#Slug function]]):

- `GitHub Key`, `github-key` and `GitHub_Key` all map to `github-key.age`.
- Names with no ASCII alphanumerics (`日本`, `!!!`) map to `.age`.
- Creating a secret never checks whether the file already exists (`store.go:119`).
- Importing the same filename twice overwrites the first import (`fileops.go:111`).

`Save` only checks `Name == ""`, so it also skips `NewSecretName`'s 255-character limit.

## Fix
- Split the API into `Create(ctx, *Secret)` and `Update(ctx, *Secret)`.
  - `Create` opens the final path with `O_CREATE|O_EXCL` semantics (or checks inside the atomic-write helper) and returns `ErrSecretExists`.
  - `Update` requires the file to exist **and** the decrypted name to match exactly.
- Reject names whose slug is empty, with a clear error.
- Validate with `NewSecretName` in `Save`/`Create`.
- In the UI, show "A secret with that name already exists in *group*" and offer to rename.

## Acceptance criteria
- [ ] Creating `github-key` when `GitHub Key` exists in the same group fails with `ErrSecretExists`
- [ ] Creating a secret named `!!!` fails validation
- [ ] Importing the same file twice prompts instead of overwriting
- [ ] Table-driven test for slug collisions

## Related
- [[F02 Atomic file writes]]
