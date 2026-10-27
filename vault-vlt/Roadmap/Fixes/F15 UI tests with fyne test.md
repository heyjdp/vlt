---
id: F15
title: UI tests with fyne/test
aliases:
  - F15
tags:
  - fix
  - testing
severity: medium
status: done
phase: 5
area: ui
review_finding: Tests
files:
  - internal/ui/
  - internal/testutil/fixtures.go
depends_on:
  - "[[F04 Confirm passphrase on vault creation]]"
  - "[[F05 Refresh list after edit and delete]]"
  - "[[F07 Inactivity-based auto-lock]]"
  - "[[F12 Fix CI pipeline]]"
created: 2026-10-27
---

# F15: UI tests with fyne/test

## Problem
`internal/ui` has **zero tests**, yet most of the bugs in the [[2026-10-27 Code Review]] are in that package.

## Fix
Use `fyne.io/fyne/v2/test`, which renders headlessly with no display. Since F10, tests inject an inline or queued `runner` and use real vaults created with a low scrypt work factor (`store.WithWorkFactor(10)`), so no fake store is needed.

Auto-lock is tested with an injectable `timerFactory` whose fake timers are fired by hand (since F07).

## Test matrix
| Flow | Tests |
|---|---|
| Create vault | ✅ `TestCreateScreen_MismatchShowsErrorAndDoesNotCreate`, `_MatchingPassphraseCreatesAndClearsEntries`, `_ErrorClearsAfterSuccessfulRetry`, `TestApp_MissingVaultShowsCreateScreen`, `TestApp_CreateRunsOffTheMainGoroutine` |
| Unlock | ✅ `TestFlow_WrongPassphraseShowsError` (message, entry cleared), `TestUnlockScreen_EntryIsClearedAfterSubmit`, `TestApp_UnlockRunsOffTheMainGoroutine`, `TestApp_StaleUnlockIsDiscardedAfterVaultSwitch` |
| Edit | ✅ `TestApp_SelectThenEditUpdatesFilteredRow`, `TestApp_SaveFromListSelectionUpdatesList`, `TestDetailView_RecordFormMasksSecretsAndSavesEveryField`, `TestDetailView_PasswordFormMasksAndSavesValue` |
| Delete | ✅ `TestFlow_DeleteAsksForConfirmation` (Yes and No through the real dialog), `TestApp_SelectThenDeleteRemovesRowAndClearsSelection` |
| Auto-lock | ✅ `internal/ui/activity_test.go`: `TestAutoLock_IdlenessLocks`, `_TypingInAFormResetsTheCountdown`, `_StayUnlockedRestartsTheCountdown`, and others. No secrets referenced after lock: `TestApp_LockDropsAllReferencesToSecrets` ([[F11 Memory hygiene and honest docs]]) |
| Clipboard | ✅ `internal/ui/clipboard_test.go`: `TestClipboard_SecondCopyGetsTheFullTTL`, `_LeavesUnrelatedContentAlone`, `TestApp_LockClearsCopiedSecret`, `TestApp_QuitClearsCopiedSecret` |
| Import | ✅ `TestFlow_ImportAsksForGroupThenConfirmsReplace` (group form, then replace answered No and Yes), `TestApp_FailingImportShowsErrorNotGroupForm`, `TestApp_FailingImportSavesNothing` |
| Export | ✅ `TestFlow_ExportWritesTheFile`, `TestWriteExport_WriteErrorIsReportedAndWipes`, `TestApp_ExportAfterLockWritesNothing` |
| Change passphrase | ✅ `TestFlow_ChangePassphraseThroughSettings` (old passphrase rejected, new one unlocks), `TestSettings_PassphraseEntriesAreClearedOnEveryPath` |
| Switch vault | ✅ `TestFlow_SwitchVaultThroughSettings`, `TestSettings_SwitchedVaultIsSavedOnlyOnceOpened` |
| Settings | ✅ `TestLoadConfig_*`, `TestSettings_*` |

## As shipped
- The tests run headless on `fyne/test` and use real vaults created with a cheap scrypt work factor. `newApp` takes store options, so tests use 2^10, not 2^18.
- Background work runs on inline or queued runners, and timers are fake ones fired by hand.
- Shared helpers live in `helpers_test.go`. Widgets are found by placeholder, label or type rather than by position, and dialog buttons by label.
- Most ui tests run serially because `fyne/test` replaces the global app; pure functions run in parallel.

## Acceptance criteria
- [ ] All of the above run in CI under `-race`. They pass locally under `go test -race` (repeated runs, including `GOMAXPROCS=2`); the CI run is pending the first push.
- [x] `internal/ui` coverage ≥ 60 %: **86.3 %**

## Outcome
Verification found and fixed:
- **Memory.** The suite peaked at about 4.3 GB under `-race`, because `App` created and re-wrapped vaults at the production work factor. With test store options it peaks at 0.73 GB, and the run takes 11 s instead of 26 s.
- **A surviving mutant.** Import-replace ignored a "No" answer and no test noticed. The flow now answers No, then Yes.
- **Weak tests.** A real-timer goroutine test couldn't catch the bug it targeted, so it was removed; the fake-timer tests assert the stop. Positional `Objects[n]` lookups were replaced. The export test now drives `saveExport` itself.

The unused `UnlockScreen.PassphraseEntry` was removed. Only the illustrative example in `CLAUDE.md` still mentions it.

## Related
- [[Roadmap]]
