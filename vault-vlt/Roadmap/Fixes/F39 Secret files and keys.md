---
id: F39
title: "Secret files and keys"
aliases:
  - F39
tags:
  - fix
severity: medium
status: done
phase: 11
area: store
review_finding: "Architecture review 2026-11-11 (second pass), candidate C"
files:
  - internal/store/layout.go
  - internal/store/layout_test.go
  - internal/store/store.go
  - internal/store/store_test.go
  - internal/store/migrate.go
  - internal/store/export_test.go
  - internal/store/*_test.go
  - internal/ui/list.go
  - internal/ui/detail.go
  - internal/ui/fileops.go
  - internal/ui/flows_test.go
  - GLOSSARY.md
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F34
  - F38
created: 2026-11-11
---

# F39: Secret files and keys

## Problem
- **Three walkers decided what a secret file is.** `contents()` (listing), `CopyTo` and migration's `secretFiles` each walked the group folders with their own copy of the rule, and only `secretFiles` skipped a folder holding another vault (`hasVaultMarker`). Two live bugs followed: since [[F34 Tampered vault files]] and [[F38 Secret list errors]] the list counted a nested vault's `vlt.age` as "a secret file that couldn't be read", and moving the vault copied that `vlt.age` without its `vlt.json`.
- **The UI re-derived the store's path rule.** `sameSecret`, `matchGroup`, `Groups` and the search filter in `list.go` called `store.NormalizeGroup` and `store.Slugify` to decide whether two secrets share a file or two labels a folder.
- **Import was a four-step race**: `Create`, on `ErrSecretExists` a `Get` to learn what was there, a dialog, then `Update`. A file that couldn't be read made the `Get` fail with a raw decryption error.
- **`Store.List` had no production callers** (about 18 test calls; the UI uses `Contents`), and `store.New`/`Store.Init` were used only by tests.

## Fix
- **`groupFolders`** (new `layout.go`) is the one walker: it lists the top-level folders that can be groups (no dot-folders, no folder holding `vlt.json` or `vlt.migrating.age`) and the `.age` files in each, and keeps a folder's listing error with that folder. `Contents` names such a folder as unreadable, `CopyTo` and migration fail on it, as before. `secretFiles` (used by `Exists`, `Open` and migration) is built on it.
- **`SecretID`** (`Secret.ID()`) names the file a secret lives in, as a comparable value: names that differ only in case or punctuation, and differently spelled labels of one group, give equal IDs. **`GroupID`** (`GroupIDOf(label)`, `SecretID.Group()`) names a group's folder, and `GroupID.Contains(query)` matches a search against the folder spelling. The store builds paths from `SecretID`; the UI compares IDs and no longer calls `NormalizeGroup` or `Slugify`. Labels are still kept as typed.
- **`*ExistsError`**: `Create` (file taken) and `Update` (file held by another name) wrap it. It carries the secret already there, or `ReadErr` when that file can't be read, and matches `errors.Is(err, ErrSecretExists)`, so existing callers are unchanged. Import is now one `Create` and a confirm: same name asks, then `Update`; a differently named secret or an unreadable file is refused with the reason.
- **Removed `Store.List`**; its tests call `Contents` and check `Contents.Secrets` (renamed `TestStore_Contents_*`, `TestRemoteStore_Contents*`). **`store.New` and `Store.Init`** moved to `export_test.go`; the test of `Init` itself went with them.
- **Tests**: `TestStore_Contents_SkipsAVaultInsideTheVault` and `TestStore_CopyTo_LeavesAVaultInsideTheVaultBehind` (red before the walker); `TestSecret_ID_IsSharedByNamesAndGroupsThatMeanTheSame`, `TestSecret_ID_SameIDIsTheSameFile`, `TestGroupID_Contains_MatchesEitherSpellingOrPartOfTheFolderName`; `TestStore_Create_ExistsErrorCarriesTheSecretThere`, `TestStore_Create_ExistsErrorWithoutAReadableSecret`, `TestStore_Update_ExistsErrorCarriesTheSecretThere`; `TestFlow_ImportOverADifferentlyNamedSecretIsRefused` and `TestFlow_ImportOverAnUnreadableFileIsRefused` (the second red before `ExistsError`).

## Outcome
- Mutation checks: dropping the nested-vault skip fails both new tests (and the `Exists` ones); an ID that ignores the slug, or a `Contains` that matches an empty query, fails the ID tests; an `ExistsError` without `Existing`, without `ReadErr`, or that isn't `ErrSecretExists` fails the store tests.
- `CopyTo` still creates every group folder, empty ones included; a nested vault's folder is now left out entirely (Move never deletes the source, so nothing is lost).
- Nested vaults without `vlt.json` (format 1, or a copy that lost it) still can't be told from a group; `Contents` still names their `vlt.age` as unreadable, as the log and *Details…* explain.
- Kept: `NormalizeGroup` and `Slugify` stay exported, because `internal/testutil` writes legacy vault files by path.
- Older fix notes cite the old `TestStore_List_*` names; they describe the code as it was.
- Verification added `CopyTo` tests on the new walker: a group folder that can't be listed, a cancel mid-copy, and a file where a group folder belongs at the destination (`store_internal_test.go`).

## Acceptance criteria
- [x] The nested-vault tests fail without the shared walker
- [x] `TestFlow_ImportOverAnUnreadableFileIsRefused` fails without `ExistsError`
- [x] The UI no longer calls `store.NormalizeGroup` or `store.Slugify`
- [x] `errors.Is(err, store.ErrSecretExists)` still holds for every existing caller
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG, GLOSSARY, Storage Format, Unlock Flow

## Related
- [[Roadmap]]
- [[F34 Tampered vault files]]
- [[F38 Secret list errors]]
- [[Storage Format]]
