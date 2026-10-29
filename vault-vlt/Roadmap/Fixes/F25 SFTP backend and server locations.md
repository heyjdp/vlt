---
id: F25
title: "SFTP backend and server locations"
aliases:
  - F25
tags:
  - fix
severity: medium
status: done
phase: 7
area: store
review_finding: User request (2026-10-29)
files:
  - internal/store/location.go
  - internal/store/sshfs.go
  - internal/store/console_windows.go
  - internal/store/console_other.go
  - internal/store/location_test.go
  - internal/store/sshfs_internal_test.go
  - go.mod
  - go.sum
depends_on:
  - "[[F24 Storage abstraction]]"
created: 2026-10-29
---

# F25: SFTP backend and server locations

## Problem
There was no way to keep a vault on a server.

## Fix
- **Locations** (`location.go`): `ParseLocation` accepts a local path, `[user@]host:path` or `ssh://[user@]host[:port]/path`, with `~`, IPv6 and ports.
  - Host and user may hold only letters, digits, `.`, `_` and `-` (not first; IPv6 also `:` and `%`), because ssh can substitute them into commands from the user's ssh config.
  - `String` writes a form `ParseLocation` reads back.
- **Connecting** (`sshfs.go`): `dialSSH` runs the system `ssh` with `BatchMode=yes`, `ConnectTimeout=15`, `ServerAlive*` and no forwarding: `-s -- host sftp`. It speaks SFTP with `github.com/pkg/sftp` v1.13.12. If ssh fails, its stderr is shown.
- **Safe writes** (`remoteFS`):
  - Temp files are created exclusively and set to 0600 before any data is written, and fsynced if offered.
  - Replacing a file uses `posix-rename`; creating one uses `hardlink`, then a stat to report `ErrExist`.
  - A server without both extensions is refused.
- **Dropped connections**: the operation that saw the drop fails with `ErrConnectionLost`, and the next one reconnects.
- **Closing**: `Store.Close` zeroes the key at once and closes a remote connection in the background (`Closed()` reports when it's done).

## Acceptance criteria
- [x] `TestParseLocation`, `TestParseLocation_Rejects`, `TestLocation_StringRoundTrips`
- [x] `TestRemoteStore_FullLifecycle` over pkg/sftp's server on an in-process pipe: create, CRUD, passphrase change, reopen, copy local↔remote, only ciphertext on the server
- [x] `TestRemoteFS_ReconnectsAfterTheConnectionDrops`, `TestRemoteFS_RefusesAServerWithoutSafeWrites`, `TestSSHArgs`, `TestDialSSH_ReportsWhatSSHPrinted`
- [x] `TestRemoteStore_RealServer` passes against OpenSSH on 10.20.20.150 (Debian 13): `fsync` offered, vault, temp and group folders 0700, files 0600, lock released
- [x] `govulncheck`: no vulnerable code reached. `golang.org/x/net` bumped to v0.62.0

## Outcome
Verification found and fixed:
- **A temp file was briefly readable by other users.** SFTP creates files with the server's umask, so another user could open one before its chmod and keep reading it. Temp files now live in `.vlt-tmp/`, made 0700 (or tightened) once per session (`TestRemoteStore_TempFilesStayInAPrivateFolderThatIsNotAGroup`, `TestRemoteFS_LoosenedTempFolderIsMadePrivate`). Leftovers older than an hour are removed.
- **Shell characters in host names.** `h;id:/v` parsed, and OpenSSH before 9.6 can pass `%h` into a ProxyCommand. Host and user names are now limited to `[A-Za-z0-9._-]` (IPv6: also `:` and `%`).
- **A hostile server could steer a copy out of the destination.** It could return a name like `x/..`. Listings now drop any name that isn't a single path element (`TestPlainEntryName`).
- **A dropped connection during `List`** silently left secrets out. Listing now fails as a whole (`TestRemoteStore_ListFailsWhenTheConnectionDrops`).
- **A data race on the fsync flag** across a reconnect. Each session now records its own extensions.
- **A stuck ProxyJump child could hold up `Wait`.** Fixed with `cmd.WaitDelay`.
- **ssh config hardening.** ssh now also gets `-T`, `ClearAllForwardings`, `PermitLocalCommand=no` and `RemoteCommand=none`, and no console window on Windows.
- **Servers that cannot hard-link** (exFAT, some network mounts) now fall back to an `O_EXCL` create, as locally.

Accepted:
- **Host keys.** `StrictHostKeyChecking accept-new` in the user's config is honoured. This is documented.
- **Fsync is not exercised in CI.** pkg/sftp's server lacks it; the real-server test covers it.

## Related
- [[Roadmap]]
- [[ADR-002 Vaults on a server over the system ssh]]
- [[F24 Storage abstraction]]
- [[F26 Vault lock file]]
