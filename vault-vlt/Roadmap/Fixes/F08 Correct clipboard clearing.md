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
status: todo
phase: 2
area: ui
review_finding: "#8"
files:
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

## Acceptance criteria
- [ ] Copy A then B: B survives a full $\tau$
- [ ] An unrelated copy after vlt's copy isn't wiped
- [ ] Lock clears the clipboard and leaves no goroutines (check `runtime.NumGoroutine` or goleak in the test)

## Related
- [[Security Model]] · [[F06 Persist settings]]
