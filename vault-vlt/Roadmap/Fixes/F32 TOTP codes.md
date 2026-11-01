---
id: F32
title: "TOTP codes"
aliases:
  - F32
tags:
  - fix
severity: low
status: done
phase: 8
area: ui
review_finding: User request (2026-11-01)
files:
  - internal/totp/totp.go
  - internal/totp/totp_test.go
  - internal/ui/totp.go
  - internal/ui/totp_test.go
  - internal/ui/detail.go
  - internal/ui/app.go
  - internal/ui/screenshots_test.go
  - docs/screenshots/
  - README.md
  - DESIGN.md
  - CHANGELOG.md
  - vault-vlt/Architecture/Storage Format.md
depends_on: []
created: 2026-11-01
---

# F32: TOTP codes

## Problem
Records had a masked *TOTP secret* field, but vlt did nothing with it. Users still needed an authenticator app to get the code, and nothing checked that the stored secret was usable.

## Fix
- **`internal/totp`** (standard library only):
  - `Parse` reads base32 as sites show it, or an `otpauth://totp/` link with its digits, period and algorithm.
  - HOTP, other link types and out-of-range values are refused with a reason.
  - `Key.Code` implements RFC 6238. `Key.Remaining` gives the time left in the current period.
- **Record view:**
  - A *One-Time Code* panel above the form shows the code (`123 456`), "Changes in N s" and *Copy Code*.
  - The copy goes through the clipboard clearer, so it is cleared after the TTL and on lock.
  - The panel ticks at whole seconds, using the app's timer factory and a generation counter. A re-render or a lock stops it.
  - An unreadable stored secret is explained instead.
- **Saving:**
  - The TOTP field is validated (`formField.check`) and stored trimmed.
  - A changed secret re-renders the form, so the code appears, changes or goes.
- **README screenshots** show a record with a code, rendered with a fixed clock.

## Acceptance criteria
- [x] RFC 6238 appendix B vectors (SHA-1, SHA-256, SHA-512) pass
- [x] `TestCode_*`, `TestParse_*`, `TestRemaining`, `TestStep_*`, `TestWipe_*` (package); `TestTOTP_*`, `TestSpacedCode`, `TestCheckTOTPSecret` (UI)
- [x] README, DESIGN, CHANGELOG; Architecture and Security Model notes
- [x] `go test -race ./...` and golangci-lint pass

## Outcome
Verification found and fixed:
- **Key and code survived a lock.** The decoded key bytes stayed reachable through the Copy Code closure, which Fyne's renderer cache can keep alive, and the code label kept its text. Stopping the panel, on re-render or lock, now calls `Key.Wipe` and blanks the label (`DetailView.totpStop`).
- **An old, unreadable TOTP value blocked every save of its record.** Only a new or changed secret is checked now.
- **Steam Guard links were accepted and gave codes that never work** (`otpauth://totp/…&encoder=steam`). Any `encoder` is refused.
- **Parsing was stricter than real links.** These now work:
  - a raw `%` or `#` in the label (the label isn't parsed any more);
  - parameter names in any case;
  - non-breaking spaces in a pasted secret.
  
  `parseURI` was split up (`keyFromParams`).
- **Times outside 1970–2262.** `Remaining` overflowed after 2262 and was off before 1970, where `Code` also disagreed with it. Both now count periods from the epoch, rounding down (`Key.step`).
- **Tests that missed things.** 7 of 58 mutants survived. New tests cover:
  - the countdown rounding up;
  - a save finishing after the user moved on;
  - a save that leaves the secret alone keeping the form;
  - both ends of the digit and period ranges;
  - an untrimmed link at package level.
  
  An earlier test secret, "on my phone", turned out to be valid base32 and proved nothing; it is now unparseable.
- **Docs:**
  - CHANGELOG said "any period".
  - The README didn't warn that one vault then holds both factors; it now does, as do the CHANGELOG and DESIGN.
  - "The six digits" contradicted 6–8.
  - Period range, Steam wording, test-list order.
  - A note in Storage Format.

## Related
- [[Roadmap]]
- [[F08 Correct clipboard clearing]]
- [[F11 Memory hygiene and honest docs]]
