---
id: F18
title: "Backup and uninstall guidance"
aliases:
  - F18
tags:
  - fix
severity: medium
status: todo
phase: 6
area: docs
review_finding: Reinstall verification
files:
  - README.md
depends_on: 
  - "[[F17 Open an existing vault from the create screen]]"
created: 2026-10-28
---

# F18: Backup and uninstall guidance

## Problem
The README doesn't say where the vault lives on each OS, that uninstalling keeps it, what to back up, or that cleaner apps can delete it.

## Impact
Users can lose every secret by deleting `vlt.age` or letting an uninstaller remove `~/Library/Application Support/vlt`.

## Fix
A **Your data** section in the README: vault and settings locations per OS, what survives an uninstall and a reinstall, back up the whole folder (`vlt.age` is the key), the cleaner-app warning, and how to reopen a vault in another folder.

## Acceptance criteria
- [ ] Locations match `defaultVaultDir` and Fyne's preferences paths
- [ ] Verified by a documentation review

## Related
- [[Roadmap]]
