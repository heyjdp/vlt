---
id: F34
title: "Tampered vault files"
aliases:
  - F34
tags:
  - fix
severity: high
status: done
phase: 10
area: crypto
review_finding: Go skills audit 2026-11-11, item 1
files:
  - internal/store/store.go
  - internal/crypto/vault_test.go
  - internal/store/store_tamper_test.go
  - internal/store/store_test.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on: []
created: 2026-11-11
---

# F34: Tampered vault files

## Problem
age authenticates every file, and that is the vault's main promise against anyone who can write to it: a hostile server, a shared folder, a bad disk. No test showed vlt relies on it. The only bad-input test fed `"not age"`. The read-error branches of `crypto.Decrypt` and `DecryptWithPassphrase`, and `Store.Get`'s decrypt and parse failures, never ran. Nothing checked what happens to a valid file encrypted to another vault's key.

## Fix
- **`crypto` tests**: one table of tamperings, applied to a file just over one age chunk:
  - a flipped byte in the payload, the nonce and the last byte, and a changed header MAC character;
  - the last 16 bytes cut off, the final chunk cut off, a byte appended;
  - only the header and nonce, only the header, an empty file.

  `Decrypt` and `DecryptWithPassphrase` must return an error and no plaintext for each. With the right passphrase, damage is never reported as a wrong passphrase. (An altered scrypt stanza is left out: age can't tell it from a wrong passphrase.)
- **`store.openSecret`** (new), shared by `Get` and `List`: after decrypting and parsing, the secret must have a name and a known type, and its name and group must give back the path it was read from (ignoring case: no two slugs differ only in case, and folders made by hand may). A secret sharing its slug with another name still matches, so `Update`'s collision check is unchanged.
- **`store` tests**: a flipped byte, another vault's file, a file copied over another secret and into another group, and files holding `not json`, `{}`, an unknown type or a blank name. `Get` refuses each without calling it missing; `List` skips them and returns the good secret once.

## Outcome
Verification found that the tests alone weren't enough:
- **A file decrypting to `{}` or a nameless secret was accepted:** `Get` returned an empty secret and `List` showed a nameless entry.
- **A secret's file wasn't tied to its path:** copying `work/a.age` over `work/b.age` made `Get("b")` return "a", and `List` showed "a" twice.

Both are fixed by `openSecret`. The verifier's mutations (partial plaintext, damage reported as a wrong passphrase or as missing, a zero secret accepted) are all caught, as are removing the path and type checks.

A second check found nothing vlt ever wrote is hidden (slug rules haven't changed; legacy empty-slug, untrimmed and ungrouped names still open; `Update`'s collision check works), but files named by hand in another case were, so the path comparison now ignores case. Errors for a file that holds no valid secret got their own text.

Still open: the list leaves an unreadable file out with only a log warning; the user isn't told. That belongs with [[F38 Secret list errors]].

## Acceptance criteria
- [x] The new tests pass, and fail when `Decrypt` returns partial plaintext, or `Get` returns a secret for a file it can't decrypt, parse or place
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG, Security Model

## Related
- [[Roadmap]]
