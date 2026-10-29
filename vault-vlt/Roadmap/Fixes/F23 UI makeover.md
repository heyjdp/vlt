---
id: F23
title: "UI makeover"
aliases:
  - F23
tags:
  - fix
severity: low
status: done
phase: 6
area: ui
review_finding: User request (2026-10-29)
files:
  - internal/ui/theme.go
  - internal/ui/style.go
  - internal/ui/unlock.go
  - internal/ui/create.go
  - internal/ui/app.go
  - internal/ui/list.go
  - internal/ui/detail.go
  - internal/ui/settings.go
  - internal/ui/icon.png
  - internal/ui/screenshots_test.go
  - cmd/vlt/main.go
  - Makefile
  - README.md
depends_on: []
created: 2026-10-29
---

# F23: UI makeover

## Problem
The app works but looks like a widget demo:
- Fyne's default blue, unrelated to the teal app icon.
- Text-only toolbar.
- List rows whose group and name columns overlapped ("Personal Finance" ran into the name).
- A detail form that was cut off below the window (no scroll).
- Plain "Label:" rows and RFC 3339 timestamps.
- Wrapped text on the create screen that clipped.

## Fix
- **Theme** (`theme.go`): a teal accent from the icon, neutral light and dark palettes, rounded inputs, text sizes heading > subheading > body > caption.
- **Shared look** (`style.go`): cards, headings, muted text, empty states and type icons. Everything is built from plain containers, so the activity tracker and `blankEntries` still reach every field.
- **Screens**:
  - Unlock, create and error screens are centred cards (unlock and create with the app icon, the error screen with an error icon), laid out by `columnLayout`, which measures wrapped text at its real width.
  - The main view has a header bar with icon buttons and a sidebar with two-line rows (type icon, name, group · type) and a count.
  - The detail pane scrolls. It has a header (icon, name, type · group), a labelled form in a card, Save and Copy on the left, Delete on the right, and readable UTC timestamps.
  - Settings are grouped in cards with a short explanation each.
- The window uses the app icon.
- `make screenshots` renders the README screenshots headlessly.

## Acceptance criteria
- [x] Contrast test for both variants: `TestVltTheme_TextIsReadableInBothVariants` (body ≥ 7:1, secondary and accent buttons ≥ 4.5:1)
- [x] `TestColumnLayout_WrappedTextIsNeverClipped` fails with the old VBox layout
- [x] List row, count and type icon tests: `TestListRow_ShowsNameGroupAndType`, `TestSecretList_CountFollowsTheSearch`, `TestTypeNamesAndIcons`, `TestCountText`
- [x] Lock still blanks every form entry, which now sit inside nested containers (`containedEntries` in the hygiene and detail tests)
- [x] `go test -race ./...` passes

## Outcome
Verification found and fixed:
- **Read-only text was just under WCAG AA in dark mode.** A saved secret's name and group are drawn in the secondary colour on the input background, at 4.48:1. The secondary grey is lighter now. The contrast test also checks read-only entries, disabled buttons (now a dimmer shade of their own) and placeholders (3:1; the light one was 2.85:1).
- **The lock test only checked entries it could already reach.** It now also walks inside widgets and fails if any entry sits outside the plain containers `blankEntries` walks; a field wrapped in a theme override proves it. It also checks that the group selector is cleared.
- The error screen was said to show the app icon; it shows an error icon.
- Records were called "Login" in the list but "Record" on the buttons. They are "Record" everywhere now.
- A secret with no group no longer shows a stray " · ".
- Smaller test fixes:
  - The column layout test also checks min height.
  - The count is checked after lock.
  - `TestTypeNamesAndIcons` uses a test app instead of running in parallel.
  - The screenshot test requires an absolute path and checks `Close`.
- The README shows the light or dark screenshot to match the reader's setting.
- DESIGN.md notes that the 256px icons are copies of `Icon.png`.

Accepted:
- **Read-only name and group keep a grey outline.** Fyne draws a disabled entry's border and text in the same colour.
- **The card column doesn't grow for content wider than its width.** Long paths wrap instead.

## Related
- [[Roadmap]]
- [[F20 App metadata, icon and local packaging]]
- [[F15 UI tests with fyne test]]
