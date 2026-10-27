---
title: Roadmap
tags:
  - roadmap
  - moc
created: 2026-10-27
source: "[[2026-10-27 Code Review]]"
---

# Roadmap

The fix plan from the [[2026-10-27 Code Review]], in five phases. Earlier phases unblock or de-risk later ones. Each fix is its own note in `Roadmap/Fixes/`, and its `status` property drives the tracker below.

> [!important] Why storage goes first
> [[F01 Scrypt-wrapped identity]] changes the on-disk format. Doing it before anyone has real vaults avoids writing a migration under pressure, and it removes the root cause of two critical findings.

## Dependency graph

**Storage chain.** Everything that depends on the new key design.

```mermaid
flowchart TB
    F02["`F02 Atomic file writes`"] --> F01["`F01 Scrypt-wrapped identity`"]
    F01 --> F04["`F04 Confirm passphrase on vault creation`"]
    F01 --> F10["`F10 Keep vault IO off the UI goroutine`"]
    F01 --> F11["`F11 Memory hygiene and honest docs`"]
    classDef p1 fill:#e5484d22,stroke:#e5484d
    classDef p2 fill:#f76b1522,stroke:#f76b15
    classDef p3 fill:#ffc53d22,stroke:#ffc53d
    classDef p4 fill:#0090ff22,stroke:#0090ff
    classDef p5 fill:#30a46c22,stroke:#30a46c
    class F01,F02,F04 p1
    class F10 p2
    class F11 p3
    class F01,F02,F04,F10,F11 internal-link;
```

**UI test gate.** The behaviour these fixes add is what [[F15 UI tests with fyne test|F15]] tests.

```mermaid
flowchart LR
    F04["`F04 Confirm passphrase on vault creation`"] --> F15["`F15 UI tests with fyne test`"]
    F05["`F05 Refresh list after edit and delete`"] --> F15
    F07["`F07 Inactivity-based auto-lock`"] --> F15
    F12["`F12 Fix CI pipeline`"] --> F15
    classDef p1 fill:#e5484d22,stroke:#e5484d
    classDef p2 fill:#f76b1522,stroke:#f76b15
    classDef p3 fill:#ffc53d22,stroke:#ffc53d
    classDef p4 fill:#0090ff22,stroke:#0090ff
    classDef p5 fill:#30a46c22,stroke:#30a46c
    class F04 p1
    class F05,F07 p2
    class F12 p4
    class F15 p5
    class F04,F05,F07,F12,F15 internal-link;
```

Phase colours: <span style="color:#e5484d">■</span> 1 storage · <span style="color:#f76b15">■</span> 2 UI · <span style="color:#ffc53d">■</span> 3 memory · <span style="color:#0090ff">■</span> 4 CI · <span style="color:#30a46c">■</span> 5 tests. An arrow `A → B` means *do A before B*. Click a node to open its note.

The other fixes have no ordering constraints and can be picked up at any point within their phase: [[F03 Prevent silent overwrites]], [[F06 Persist settings]], [[F08 Correct clipboard clearing]], [[F09 Fail import on read errors]], [[F13 Project hygiene]], [[F14 Remove reinvented stdlib and dead code]].

## Phases

### Phase 1: Storage & data safety
Goal: no operation can lose or strand a secret, and unlock costs one scrypt.
- [[F02 Atomic file writes]]: prerequisite for a safe migration
- [[F01 Scrypt-wrapped identity]]: see [[ADR-001 Scrypt-wrapped X25519 identity|ADR-001]]
- [[F03 Prevent silent overwrites]]
- [[F04 Confirm passphrase on vault creation]]

### Phase 2: UI correctness
Goal: the UI never freezes, never shows stale data, and never discards edits.
- [[F10 Keep vault IO off the UI goroutine]]
- [[F05 Refresh list after edit and delete]]
- [[F07 Inactivity-based auto-lock]]
- [[F08 Correct clipboard clearing]]
- [[F09 Fail import on read errors]]
- [[F06 Persist settings]]

### Phase 3: Memory hygiene
- [[F11 Memory hygiene and honest docs]]

### Phase 4: CI & project hygiene
These can run in parallel with phases 1–3.
- [[F12 Fix CI pipeline]]
- [[F13 Project hygiene]]

### Phase 5: Quality & tests
- [[F14 Remove reinvented stdlib and dead code]]
- [[F15 UI tests with fyne test]]

## Tracker

![[Roadmap.base]]

## Progress

Each fix note's `status` property is one of `todo`, `in-progress`, or `done`. The **Progress** view in the tracker counts them by phase.

> [!success] All 15 fixes are done (2026-10-27)
> Every fix went through independent code, documentation and tooling verification before its commit, on branch `fix/review-roadmap`. Two things wait on the owner: the first CI run, which needs a push, and the `yourorg` module path and app ID rename ([[F13 Project hygiene]]).
