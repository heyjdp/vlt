---
id: F36
title: "Private exports"
aliases:
  - F36
tags:
  - fix
severity: medium
status: done
phase: 10
area: ui
review_finding: Go skills audit 2026-11-11, item 3
files:
  - internal/ui/fileops.go
  - internal/ui/flows_test.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on: []
created: 2026-11-11
---

# F36: Private exports

## Problem
The vault's files are 0600, but an exported file was not. Fyne's save dialog creates the file with `os.Create` (0666 less the umask, usually 0644) before vlt sees it, so an exported SSH or TLS private key was readable by every user of the computer, and ssh refuses to use it. A `chmod` alone would leave a gap: another user could open the empty file before the chmod, and read the export later through that handle.

## Fix
- **`FileOps.writePrivateExport`**: on Unix, a local file (`file` scheme) is written to a new temp file in the same folder (`os.CreateTemp`, mode 0600, exclusive), fsynced, then renamed over the path. A handle on the old file never sees the export, a planted file or symlink is replaced rather than written through, and a failed export removes only the temp file. The data is wiped on every path.
- **A folder that takes no new files** (the user can write the file but not the folder): the file the dialog created, and already emptied, is made 0600 and written in place. If it can't be made 0600 (it may be another user's), nothing is written. Any other failure to create the temp file fails the export.
- **After the rename** the folder is fsynced (best effort), as vault writes do.
- **Windows** writes in place, as before: Go's file mode there is only the read-only flag, and the folder's ACL decides who can read. Other schemes (Android, iOS) are written as given.
- **Tests**: the export has mode 0600; a handle opened on the existing file before the export reads none of the secret; a symlink at the path is replaced by a regular 0600 file and its target never receives the secret; in a read-only folder the export is written in place with mode 0600 and no temp file is left. All but the first are skipped on Windows; the read-only test also when running as root.

## Outcome
The first version removed the file and recreated it exclusively with mode 0600. Verification found:
- **A regression in folders the user can't add files to:** the dialog's `os.Create` had already emptied the file, the remove failed, and the export was lost. Now handled by writing in place.
- **A gap between remove and create:** another user of a shared folder could plant a file there; nothing leaked, but the export failed with a misleading message. The temp file and rename close the gap.
- **Windows:** removing a file another program has open fails there, and modes don't apply; Windows now keeps the in-place write, and the handle test is skipped.
- **Fyne follows a symlink:** the save dialog's `os.Create` empties a symlink's target before vlt sees the file. vlt can't prevent that, but never writes the secret through the link.
- **Docs:** "readable only by you" held only on Unix (README, CHANGELOG now say macOS, Linux and OpenBSD, and name Windows and FAT/exFAT/SMB drives); DESIGN's file-modes bullet now covers exports.

A check of the reworked version confirmed every error path wipes the data and removes the temp file, and that the path is only deleted when the rename didn't happen. It found the in-place fallback wrote the secret even when chmod failed (perhaps into another user's file) and ran on any `CreateTemp` error (a full disk, too many open files); it now runs only on a permission error and only after a successful chmod. The folder wasn't fsynced after the rename, and a crash can leave a hidden temp file; both are now handled or documented. Failed writes, syncs and renames are checked by reading the code, not by tests.

Not checked: Flatpak builds, where the save portal returns a path inside the document portal's mount; vlt doesn't ship one.

## Acceptance criteria
- [x] The tests fail on the old code (the mode test under the usual umask 022) and pass now
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG, Security Model

## Related
- [[Roadmap]]
- [[Security Model]]
