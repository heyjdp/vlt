---
id: F46
title: "Tests on every OS"
aliases:
  - F46
tags:
  - fix
severity: low
status: done
phase: 12
area: ci
review_finding: Go skills audit 2026-11-11, item 9
files:
  - internal/store/vault.go
  - internal/store/vault_test.go
  - .github/workflows/ci.yml
  - internal/ui/operations.go
  - internal/ui/fileops.go
  - internal/ui/helpers_test.go
  - internal/ui/session_test.go
  - internal/ui/remote_test.go
  - Makefile
  - .lefthook.yml
  - README.md
  - DESIGN.md
  - CLAUDE.md
  - CHANGELOG.md
depends_on:
  - F47
created: 2026-11-12
---

# F46: Tests on every OS

## Problem
- **The tests ran only on Linux.** CI built vlt on macOS and Windows but never tested there, so the Windows-only code (atomic writes without a directory fsync, exports written in place, `console_windows.go`, `process_windows.go`, file-mode differences) and every test's Windows skip had never run.
- **Nothing caught order-dependent tests**: the suite always ran in source order.

## Fix
- **`test` is a matrix** over `ubuntu-latest`, `macos-latest` and `windows-latest` (`fail-fast: false`, bash on every OS, `CGO_ENABLED=1`, a 30-minute job limit). apt packages, `go mod tidy -diff`/`go mod verify` and coverage (total in the job summary, profile as a `coverage` artifact) run on Linux only; vet and `go test -race -shuffle=on -timeout 10m` run everywhere. `make test-race` and the pre-push hook use the same flags.
- **A Windows bug the matrix would have found first**: Fyne's file URIs use forward slashes on Windows, so a folder from *Browse…* or *Open Existing Vault…* came back as `C:/Users/…`. `pickFolder` now returns `filepath.FromSlash(uri.Path())` (a no-op on Unix), as does the path in a failed export's error. `TestOperations_PickFolder_GivesThePath` and `TestSettings_BrowseFillsInTheChosenFolder` compare against `t.TempDir()` and would have failed on Windows.
- **Success-path session waits** (`sess.Close(time.Second)`, 8 places) use `closeWait` (5 s): `Close` returns as soon as the stores are released, so passing tests are no slower, and a slow Windows runner under `-race` has headroom.
- **Decision: keep `-race` on Windows.** The race detector supports windows/amd64 with cgo, and the runner image has MinGW gcc (15.2) on the PATH, which the build job already uses for Fyne.
- **Decision: keep `-timeout 10m`.** `internal/ui`, the slowest package, takes about 40 s under `-race` locally.

## Outcome
A review of every test for Windows and macOS risks (open files at `TempDir` cleanup, renames of open files, slash paths, modes, symlinks, `HOME`, external programs, global state under shuffle) found only the slash-path bug above; everything else is already skipped with a reason or works on Windows. Cross-checks: `GOOS=windows go vet` on store, crypto, totp, update and (with `CGO_ENABLED=0`) ui, and `go test -c` for Windows on the non-cgo packages. Four local runs with `-race -shuffle=on` (different seeds) passed.

Low-risk leftovers, watched rather than changed: memguard locks one page per buffer and Windows allows only a few dozen locked pages by default (the suite peaks at about 13 vault keys plus 3 other buffers); a concurrent-create test may leave a `.tmp-*` hard link on Windows that it doesn't check for.

### Review
Three independent verifiers checked the branch (config, tests, docs):
- **Config**: every action SHA matches its version tag; with zizmor's `advanced-security: false` nothing is uploaded, so `contents: read` suffices; `filepath.FromSlash` is the right fix for Fyne's slash paths; the removed `//nolint` directives were truly dead.
- **Tests**: no test is expected to fail on Windows.
- **Docs**: CLAUDE.md's Makefile, go.mod, golangci-lint and static-analysis sections, README's tool table, test and hook wording, and the CHANGELOG section were out of date; fixed in a follow-up `docs:` commit, and `workflow-lint` got a 10-minute job limit.

Residual risks on Windows, to watch in the first runs: the memguard locked-page limit above; Defender holding a file open and failing the rename in `writeFileAtomic`; timing bounds under `-race` on a slow runner; the remote/ssh tests are skipped there; and the `FromSlash` fix is only tested on Unix, where it is a no-op, until the Windows job runs.

**Second CI run (run 43129173865, 2026-11-12):** green on Linux, macOS and Windows, with `-race -shuffle=on`.

**First CI run (run 19, 2026-11-12):** Linux and macOS green; Windows failed one test, `TestApp_UnreadableDefaultVaultIsNotOfferedAgain`, which points the vault at a file. On Unix looking inside a file fails as "not a directory", so vlt showed its error screen; on Windows it fails as "path not found", which `store.Exists` read as an empty location, so vlt offered to create a vault there. A real Windows bug: `Exists` and `Create` now refuse a file location with `store.ErrNotAFolder` on every system (`TestStore_Exists_AFileIsNotAVaultLocation`, which fails without the fix on Unix too, since the old error wasn't `ErrNotAFolder`). Left as is: a location whose path runs *through* a file (`file\sub`) still reads as missing on Windows (Go maps `ERROR_PATH_NOT_FOUND`, which is also its ENOTDIR, to `ErrNotExist`), so vlt offers to create a vault there; creating fails at `MkdirAll` before anything is written.

## Acceptance criteria
- [x] actionlint and zizmor clean; `make check` passes locally
- [x] `go test -race -shuffle=on ./...` passes locally with several seeds
- [x] The test job passes on macOS and Windows on GitHub (only checkable once pushed), including `-race` on Windows

## Related
- [[Roadmap]]
- [[F47 Workflow linters]] (`depends_on`: both change `ci.yml`, `make check` and the hooks, and F46 builds on F47's versions of them)
- [[F48 nolintlint]]
