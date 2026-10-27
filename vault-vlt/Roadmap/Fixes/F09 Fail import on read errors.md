---
id: F09
title: Fail import on read errors
aliases:
  - F09
tags:
  - fix
  - ui
severity: high
status: done
phase: 2
area: ui
review_finding: "#9"
files:
  - internal/ui/fileops.go
  - internal/ui/fileops_test.go
  - internal/ui/app.go
  - internal/ui/settings.go
depends_on: []
created: 2026-10-27
---

# F09: Fail import on read errors

## Problem
The read loop in `processImport` (`internal/ui/fileops.go:71-84`) treats **any** read error as end-of-file:

```go
if err != nil {
	break
}
```

A transient I/O error, a network-drive drop, or a permission change part-way through therefore imports a truncated file that looks complete.

## Fix
```go
data, err := io.ReadAll(io.LimitReader(reader, maxFileSize+1))
if err != nil {
	dialog.ShowError(fmt.Errorf("read %q: %w", filename, err), fo.window)
	return
}
if len(data) > maxFileSize {
	dialog.ShowError(errFileTooLarge, fo.window)
	return
}
```

This also removes the hand-rolled buffer loop. Consider showing the SHA-256 of the imported bytes in the confirmation dialog.

## Acceptance criteria
- [x] A reader that fails after N bytes produces an error dialog and saves nothing: `TestReadImport` ("read error part-way"), `TestPrepareImport_ReadErrorClosesAndFails`, `TestApp_FailingImportSavesNothing`, `TestApp_FailingImportShowsErrorNotGroupForm`
- [x] Files of exactly 10 MiB are accepted and 10 MiB + 1 byte is rejected: `TestReadImport`, `TestReadImport_OverLimitIsErrFileTooLarge`

## Outcome
Shipped with extra scope taken on from [[F10 Keep vault IO off the UI goroutine]]'s follow-up:
- Import read, MIME detection, base64 and SHA-256 run in the background. The SHA-256 is shown on completion.
- Export decode and write run in the background. A failed write or close is reported (`TestWriteExport*`), and the partial file is deleted where possible.
- Plaintext buffers are wiped on every path.

Verification found and fixed:
- An export dialog left open across a lock could still write plaintext. `Lock` now dismisses every dialog (`TestApp_LockDismissesOpenDialogs`), and a late confirmation writes nothing (`TestApp_ExportAfterLockWritesNothing`).
- Decoded plaintext wasn't wiped when the vault locked mid-decode, or on import close errors.
- A surviving mutant: the group form being shown despite a read error.

The passphrase-changed and vault-moved messages are now shown after the relock, so the dialog dismissal doesn't hide them.

## Related
- [[F03 Prevent silent overwrites]]: import now uses `Create` and asks before replacing (done)
