---
id: F41
title: "Operations"
aliases:
  - F41
tags:
  - fix
severity: medium
status: done
phase: 11
area: ui
review_finding: "Architecture review 2026-11-11 (second pass), candidate B"
files:
  - internal/ui/operations.go
  - internal/ui/operations_test.go
  - internal/ui/picker.go
  - internal/ui/activity.go
  - internal/ui/app.go
  - internal/ui/detail.go
  - internal/ui/fileops.go
  - internal/ui/settings.go
  - internal/ui/updates.go
  - internal/ui/*_test.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F40
created: 2026-11-11
---

# F41: Operations

## Problem
- **Every caller re-assembled "operation with feedback".** Saving, deleting, importing, exporting, changing the passphrase, moving the vault and checking for updates each wired up the same steps by hand: `background` bound to the session, status text or a progress dialog, a re-entry guard, an error dialog, a confirmation dialog, and a call to `activity()` from every dialog callback (about nine of them) so the auto-lock saw the answer.
- **Four re-entry policies, and two holes.** `DetailView.busy` refused a second save or delete; Change Passphrase disabled its button; the update check had its own flag; the list disabled Retry. *Move Vault* and *Switch Vault* had none: a second Move could start a second copy into the same folder while the first ran, and Switch (which locks at once) or a passphrase change could cut into a copy, leaving a half-copied vault or a copy with the old key.
- **Pickers tests couldn't drive.** Fyne's file dialogs can't be answered headless, so export tests called the private `saveExport(writer, nil, …, fo.pending)` and a hygiene test called `askExportPath`, testing past the interface.
- **The export's plaintext was wiped in about ten places** across five functions: the dialog callback's four exits, `dropPending` on lock (through `DetailView.clear`), both writers and their error paths.

## Fix
- **`operations.go`**: one module per session (and one for the app's own update checks) with a small interface:
  - `start(ops, &operation{kind, doing, progress, title, control, failed}, work, done)` runs work through `background` (a result after a lock is dropped), shows progress in the status bar or, with a title, a dialog, disables the control that started it, refuses an operation whose `opKind` is already running ("Please wait for the current operation to finish" in the status bar), and shows a failure with `showError(doing, err)` unless `failed` shows it itself.
  - `now(op, f)` applies the same refusal to an action with nothing to wait for (a switch).
  - `confirm(&question{…}, yes)` wraps `ShowConfirm`/`ShowCustomConfirm`; the answer, and input in the dialog's content (the import's group form, now a custom confirm; the tracker learned `*widget.Form`), counts as activity.
  - `pickFile`, `pickFolder`, `pickSave(name, doing, use)` go through the picker seam, count the answer as activity, ignore a cancel, show a failure, and drop an answer that arrives after the session ended, closing the chosen file. `pickSave` answers exactly once, with nil when nothing is to be written; `end` (called when the session is dropped) answers save requests still open, since the lock dismisses dialogs without an answer.
- **The re-entry policy**: per kind. `opSecretChange` (save, delete), `opVault` (change passphrase, move, switch), `opUpdateCheck`. Operations without a kind (imports, export decode and write) may overlap, as before. Because the kinds live in the session's operations, the guard also holds across a Settings screen opened again.
- **`picker.go`**: the `picker` interface (`openFile`, `openFolder`, `saveFile`) with two adapters: `fynePicker` (Fyne's dialogs, with the suggested name and extension filter) and the tests' `scriptedPicker`, whose requests the test answers or leaves open.
- **Export**: `exportDecoded` owns the decoded file and is the only place it is wiped (cancel, chooser failure, lock with the dialog open, failed or finished write); `writePrivateExport` and `writeExport` no longer wipe. The F36 private write (0600 temp file renamed into place) is unchanged.
- **Callers migrated**: DetailView save, delete and its confirmation; FileOps import (chooser, group form, create, replace confirmation and update) and export; Settings change passphrase, move, switch and *Browse…*; Open Existing Vault's folder chooser; the manual update check and the release dialog. The `activity` fields of DetailView, FileOps and Settings, `DetailView.busy`, `App.checkingUpdates`, `FileOps.pending`/`dropPending`/`askExportPath`/`saveExport` and `noActivity` are gone.
- **Left on `background`, deliberately**: the list's load (Retry), which must keep its own in-flight state to replay saves and deletes made meanwhile; and the pre-session work in `app.go` (vault check, unlock, create, open existing, the "Vault Already Open" question), which belongs to the screens and the session, not to an open vault.

## Outcome
- Commits: `refactor(ui): run user operations through one operations module`, `refactor(ui): put the file choosers behind a picker seam`, `fix(ui): keep Move, Switch and passphrase change from overlapping`.
- Tests: `operations_test.go` covers the module alone on a queued runner (progress and control, refusal by kind and freeing after success or failure, failure wording or the caller's handling, results dropped after the end, the titled dialog, confirm Yes/No and activity, input in a dialog, every picker outcome including answers after the end, `pickSave` answering exactly once). Import and export now run through *Import File*, *Export File* and the scripted chooser; `TestFileOps_ExportWipesThePlaintextWhicheverWayItEnds` covers the five ways an export ends, and `TestApp_ExportWipesWhatItWrote` checks the bytes handed to the writer are wiped. The guard fix was written test first: `TestSettings_MoveVaultIsNotStartedTwice` (3 copies before, 1 after), `TestSettings_SwitchVaultWaitsForAMoveToFinish`, `TestSettings_PassphraseChangeWaitsForAMoveToFinish`.
- Mutation checks: dropping the kind claim, its release, the control's disable or enable, the progress hide, the confirm's Yes check or activity, the content instrumenting, either wipe in `exportDecoded`, `end`'s cancel, closing a late file, the nil answer after a chooser failure, `now`'s claim or release, or the `opVault` kind or control on Move or Change Passphrase each fail a test.
- Verification found one window left: a lock after the export's decode finished but before its result was delivered dropped the decoded bytes unwiped. `startOwned` (on `backgroundOwned`) takes a `release` for a dropped result; the export passes `crypto.Wipe` (`TestOperations_StartOwned_ReleasesAResultDroppedAtTheLock`). It also added `pickFolder` tests for a failed chooser and an answer after the lock.
- Adapted tests (they reached removed internals): export and import flows, the lock-wipes-export hygiene test, the dialog-activity test, the update tests' runner swap (`setRunner`), and components built with a runner now take operations.

## Acceptance criteria
- [x] One module runs operations with progress, one re-entry policy, activity and error wording
- [x] Confirmations and file choosers go through it; the choosers sit behind a seam with two adapters
- [x] Export and import tests drive the public flow; no test calls `saveExport`
- [x] The export plaintext is wiped in one function on every path
- [x] Move and Switch can't overlap a copy (test first)
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG

## Related
- [[Roadmap]]
- [[F40 Error wording]]
- [[F36 Private exports]]
- [[F10 Keep vault IO off the UI goroutine]]
