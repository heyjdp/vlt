---
id: F02
title: Atomic file writes
aliases:
  - F02
tags:
  - fix
  - store
severity: critical
status: todo
phase: 1
area: store
review_finding: "#2"
files:
  - internal/store/store.go
depends_on: []
created: 2026-10-27
---

# F02: Atomic file writes

## Problem
`Save`, `CreateSentinel`, `ReEncryptAll` and `CopyTo` all call `os.WriteFile` (`internal/store/store.go:76,149,303,323,362`). That opens the file with `O_TRUNC` and then writes, so a crash, power loss, or full disk between the two leaves a zero-length or partial `.age` file. That secret is unrecoverable.

## Fix
Add one helper and route every write through it:

```go
// writeFileAtomic writes data to path via a temp file in the same
// directory, fsyncs it, and renames it into place.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir) // fsync the directory so the rename is durable (no-op on Windows)
}
```

`List` and `CopyTo` must skip `.tmp-*` files.

## Acceptance criteria
- [ ] All `os.WriteFile` calls in `store` are replaced
- [ ] Test: a failed write (read-only dir) leaves the previous file contents intact
- [ ] Leftover `.tmp-*` files are ignored by `List`/`CopyTo`

## Related
- [[F01 Scrypt-wrapped identity]] · [[Storage Format]]
