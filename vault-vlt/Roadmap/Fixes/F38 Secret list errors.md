---
id: F38
title: "Secret list errors"
aliases:
  - F38
tags:
  - fix
severity: medium
status: done
phase: 10
area: ui
review_finding: Go skills audit 2026-11-11, item 5
files:
  - internal/store/store.go
  - internal/store/store_tamper_test.go
  - internal/ui/list.go
  - internal/ui/list_test.go
  - internal/ui/helpers_test.go
  - internal/store/contents_internal_test.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F34
created: 2026-11-11
---

# F38: Secret list errors

## Problem
`store.List` deliberately fails on a lost connection "so the user sees why", but `SecretList.Refresh` only logged the error and returned: the list stayed empty, with no message. And since [[F34 Tampered vault files]], damaged, foreign and misplaced secret files are skipped with only a log warning, so a secret could vanish from the list unnoticed.

## Fix
- **`Store.Contents`** returns the readable secrets and the vault-relative paths it skipped (`Unreadable`, sorted): files it couldn't read, decrypt, parse or place, and group folders it couldn't read. `List` is now a thin wrapper. A lost connection still fails the listing, now also while reading a group folder.
- **The list panel** has a notice under the list:
  - a failed load shows "Couldn't load the vault: …" (via `userMessage`) and *Retry*, which reloads;
  - skipped files show "N secret files couldn't be read." ("… files or folders …" when a group folder is among them) and *Details…*, a dialog naming them and the likely reasons (damaged, from another vault, moved by hand or by a sync tool);
  - it hides when the vault loads cleanly.
- **Tests**: `Contents` names exactly the eight damaged files from F34's set, and a group folder it can't list (`home/`); a connection lost while listing a group fails the listing; a load error is shown with the reason and *Retry* clears it once fixed; two planted files are counted and named, without *Retry*; the notice wording (singular, plural, folders); no notice before the first load or on a clean load. `findShown` (in `helpers_test.go`) skips hidden containers, since `findDeep` walks into them.

## Outcome
Verification found:
- **Retry during a load lost a save.** Retry stayed enabled while its load ran; a second `Refresh` cleared the replay queue, so the first load to finish replayed the pending saves and the second then overwrote the list with what it had read before them. Before this change only unlock called `Refresh`, so loads never overlapped. Retry is now disabled while a load runs, with a queued-runner test.
- **Untested paths**: an unreadable group folder, a lost connection while listing one, the notice hidden before the first load, Retry hidden beside *Details…*, and the singular wording. All now tested; each removal fails a test.
- **A group folder was counted as a "secret file"**; the wording now says "files or folders" when one is among them.
- Smaller: `Unreadable` is sorted (the comment said directory order); the CHANGELOG puts the load error under Fixed and the skipped files with F34's Security entry; the test helpers moved to `helpers_test.go`.

Kept: `Store.List`, now a thin wrapper used by tests. The migration dialog and the notice both report files the migration skipped, which is fine: one is a one-time report, the other lasts.

## Acceptance criteria
- [x] `TestSecretList_ALoadErrorIsShownWithRetry`, `TestSecretList_SkippedFilesAreCountedWithDetails` and `TestSecretList_RetryWaitsForTheLoadInFlight` fail without the fix
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG, Security Model

## Related
- [[Roadmap]]
- [[F34 Tampered vault files]]
- [[F37 Lock read errors]]
