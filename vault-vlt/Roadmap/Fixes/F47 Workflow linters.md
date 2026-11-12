---
id: F47
title: "Workflow linters"
aliases:
  - F47
tags:
  - fix
severity: low
status: done
phase: 12
area: ci
review_finding: Go skills audit 2026-11-11, item 10
files:
  - .github/workflows/ci.yml
  - .lefthook.yml
  - Makefile
  - README.md
  - CLAUDE.md
  - CHANGELOG.md
depends_on:
  - F30
created: 2026-11-12
---

# F47: Workflow linters

## Problem
- **actionlint and zizmor ran nowhere.** README said "`actionlint` and `zizmor` report no findings" and CLAUDE.md lists both for pre-commit and CI, but neither was in `ci.yml` or `.lefthook.yml`. A workflow change that broke the hardening (an unpinned action, a template injection, persisted credentials) would pass CI. shellcheck ran only in the pre-commit hook.
- **The pre-commit hook rewrote files without staging them.** `golangci-lint run --fix` fixed the working tree, but the commit took the unfixed staged copy.
- **`make check` was not what CI runs.** It started with `fmt` (rewriting files), and left out the release-notes tests, the 10-minute test timeout, and any check of `go.mod`/`go.sum`, which CI didn't check either.

## Fix
- **A `workflow-lint` job** runs actionlint (`ACTIONLINT_VERSION`, built with `go install`, which checks the Go checksum database; it shellchecks every `run:` block with the runner's shellcheck), zizmor through `zizmorcore/zizmor-action` pinned by SHA (`ZIZMOR_VERSION`, installed from a hash-pinned wheel; regular persona; online audits with the read-only job token; `advanced-security: false`, so findings are annotations and no `security-events: write` is needed), and shellcheck on `packaging/*.sh` and `packaging/*/*.sh`. The packaging jobs now also wait for it.
- **The test job checks the module files**: `go mod tidy -diff` and `go mod verify`.
- **Hooks**: pre-commit runs actionlint on staged workflows and zizmor on staged workflows and `dependabot.yml`; golangci-lint runs without `--fix`; pre-push also runs golangci-lint.
- **`make check`** is `vet lint mod-check test-release-notes workflow-lint test-race vuln`, never rewriting a file; `make workflow-lint` and `make mod-check` run those parts alone.
- **Decision: hooks fail when a tool is missing** rather than skipping it, like the shellcheck and detect-secrets hooks already did; they only run when a workflow is staged. README lists how to install each tool.
- **Decision: drop `--fix` rather than add `stage_fixed`.** The format hook runs `golangci-lint fmt --diff` in parallel, and a hook that rewrites the staged copy can commit changes nobody looked at; `make fmt` formats.
- **Decision: actionlint via `go install`** rather than a release binary with a hand-kept checksum: one pinned version, and the checksum database does the verification.

## Outcome
Three independent verifiers checked the branch (config, tests, docs):
- **Config**: every action SHA matches its version tag; with zizmor's `advanced-security: false` nothing is uploaded, so `contents: read` suffices; `filepath.FromSlash` is the right fix for Fyne's slash paths; the removed `//nolint` directives were truly dead.
- **Tests**: no test is expected to fail on Windows.
- **Docs**: CLAUDE.md's Makefile, go.mod, golangci-lint and static-analysis sections, README's tool table, test and hook wording, and the CHANGELOG section were out of date; fixed in a follow-up `docs:` commit, and `workflow-lint` got a 10-minute job limit.

Residual risk: zizmor's online audits (impostor commits, known-vulnerable actions) and the job itself have only run locally and offline.

Done once the first CI run on GitHub is green on all three OSes.

**First CI runs (2026-11-12):** the `workflow-lint` job (actionlint, zizmor with online audits, shellcheck) passed on both pushes.

## Acceptance criteria
- [x] actionlint v1.7.12, zizmor 1.32.0 (regular and auditor personas: only informational "job has no name") and shellcheck 0.11.0 report nothing locally
- [x] `lefthook validate` passes; the actionlint, zizmor and shellcheck hooks pick up the right files (`lefthook run pre-commit --all-files`)
- [x] `make check` passes locally with the pinned tools
- [x] The workflow-lint job passes on GitHub, including zizmor's online audits (only checkable once pushed)

## Related
- [[Roadmap]]
- [[F30 Dependabot]]
- [[F46 Tests on every OS]]
- [[F48 nolintlint]]
