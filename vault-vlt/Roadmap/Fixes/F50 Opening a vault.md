---
id: F50
title: "Opening a vault"
aliases:
  - F50
tags:
  - fix
severity: low
status: done
phase: 13
area: ui
review_finding: "Architecture review 2026-11-12 (third pass), candidate 2"
files:
  - internal/ui/opener.go
  - internal/ui/opener_test.go
  - internal/ui/app.go
  - internal/ui/session.go
  - internal/ui/session_test.go
  - internal/ui/remote_test.go
  - DESIGN.md
  - README.md
  - vault-vlt/Architecture/Architecture Overview.md
  - vault-vlt/Architecture/Unlock Flow.md
depends_on:
  - F49
created: 2026-11-12
---

# F50: Opening a vault

## Problem
- **App did each step of opening a vault, twice.** `onUnlock` and `onCreate` repeated one skeleton: run the open or create in the background, check whether the starting screen was still shown, discard a store that arrived late, then go on. The "still shown" question was hand-written three times (`a.unlockScreen != screen`, `a.createScreen != screen`, the `current` closures), with a fourth mechanism (`screenGen`) for the check at the location.
- **Error wording was inline.** Six store errors became screen text in `onUnlock`, which also called `store.Exists` again to tell leftovers from a vanished vault.
- **The Waiting vault crossed three files.** It was driven through the session's `Hold`, `Take`, `Pending` and `Discard`, so understanding it meant reading `app.go`, `session.go` and `lock.go` together. `Pending` had no production caller; every test of the flow built a whole App and peeked at it.

## Fix
- **One module, `opener` (`internal/ui/opener.go`).** `Look` (is a vault at the location?), `CheckFolder` (*Open Existing Vault…*), `Unlock` and `Create` run in the background, with the screen busy until the outcome arrives. `Unlock` and `Create` share one path and report one `openOutcome`: `vaultOpened` (the session has begun; the store and the session's context), `vaultWaiting` (open elsewhere; the lock's holder), `waitTimedOut`, `vaultGone`, `wrongPassphrase`, `nothingToUnlock`, `keyMissing`, `unlockFailed` or `createFailed`. `problem()` gives the screen's words for those that stay on the screen, unchanged.
- **The opener owns the Waiting vault until the person decides.** `OpenAnyway` takes the lock over in the background and then begins the session; `Cancel` lets the vault go; the auto-lock timeout lets it go on the timer's goroutine and reports `waitTimedOut` through `fyne.Do`. During a takeover the vault still waits, so the timeout and `Abandon` still let it go, and its `Close` waits for the takeover ([[F49 Store owns its lock file]]).
- **Stale results: `Abandon`, in one place.** `App.showUnlockScreen` and quitting call it. Results still to come for the old screen are dropped (a store opened or created meanwhile is let go), and the Waiting vault is let go; an answer after that does nothing. It replaces the three checks and `screenGen`.
- **App maps outcomes to screens.** `showOutcome` is a five-case switch; `askOpenAnyway` shows the dialog and passes the answer back. `quit` calls `closeVaults`, which abandons, then closes the session.
- **Decision: the session keeps `Discard`, the opener keeps the wait.** Every store the opener lets go goes through `session.Discard`, so zeroing the key at once, `Close` off the caller's goroutine and quitting's bounded wait stay decided in the session ([[F33 Vault session module]], [[F45 Lock without freezing]]). The opener calls it under its own mutex, so a quit racing the timeout still finds the vault (as `takeAndDiscardLocked` did; lock order opener → session, never the reverse). `Hold`, `Take`, `Pending`, the pending store and its timer leave the session, whose interface is back to `Begin`, `End`, `Activity`, `Pause`, `Close`, `Gen`, `Current`, plus `Store` and `Discard`.
- **Decision: the opener hands the vault to the session itself.** `Begin` is called by the opener (for a takeover, under its mutex), so no store is ever held by App between the wait and the session.
- **Decision: the opener works on `*store.Store` and the concrete session.** One adapter each; a seam for them would be hypothetical. The wait's race test calls the internal `waitFor` with one real store let go again and again.
- **Decision: an unparsable location skips the "Checking vault…" placeholder.** It was shown for at most one frame before the error screen; now the error screen is shown at once, with no background step.

## Outcome
Two commits on `refactor/deepen-open-and-lock`, each with build, vet, `go test -race` and golangci-lint green:
- `refactor(ui)`: the opener; App maps outcomes to screens; flow tests move to `opener_test.go`.
- `refactor(ui)`: `Hold`, `Take` and `Pending` leave the session; its wait tests move to the opener.

Nothing changes for the user: the same screens, dialogs, wording, *Open Anyway*, *Cancel*, timeouts, *Retry*, *Open Existing Vault…* and server dialog, and the location is still saved only after a successful unlock. `app.go` is about 135 lines shorter and `session.go` about 85. The opener's tests use temporary vaults, a queued runner, hand-fired timers and a real session, without a window; App keeps tests for the dialog, *Cancel*, the timeout's screen, quitting and the `Abandon` wiring. No test peeks at a waiting store any more.

## Acceptance criteria
- [x] Every opener test was mutation-checked: removing the screen check, a dropped store's release, `Cancel`, `Abandon`'s release of the Waiting vault, the timer, the timeout's busy reset or its screen check, the leftovers case, the takeover's "still waiting" check or timer stop, or making the screen busy each fails a test
- [x] `TestOpener_QuitWhileTheWaitingVaultTimesOutWaitsForItsRelease` fails when the timer lets the vault go outside the opener's mutex
- [x] `TestOpener_ASecondWaitingVaultReplacesTheFirst` fails when an old timer's check is "anything waiting" rather than "this vault"
- [x] `TestApp_AnUnlockFinishingAfterAVaultSwitchIsDiscarded` and `TestApp_StaleUnlockIsDiscardedAfterVaultSwitch` fail without `Abandon` in `showUnlockScreen`; `TestApp_OpenAnywayAfterTheWaitingStoreWasLetGoDoesNothing` fails without it in `closeVaults`
- [x] No caller of `session.Pending`, `Hold` or `Take` remains; the methods are deleted
- [x] DESIGN, README (project tree and test list), Architecture notes

## Related
- [[Roadmap]]
- [[F49 Store owns its lock file]]
- [[F33 Vault session module]]
- [[F45 Lock without freezing]]
- [[F26 Vault lock file]]
- [[Unlock Flow]]
