---
id: F08
title: Correct clipboard clearing
aliases:
  - F08
tags:
  - fix
  - ui
  - security
severity: high
status: done
phase: 2
area: ui
review_finding: "#8"
files:
  - internal/ui/clipboard.go
  - internal/ui/clipboard_test.go
  - internal/ui/detail.go
  - internal/ui/app.go
depends_on: []
created: 2026-10-27
---

# F08: Correct clipboard clearing

## Problem
`copyToClipboard` (`internal/ui/detail.go:383`) starts a new goroutine per copy. Each one sleeps for the TTL $\tau$ and then clears the clipboard **unconditionally**.

If secret A is copied at $t_0$ and secret B at $t_1 > t_0$, A's goroutine clears the clipboard at $t_0 + \tau$, so B's effective lifetime is

$$
\tau_B^\text{eff} = \tau - (t_1 - t_0) < \tau
$$

Two goroutines also write competing countdowns to the status bar. Other problems:
- If the user copies something unrelated after a secret, vlt wipes it anyway.
- `Lock()` neither clears the clipboard nor stops the goroutines, which also have no termination signal (`CLAUDE.md`: *goroutine without a done signal*).

## Fix
- One `clipboardClearer` owned by `App`, with a `time.Timer` plus a cancel context. Each copy stops the previous timer and starts a new one.
- When it fires, clear only if `Clipboard().Content()` still equals what vlt copied. Compare a SHA-256 of the value rather than storing another plaintext copy.
- `Lock()` clears immediately (with the same check) and cancels the countdown.
- Use a `time.Ticker` for the status countdown, stopped via the context.

## As shipped
- `clipboardClearer` (`clipboard.go`), one per app, uses chained one-second timers from the app's `timerFactory` instead of a `time.Timer` plus `time.Ticker`.
- Each copy starts a new generation; ticks of older generations do nothing. This replaces the planned cancel context.
- On expiry, `Lock` or quit, the clipboard is cleared only if it still holds vlt's value (SHA-256 comparison). An empty read, meaning the OS clipboard couldn't be read, is treated as vlt's value.
- Quit is intercepted on window close and through a *Quit* menu item.
- The TTL is read at each copy. A non-positive TTL falls back to 30 s, and empty values aren't copied.

## Acceptance criteria
- [x] Copy A then B: B survives a full TTL: `TestClipboard_SecondCopyGetsTheFullTTL`
- [x] An unrelated copy isn't wiped: `TestClipboard_LeavesUnrelatedContentAlone`, `_ClearLeavesUnrelatedContentAlone`
- [x] Lock clears the clipboard and leaves no goroutines: `TestApp_LockClearsCopiedSecret`, `TestClipboard_ClearWipesImmediatelyAndStops`, `_RealTimersLeaveNoGoroutines` (real timers with `runtime.NumGoroutine`, not goleak)
- [x] Quit clears too: `TestApp_QuitClearsCopiedSecret`

## Outcome
Verification found and fixed:
- Quitting left a copied secret on the system clipboard. Fyne's `OnStopped` runs after GLFW shuts down, so close and quit are intercepted instead.
- On Windows, a blocked clipboard read returning "" made the digest check skip the wipe.
- A surviving test mutant: copy not stopping the previous timer.

As a side effect, this also removed the deprecated `Window.Clipboard` calls (SA1019).

## Related
- [[Security Model]] · [[F06 Persist settings]]
