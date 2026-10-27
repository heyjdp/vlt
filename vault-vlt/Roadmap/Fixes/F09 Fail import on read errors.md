---
id: F09
title: Fail import on read errors
aliases:
  - F09
tags:
  - fix
  - ui
severity: high
status: todo
phase: 2
area: ui
review_finding: "#9"
files:
  - internal/ui/fileops.go
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
- [ ] A reader that fails after N bytes produces an error dialog and saves nothing
- [ ] Files of exactly 10 MiB are accepted; 10 MiB + 1 byte is rejected

## Related
- [[F03 Prevent silent overwrites]]: import also needs the existence check
