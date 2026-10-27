---
id: F18
title: "Backup and uninstall guidance"
aliases:
  - F18
tags:
  - fix
severity: medium
status: done
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
A **Your Data** section in the README: vault and settings locations per OS, what survives an uninstall and a reinstall, back up the whole folder (`vlt.age` is the key), the cleaner-app warning, and how to reopen a vault in another folder.

## Acceptance criteria
- [x] Locations match `vaultDirFor` and Fyne's preferences paths (checked against Fyne v2.7.3)
- [x] Verified by a documentation review (accuracy, style and rendering)

## Outcome
Verification corrected several claims:
- **Settings file contents.** It also holds Fyne's last file-dialog folder, which can be where a decrypted file was exported.
- **Backups.** They hide secret contents but not names: file and folder names show group and secret names.
- **Restoring.** *Open Existing Vault…* appears only on the create screen. With a vault already open, the route is *Switch Vault (no copy)*.
- **Stale copies.** An old copy left at the default location by *Move Vault* is opened instead of the moved vault.

Added:
- Exported files are plaintext copies outside the vault.
- How to remove vlt completely.
- Changing the passphrase keeps the vault key, so destroy older backups after a leak.

Code fix found on the way: a partial backup without `vlt.age` *and* `vlt.json` reported "Incorrect passphrase". `Open` now reports the missing key when no probed file is passphrase-encrypted (`TestStore_Open_ReportsMissingKeyWhenTheFormatMarkerIsGoneToo`).

## Related
- [[Roadmap]]
