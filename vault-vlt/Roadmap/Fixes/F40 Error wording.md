---
id: F40
title: "Error wording"
aliases:
  - F40
tags:
  - fix
severity: low
status: done
phase: 11
area: ui
review_finding: "Architecture review 2026-11-11 (second pass), candidate A"
files:
  - internal/ui/errmsg.go
  - internal/ui/errmsg_test.go
  - internal/ui/app.go
  - internal/ui/about.go
  - internal/ui/create.go
  - internal/ui/detail.go
  - internal/ui/fileops.go
  - internal/ui/list.go
  - internal/ui/settings.go
  - internal/ui/totp.go
  - internal/ui/updates.go
  - internal/ui/*_test.go
  - internal/store/vault.go
  - internal/store/lock.go
  - internal/crypto/identity.go
  - internal/update/update.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on:
  - F39
created: 2026-11-11
---

# F40: Error wording

## Problem
- **Errors reached people with developer prefixes.** The store, crypto and update packages wrap errors with where they failed (`store.CopyTo "…": `, `store.Update: write: `), as the project's conventions ask. Most of the UI's ~25 error displays showed `err.Error()` as it was: "Move failed: store.CopyTo …", "Failed to open vault: store.Open: …", "save failed: store.Update: write: …".
- **Two strippers, both tied to format strings.** `userMessage` removed prefixes with a regex that matched the store's `store.Func "name": ` format (used by three displays), and `updateErrorText` trimmed `update.Latest: ` by hand. A new prefix shape, or an error from another package, went through untouched.
- A few errors were one string with the prefix inside it (`errors.New("store.ChangePassphrase: vlt.age does not hold …")`, `"update.Latest: GitHub answered %s"`), so no structural rule could separate the words from the prefix.

## Fix
- **`errmsg.go`** in `internal/ui` is the one module that turns an error into text: `errorMessage(doing, err) string` for labels and notices, `showError(window, doing, err)` for dialogs. `doing` is the failed action ("Couldn't move the vault"); the result is capitalised, with no full stop added.
- It names by meaning (`errors.Is`) the wrong passphrase, cancellation and timeouts; puts `*fs.PathError` and `*os.LinkError` as "can't <action> <path>: <why>" (permission denied, it does not exist); and otherwise walks the chain, dropping the text each wrapper puts before the error it wraps and keeping what it adds after, so `ErrBadLocation`'s detail, a server's name and ssh's message, an `*ExistsError`'s read error and "(the file … may be incomplete)" survive. No prefix format is matched.
- **Every error display** (about 25 call sites: unlock, create, error screen, server dialog validator, list notice, save, delete, import, replace, export, folder and file pickers, passphrase change, location, move, updates, browser) routes through it with a "Couldn't …" lead. `userMessage`, `storePrefix` (the regex) and `updateErrorText` are deleted; `isServerError` moved beside it and still only picks the screen and heading.
- The prefixed one-string errors in `store`, `crypto` and `update` now wrap a separate cause (same text), so the prefix comes before a `%w`.
- The UI's own wrappers that carried people's words (`"TOTP secret: %w"`, `"cannot import %q: … (%w)"`, `"read %q: %w"`) became the `doing` argument instead.
- **Tests**: `TestErrorMessage_DescribesRealErrorsInPlainWords` (table over real errors from temp-dir vaults: wrong passphrase on open and on change, no vault, vault and secret already there, open elsewhere via a planted `vlt.lock`, cancelled and timed-out `AcquireLock`, a bad location, a failed rename; plus server, update, export and joined errors wrapped as those packages do), each asserting the sentence and that no `store.`/`crypto.`/`update.`/`totp.` prefix leaks; `TestErrorMessage_UnwritableFolderSaysWhichAndWhy` (a real `CopyTo` into a read-only folder); `TestErrorMessage_LeadsWithDoingOrStandsAlone`. Existing tests now expect the new wording (updates, unreadable folder, TOTP check, mismatched passphrase).

## Outcome
- Sample: "Move failed: store.CopyTo \"/Volumes/usb/v\": mkdir /Volumes/usb/v: permission denied" is now "Couldn't move the vault: can't create the folder /Volumes/usb/v: permission denied"; "Cannot check for updates: …" is "Couldn't check for updates: GitHub's request limit was reached; try again later"; "TOTP secret: the TOTP secret is not valid base32 …" is "Couldn't save the secret: the TOTP secret is not valid base32 …".
- Mutation checks: writing back a wrapper's prefix, dropping its suffix, giving up on a wrapper that doesn't repeat one of its causes, dropping the passphrase, cancel, timeout, not-exist, `PathError`, `LinkError` or `mkdir` wording, the capitalisation, or either `Unwrap` form each fail a test. Mapping `fs.ErrPermission` to "permission denied" survives, since the system's text says the same.
- Kept: Settings' "New " + validation text, and the unlock screen's fixed messages for a wrong passphrase, a missing key and no vault, which are already written for people and decide the flow.
- Rule for later code: a package may add any wrapper, as long as its `pkg.Func:` prefix comes before a `%w` and never inside one string.

## Acceptance criteria
- [x] No error display in `internal/ui` shows `err.Error()` directly
- [x] `userMessage`, the prefix regex and `updateErrorText` are gone
- [x] The table test runs over real store errors and fails if a prefix leaks
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, CHANGELOG

## Related
- [[Roadmap]]
- [[F39 Secret files and keys]]
