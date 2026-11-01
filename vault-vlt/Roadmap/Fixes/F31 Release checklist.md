---
id: F31
title: "Release checklist"
aliases:
  - F31
tags:
  - fix
severity: low
status: done
phase: 8
area: ci
review_finding: User request (2026-11-01)
files:
  - packaging/release-notes.sh
  - packaging/release-notes-test.sh
  - .github/workflows/ci.yml
  - Makefile
  - README.md
  - CHANGELOG.md
depends_on:
  - F29
  - F30
created: 2026-11-01
---

# F31: Release checklist

## Problem
Releasing was done from memory, with no written checklist. The GitHub release notes were GitHub's generated list of changes, not the CHANGELOG. They are what [[F29 Check for updates]] shows users. Nothing stopped a tag being pushed before the CHANGELOG was dated.

## Fix
- **README *Releasing*:** start from a green `main`, date the CHANGELOG, commit and push, then tag and push the tag. After that, check the release. If CI fails before the release is published, delete and re-tag. After publishing, never move a tag; fix forward.
- **`packaging/release-notes.sh <tag>`:**
  - prints the tag's `## [X.Y.Z] - YYYY-MM-DD` section;
  - fails on a non-release tag, a missing or undated section, or an empty one;
  - `make release-notes TAG=…` previews it.
- **CI:**
  - On a tag, lint checks the section before anything is packaged.
  - The release job publishes the section with `--notes-file`; a re-run also updates the notes.
  - `packaging/release-notes-test.sh` (`make test-release-notes`) runs in lint on every push. It covers the edge cases and checks that every published release (every release tag, which the lint job fetches) still has notes.

## Acceptance criteria
- [x] `release-notes-test.sh` passes; shellcheck, actionlint and zizmor are clean
- [x] README *Releasing*, CI job list, CHANGELOG

## Outcome
Verification found and fixed:
- **CI never checked old releases' notes.** The lint checkout was shallow, with no tags. It now fetches them all (`fetch-depth: 0`), and the test also names `v0.1.0`, so a CHANGELOG that loses a published release's section fails.
- **Script robustness:**
  - A second heading for the same version used to join two sections.
  - CRLF line endings are now accepted.
  - A section of only white space counts as empty.
  - The date is spelled `[0-9][0-9][0-9][0-9]` for older mawk.
  - A missing file gets a clear error.
  - A dead `sed` was removed.
- **Tests that missed things.** 13 mutants survived at first. New cases cover:
  - `refs/tags/…` passed in;
  - bad dates and a `###` heading;
  - any `##` heading ending the section, and duplicates;
  - CRLF;
  - usage exit codes;
  - the default `CHANGELOG.md`, the final newline, and a missing file.
  
  The test passes under bash 5.3, GNU sed, gawk and mawk as well as macOS's tools.
- **`make release-notes` echoed its command** before the notes. The recipe now starts with `@`.
- **Docs:**
  - The versioning rule now says what changes before 1.0: new features mean a minor release, so the next release is **0.2.0**.
  - Step 5 can be done when the previous version has no update check.
  - What CI does on a tag is described exactly; the three-OS build doesn't gate the release.
  - The lint job and tag trigger are listed.
  - Re-running a failed job is covered.
  - Pre-release tags are said to be unsupported.
  - The CHANGELOG no longer says the check fails "before anything is built", or blames the bundle ID for Gatekeeper's prompt.

## Related
- [[Roadmap]]
- [[F29 Check for updates]]
- [[F30 Dependabot]]
- [[F21 Release builds for macOS and Linux]]
