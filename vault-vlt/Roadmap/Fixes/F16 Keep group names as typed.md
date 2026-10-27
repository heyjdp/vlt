---
id: F16
title: "Keep group names as typed"
aliases:
  - F16
tags:
  - fix
severity: low
status: done
phase: 6
area: store
review_finding: Reinstall verification
files:
  - internal/store/secret.go
  - internal/store/store.go
  - internal/ui/list.go
  - internal/ui/detail.go
  - internal/ui/fileops.go
depends_on: []
created: 2026-10-28
---

# F16: Keep group names as typed

## Problem
`Store.seal` replaced the group with its slug (`internal/store/store.go`, `NormalizeGroup`), so a secret filed under "Personal Finance" was stored and shown as "personal-finance".

## Impact
Every user who types a group with capitals or spaces sees it rewritten on save.

## Fix
- Store the trimmed group as typed (`store.GroupLabel`); keep the slug only for the directory (`NormalizeGroup`).
- `SecretList.Groups` lists one label per directory, sorted ignoring case.
- New secrets and imports reuse the existing spelling of a group with the same directory (`matchGroup`).
- Groups saved by earlier versions keep their slug as the label, and new secrets in them reuse it. An existing secret's group can't be edited, so there is no in-app rename.
- Labels with no ASCII letter or digit become `default`.

## Acceptance criteria
- [x] `TestStore_GroupIsKeptAsTyped`, `TestGroupLabel`
- [x] `TestSecretList_GroupsShowOneLabelPerDirectory`, `TestMatchGroup` (including a group saved by an earlier version), `TestDetailView_NewSecretUsesTheExistingGroupSpelling`, `TestFlow_ImportUsesTheExistingGroupSpelling`
- [x] `go test -race ./...` passes

## Outcome
Verification found and fixed:
- Search matched only the stored spelling, so `personal-finance` no longer found `Personal Finance`. It now also matches by directory (`TestSecretList_SearchMatchesGroupBySpellingOrDirectory`).
- The "already exists in group" error still showed the slug.
- No test covered imports reusing the existing spelling; a mutation that dropped it survived. The new flow test kills it.
- Stale docs: DESIGN.md still described a by-type layout, and the README test and tree descriptions mentioned a removed group listing.

Not done: group names have no length limit, so a very long group fails with the OS's error when its directory is created.

## Related
- [[Roadmap]]
