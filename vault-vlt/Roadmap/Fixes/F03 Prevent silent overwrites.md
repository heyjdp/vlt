---
id: F03
title: Prevent silent overwrites
aliases:
  - F03
tags:
  - fix
  - store
severity: critical
status: done
phase: 1
area: store
review_finding: "#3"
files:
  - internal/store/store.go
  - internal/store/secret.go
  - internal/store/atomic.go
  - internal/store/store_test.go
  - internal/store/create_test.go
  - internal/store/secret_test.go
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

## Fix (as shipped)
- `Save` is replaced by:
  - `Create`, which validates with `NewSecretName` and writes exclusively (temp file + `link`; `O_EXCL` fallback on filesystems without hard links). It returns `ErrSecretExists` if the path is taken.
  - `Update`, which requires the stored name to match after trimming and preserves `CreatedAt`.
- `NewSecretName` rejects names with no ASCII letter or digit, and slugs over 251 bytes.
- Legacy secrets with untrimmed or punctuation-only names (stored at `<group>/.age`) can still be read, updated and deleted, through the lenient `lookupName`.
- UI changes:
  - New secrets go through `Create` and edits through `Update`.
  - Edits are applied to a copy and committed only on success.
  - After creating, the form re-renders as an existing secret.
  - Importing a duplicate asks before replacing and names the existing secret's type. A slug collision with a different name tells the user to rename.

## Acceptance criteria
- [x] Creating `github-key` when `GitHub Key` exists fails with `ErrSecretExists`: `TestStore_Create_RefusesExistingSecret` (table of collisions), `TestStore_Create_ConcurrentSameNameExactlyOneWins`
- [x] Creating `!!!` fails validation: `TestNewSecretName_Validation`, `TestStore_Create_RejectsEmptyName`
- [x] Importing the same file twice prompts instead of overwriting. Store-level sequence: `TestStore_ImportTwice_CreateRefusesThenUpdateReplaces`. The dialog itself is left for UI tests in [[F15 UI tests with fyne test]].
- [x] Table-driven test for slug collisions, plus `TestStore_Update_RefusesSlugCollision`

## Outcome
Shipped on `fix/review-roadmap` after independent code, docs, and tooling verification (all six test mutations were caught). Verification also found and fixed three regressions for legacy data: untrimmed names, empty-slug names, and filesystems without hard links.

## Related
- [[F02 Atomic file writes]]
