---
id: F07
title: Inactivity-based auto-lock
aliases:
  - F07
tags:
  - fix
  - ui
  - security
severity: high
status: done
phase: 2
area: ui
review_finding: "#7"
files:
  - internal/ui/activity.go
  - internal/ui/activity_test.go
  - internal/ui/app.go
  - internal/ui/detail.go
  - internal/ui/fileops.go
  - internal/ui/settings.go
depends_on: []
created: 2026-10-27
---

# F07: Inactivity-based auto-lock

## Problem
`resetLockTimer` (`internal/ui/app.go:236`) is only called from toolbar buttons. Typing, list selection, scrolling and dialogs don't reset it. With the default 5-minute timeout, the vault locks while you're editing a secret, and `Lock()` replaces the window content, **discarding unsaved edits**.

Formally, the lock fires at $t_\text{last toolbar click} + \tau$ rather than $t_\text{last input} + \tau$.

## Fix
- Reset on all input: wrap the window content in a thin custom widget that implements `fyne.Tappable`/`desktop.Hoverable`, and hook `window.Canvas().SetOnTypedKey`/`SetOnTypedRune`. Fyne 2.6+ also lets you register a desktop canvas key-down handler.
- Before locking with a dirty form, either warn ("Locking in 30 s, unsaved changes") or keep the draft in memory for after unlock. Warning is simpler and safer.
- Read the timeout from the live config ([[F06 Persist settings]]).

## As shipped
- `activityTracker` (`activity.go`) wraps the callbacks of entries, select entries, buttons, list rows, sliders and checks in the main view and in every rendered detail or settings panel.
  - Fyne has no global input hook, so this replaces the planned `Tappable`/`Hoverable` overlay.
  - The tracker forgets replaced forms, so old secret values aren't kept alive, and it is dropped on lock.
- Canvas key handlers cover typing when nothing has focus. Dialog callbacks (file pickers, import form, confirmations) report activity themselves.
- Timeouts of 60 s or more show a warning 30 s before locking, with *Stay unlocked* and *Lock now*. This was chosen over preserving drafts: the lock still always happens when the user is away.
- Timers come from an injectable `timerFactory`. Generation numbers stop a timer replaced by activity from warning or locking.
- The timeout is re-read on every reset, so a Settings change applies immediately.

## Acceptance criteria
- [x] Typing in any entry postpones the lock: `TestAutoLock_TypingInAFormResetsTheCountdown`, `_SearchAndListSelectionCountAsActivity`, `_TypingWithNothingFocusedResetsTheCountdown`, `_DialogCallbacksCountAsActivity`
- [x] Locks after idleness, not during typing: `TestAutoLock_IdlenessLocks`, plus the restarted countdown locking once typing stops. A real-timer probe in verification typed every 50 ms for 1 s without locking.
- [x] Warning works: `TestAutoLock_WarnsBeforeLockingAndActivityDismisses`, `_StayUnlockedRestartsTheCountdown`, `_LockNowLocksImmediately`, `_StaleTimersAreIgnoredAfterActivity`
- [x] Live config: `TestAutoLock_TimeoutChangeAppliesOnNextActivity`, `_DisabledWhenTimeoutIsZero`
- [x] Covered by [[F15 UI tests with fyne test]]

## Outcome
Verification found that *Stay unlocked* did nothing: an earlier blanket edit had removed its reset call. That is now fixed and tested.

It also found and fixed:
- dialog input not counting
- the tracker retaining every rendered form (including secret values) until the next unlock
- a stale warning or lock firing after fresh activity
- slider changes applying one input late

Known limit: scrolling and pointer movement don't count as activity.

## Related
- [[Security Model]]
