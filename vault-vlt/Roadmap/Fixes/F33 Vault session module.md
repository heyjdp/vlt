---
id: F33
title: "Vault session module"
aliases:
  - F33
tags:
  - fix
severity: medium
status: done
phase: 9
area: ui
review_finding: Architecture review 2026-11-11, candidate 1
files:
  - internal/ui/session.go
  - internal/ui/session_test.go
  - internal/ui/app.go
  - internal/ui/about.go
  - internal/ui/updates.go
  - internal/ui/*_test.go
  - GLOSSARY.md
  - README.md
  - DESIGN.md
depends_on: []
created: 2026-11-11
---

# F33: Vault session module

## Problem
Whether a vault is open was spread across nine fields of `App`:
- `store`, `pending`, `pendingTimer`, `cancelCtx`, `closing`;
- `lockTimer`, `warnTimer`, `timerGen`, `lockGen`.

About fifteen functions used them. A mutex guarded some of the fields but not others (`pendingTimer` was set without it). `Lock` was called both from the auto-lock timer's goroutine and from the main goroutine. "Has the session ended since?" was answered in two ways (the context, and `lockGen`). Testing any of this needed a window, a real unlock through the passphrase entry, and reaching into `App`'s fields.

## Fix
- **`session` (`internal/ui/session.go`)** is a deep module. Its interface:
  - `Begin`, `End`, `Activity`;
  - `Hold`, `Take` and `Pending` for the waiting vault;
  - `Store`, `Gen`, `Current`;
  - `Close(wait)`.
- **Behind that interface** sit the open and waiting stores, the session context, the auto-lock and warning timers with their generation, and the last closed store's release channel, all under one mutex. Callbacks run through `fyne.Do` and only for timers that are still current.
- **`App`** no longer holds session state: it switches screens and reacts to the session. `about.go` and `updates.go` ask the session whether a vault is unlocked, or whether the session has changed, instead of reading `App` fields. Tests use `app.session.Store()` and `Pending()`.
- **Unit tests** (`session_test.go`) drive the module with real temporary stores and hand-fired timers, without a window.
- **`GLOSSARY.md`** is new. It separates *Lock* (ending a session) from *Lock file* and *Open elsewhere*, and names the *Waiting vault*.

## Outcome
Verification found and fixed:
- **The key could outlive the auto-lock.** The first version of this refactor sent the whole timeout through `fyne.Do`, so the key was zeroed only when the main goroutine got to it. GLFW's event polling blocks while a window is resized or a native menu is open. The timeout now ends the session (closing the store and zeroing the key) on the timer's goroutine, and only the screen change waits for the main goroutine. A late notice for a session ended another way is dropped. A waiting store's timeout now closes it on the timer's goroutine as well.
- **Untestable waits.** Quitting waits for a server vault to release its lock, but four mutants of that wait survived: local stores release their locks before `Close` returns. The session is now generic over a small `sessionStore` interface, so tests use a stand-in that releases its lock only when told to.
- **Latent:**
  - `Begin` on an open session now ends it first.
  - `Hold` closes a store already waiting.
  - `Take(nil)` is false.
  - `Close` stops the waiting store's timer.
- **Tests that missed things.** 11 of 25 mutants survived at first. Tests were added for:
  - the waiting store with auto-lock off;
  - the warning at exactly twice its lead;
  - callbacks waiting for the main goroutine;
  - `Take(nil)`, `Close` stopping the timer, `Close`'s deadline, and the replacement cases above.
  
  Every survivor is now caught.
- **Smaller fixes:**
  - The callbacks are named (`sessionCallbacks`), so they can't be swapped.
  - `runner` parameters named `session` were renamed `work`.
  - Stale comments and DESIGN's `App.lockGen` were corrected.
- **Glossary.** Five definitions contradicted the app:
  - *Passphrase* (it is also typed when creating a vault and changing it);
  - *Open elsewhere* (vlt asks before showing the vault, not before unlocking it, and a crashed local copy is taken over);
  - *Auto-lock* ("usually" preceded by a warning);
  - *Open* and *Group*, whose _Avoid_ lists clashed with UI text.
  
  These were corrected, *Open* was added, and the *Waiting vault* no longer counts as a session.
- **Separately:** `govulncheck` fails on `main`. GO-2026-6859 (`net/http`) needs toolchain go1.27.4; it is fixed in its own commit.

## Acceptance criteria
- [x] `TestSession_*` pass; the existing App tests pass unchanged apart from field access
- [x] `go test -race ./...` and golangci-lint pass
- [x] README, DESIGN, GLOSSARY

## Related
- [[Roadmap]]
- [[F07 Inactivity-based auto-lock]]
- [[F26 Vault lock file]]
