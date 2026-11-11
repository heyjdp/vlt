---
id: F35
title: "Location round trip"
aliases:
  - F35
tags:
  - fix
severity: medium
status: done
phase: 10
area: store
review_finding: Go skills audit 2026-11-11, item 2
files:
  - internal/store/location.go
  - internal/store/location_test.go
  - internal/ui/settings.go
  - internal/ui/remote_test.go
  - DESIGN.md
  - README.md
  - CHANGELOG.md
depends_on: []
created: 2026-11-11
---

# F35: Location round trip

## Problem
Settings saves a server vault's location as `Location.String()`, which must read back through `ParseLocation` as the same location. The audit's fuzz test found that it didn't: `ssh://nas:2222/srv/a%23b` was accepted, saved as `ssh://nas:2222/srv/a#b`, and that string is refused ("has a query or fragment"). `?` and `%` failed the same way. A host of `:` (from `[:]:srv` or `ssh://::/0`) was also accepted.

## Fix
- **`String`** builds the `ssh://` form with `url.URL`, which escapes the path.
- **IPv6 hosts** must parse as an IPv6 address (`netip`, zones allowed).
- **`FuzzParseLocation`**: every accepted remote location has a host and user of plain name characters, and reads back from `String` as itself. Running it found four more round-trip failures, each fixed and added to the table:
  - `@[` in a shorthand path was taken for the start of an IPv6 host, and a lone `]` was trimmed from a host (now refused);
  - a path with leading or trailing spaces lost them (`ParseLocation` trims), so `String` now uses the escaped URL form for it;
  - host `ssh` with a path starting `//` wrote `ssh://…`, which reads as a URL;
  - in a URL, a folder literally named `~` at the root read as the login directory; `String` writes it as `%7E`, and only a literal `~` means home.
- After those, five minutes of fuzzing (46 million inputs) found nothing.

## Outcome
Verification found the change safe and backward compatible: 2 million strings written by the old `String` parse the same, except paths with `@[`, which the old code misread as local paths. It also found:
- **Control characters were accepted in the path.** `ssh://nas/x%00y` gave `/x\x00y`, which a C SFTP server cuts at the NUL. They are now refused.
- **`~//x` read as `/x`** (already so before): `homeRelative` now drops the extra slashes, in both forms.
- **`SSH://` read as shorthand** for host `SSH`; the scheme is now matched in any case, and `String` avoids shorthand that would start with it in any case (which the fuzzer found next).
- **Settings compared location strings**, so a location saved in an older or typed form looked different from the same place written canonically. It now compares parsed locations (`sameLocation`), tested through the Switch button.
- Smaller: brackets around a mixed `||`/`&&`, and an `escapePath` helper.

Docs: the CHANGELOG says vlt couldn't open such a vault (Settings saves the string, opening parses it); the README says only a leading or trailing space needs `%20`, and that saved `ssh://` locations are shown escaped.

## Acceptance criteria
- [x] The round-trip table covers `#`, `?`, `%`, spaces, `@[`, `//` and a root `~`, and the reject table covers non-IPv6 colon hosts and control characters
- [x] `FuzzParseLocation` passes a long run (4 minutes after the last fix)
- [x] `go test -race ./...` and golangci-lint pass

## Related
- [[Roadmap]]
- [[F27 Server vaults in the UI]]
