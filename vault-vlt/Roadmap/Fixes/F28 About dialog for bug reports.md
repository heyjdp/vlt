---
id: F28
title: "About dialog for bug reports"
aliases:
  - F28
tags:
  - fix
severity: low
status: done
phase: 7
area: ui
review_finding: User request (2026-10-29)
files:
  - internal/ui/about.go
  - internal/ui/about_test.go
  - internal/ui/app.go
  - cmd/vlt/main.go
  - README.md
  - DESIGN.md
  - CHANGELOG.md
depends_on: []
created: 2026-10-29
---

# F28: About dialog for bug reports

## Problem
Nothing in the app said which build it was. A bug report could not say which version, commit or platform it came from.

## Fix
- **Where to find it:** an *About* button sits in the status bar, so it is on every screen, locked or unlocked. There is also an *About* menu item (*vlt → About vlt* on macOS, where Fyne replaces the system About panel with it; *Help → About* elsewhere) and *Help → Report a Bug…*.
- **What it shows:** the version (`main.version`, passed through `NewApp`) and the VCS revision and time (`-modified` for a dirty tree). It also shows the platform, Go version, Fyne version, whether the vault is local or on a server, and whether it is locked. It never shows secrets, paths or names.
- **Copy Details** puts that text on the clipboard.
- **Report a Bug…** opens a new GitHub issue with the details and a short template in the body.

## Acceptance criteria
- [x] `TestReadBuildDetails`, `TestBuildDetails_String`, `TestIssueURL_CarriesTheDetails`
- [x] `TestAbout_IsReachableWhileLockedAndCopiesTheDetails` (no vault path in the text), `TestAbout_IsInTheMainViewAndTheHelpMenu`
- [x] README *Reporting Bugs*, DESIGN, CHANGELOG
- [x] `go test -race ./...` passes

## Outcome
Verification found and fixed:
- **Two "About vlt" items on macOS.** Fyne v2.7.3 only moves a menu item into the macOS app menu when it is labelled exactly "About". The menu item is now "About": on macOS it replaces the system About panel as *vlt → About vlt*, and elsewhere it stays in Help (`TestAbout_TheAboutMenuItemOpensTheDialog`).
- ***Copy Details* during a clipboard countdown** kept the countdown running. It now stops it and confirms on the button ("Copied").
- **Tests that missed things.** 12 of 22 mutants survived the first tests. Tests now cover:
  - that a server vault's host, user and path never appear (`TestAbout_AServerVaultIsNotNamed`);
  - that *Report a Bug…* opens the issue page from the dialog and the menu, through a replaceable browser opener (`TestAbout_ReportABugOpensAnIssueWithTheDetails`);
  - that a failed browser is reported;
  - that the issue body round-trips `&`, `+` and `#`;
  - that the whole text is copied;
  - that `NewApp` keeps the version;
  - that `-modified` without a commit is dropped.
- **Release builds carry the commit.** `make build` and CI keep VCS stamping (`-trimpath` and `-s -w` don't remove it, and a shallow clone still has it), so they show their commit in About.

## Related
- [[Roadmap]]
- [[F23 UI makeover]]
