---
id: F11
title: Memory hygiene and honest docs
aliases:
  - F11
tags:
  - fix
  - security
  - docs
severity: medium
status: done
phase: 3
area: crypto
review_finding: Memory hygiene
files:
  - internal/crypto/identity.go
  - internal/ui/app.go
  - internal/ui/unlock.go
  - internal/ui/settings.go
  - internal/ui/list.go
  - internal/ui/detail.go
  - internal/ui/fileops.go
  - internal/ui/hygiene_test.go
  - internal/store/store.go
  - internal/store/migrate.go
  - README.md
  - DESIGN.md
depends_on:
  - "[[F01 Scrypt-wrapped identity]]"
created: 2026-10-27
---

# F11: Memory hygiene and honest docs

## Problem
See [[Security Model#Where key material lives (after F01)]]. In short:
- Since [[F01 Scrypt-wrapped identity]], the passphrase is used once to unwrap `vlt.age` and is not held for the session. The long-lived secret is the X25519 vault key.
- The vault key's encoded form is in a `LockedBuffer`, but age's parsed `X25519Identity` lives on the Go heap for the session, along with one string copy made while parsing (and `x.String()` in `GenerateIdentity`). `Zero()` cannot wipe these.
- `UnlockScreen.entry.Text` still holds the passphrase for the whole session.
- `Lock()` keeps `a.secretList` and `a.detailView`, which hold every decrypted secret.
- The change-passphrase entries are only cleared on success.
- DESIGN claimed the passphrase is never kept in memory beyond derivation (now rewritten).

## Fix
- Clear the unlock and change-passphrase entries right after reading them, on **every** path.
- In `Lock()`, nil out `secretList` and `detailView` (`unlockScreen` is rebuilt by `showUnlockScreen`). The clipboard is already cleared on lock by [[F08 Correct clipboard clearing]].
- ✅ The passphrase is discarded after unwrapping (F01). Remaining: decide whether to parse the key per operation (more transient copies) or keep one parsed copy (current), and document the trade-off.
- Rewrite the README and DESIGN security sections to describe **best-effort** protection and its limits (Go strings, GC, Fyne widgets).

## As shipped
- The unlock and change-passphrase entries are cleared as soon as they're read, on every path. `SetText` also clears Fyne's undo history.
- `Lock` releases `App.mu` before its UI work, which removed a self-deadlock risk when widget callbacks re-enter the app. Its UI work (`dropSession`):
  - empties every loaded `*Secret` and blanks the detail form's entries
  - wipes a pending export buffer
  - drops the list, detail panel, main view and activity tracker (now cleared on the main goroutine, which fixes a race)
  - removes the canvas key handlers
- `store.List`, the migration probe and the legacy-sentinel branch zero their decrypted buffers.
- The parsed vault key is kept once for the session rather than parsed per operation. The trade-off is documented in DESIGN.
- README and DESIGN describe protection as best-effort and name the limits: Go strings, the GC, Fyne's renderer cache, library-internal buffers, and in-flight work. A false claim that vlt checks directory permissions on startup was removed.

## Acceptance criteria
- [x] After unlock, the unlock entry is empty (including after a failed attempt): `TestUnlockScreen_EntryIsClearedAfterSubmit`
- [x] Change-passphrase entries are cleared on every path: `TestSettings_PassphraseEntriesAreClearedOnEveryPath`
- [x] After lock, `App` holds no reference to any `*store.Secret`, and anything else still pointing at one sees it empty: `TestApp_LockDropsAllReferencesToSecrets`, `TestApp_LockEmptiesSecretsAndBlanksFormsStillReachableElsewhere`, `TestApp_LockWipesPendingExport`
- [x] The docs no longer overclaim (README, DESIGN, Security Model)

## Outcome
Verification measured real reachability with GC cleanups. Fyne's global renderer cache keeps old widgets, and the secret open in the form, reachable after lock until the cache expires. Secrets are therefore now emptied in place rather than only dereferenced.

Verification also found that `store.List` didn't wipe decrypted plaintext (contradicting the new docs), that a pending export buffer survived a lock, and a data race on `App.tracker`. All are fixed.

## Related
- [[Security Model]]
