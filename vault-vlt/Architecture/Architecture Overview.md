---
title: Architecture Overview
tags:
  - architecture
created: 2026-10-27
---

# Architecture Overview

vlt follows the layered layout from the project's `CLAUDE.md`: a thin Fyne interface layer over a storage layer over an age-based crypto layer. Package boundaries are enforced with `internal/`.

## Layers

```mermaid
graph TD
    subgraph Interface["Interface layer"]
        main["cmd/vlt/main.go<br/>config, logging, Fyne bootstrap"]
        ui["internal/ui<br/>App, UnlockScreen, SecretList,<br/>DetailView, FileOps, Settings, Theme"]
    end
    subgraph Domain["Domain + storage"]
        store["internal/store<br/>Secret envelope, Store CRUD,<br/>sentinel, re-encrypt, copy"]
    end
    subgraph Infra["Infrastructure"]
        crypto["internal/crypto<br/>Identity (memguard), Encrypt/Decrypt"]
        age[(filippo.io/age)]
        mg[(awnumar/memguard)]
        fs[(filesystem)]
    end

    main --> ui
    ui --> store
    ui --> crypto
    store --> crypto
    store --> fs
    crypto --> age
    crypto --> mg
```

> [!warning] Layering leak
> `internal/ui` imports `internal/crypto` directly: `App.onUnlock` and the change-passphrase flow in `SettingsScreen` build identities themselves. Ideally the UI talks only to an application-level vault service that owns identity lifecycle. That becomes natural once [[F01 Scrypt-wrapped identity]] lands.

## Packages

| Package | Responsibility | Key files |
|---|---|---|
| `cmd/vlt` | Read env config, set up `slog`, create the Fyne app and window | `main.go` |
| `internal/crypto` | Derive an age scrypt identity from the passphrase; encrypt and decrypt byte slices | `identity.go`, `vault.go` |
| `internal/store` | `Secret` JSON envelope, slugs and groups, directory CRUD, sentinel verification, re-encrypt, copy | `secret.go`, `store.go` |
| `internal/ui` | Lock state machine, screens, clipboard TTL, file import/export, settings | `app.go`, `detail.go`, `list.go`, `fileops.go`, `settings.go` |
| `internal/testutil` | Test identities and secret builders | `fixtures.go` |

## Lock state machine

```mermaid
stateDiagram-v2
    [*] --> Locked
    Locked --> Unlocking: passphrase submitted
    Unlocking --> Locked: wrong passphrase / I/O error
    Unlocking --> Unlocked: sentinel verified (or created)
    Unlocked --> Locked: Lock button
    Unlocked --> Locked: auto-lock timer fires
    Unlocked --> Locked: passphrase changed / vault moved / switched
    Unlocked --> Unlocked: toolbar click resets timer
```

The auto-lock timer only resets on toolbar clicks. See [[F07 Inactivity-based auto-lock]].

## Dependencies

| Module | Purpose | CGO |
|---|---|---|
| `filippo.io/age` v1.3.1 | File encryption | no |
| `fyne.io/fyne/v2` v2.7.3 | GUI | **yes** (desktop driver uses go-gl/glfw) |
| `github.com/awnumar/memguard` v0.23.0 | Locked memory buffers | no |

Because Fyne's desktop driver needs CGO, the CI cross-build matrix with `CGO_ENABLED=0` can't work. See [[F12 Fix CI pipeline]].

## Related
- [[Unlock Flow]]
- [[Storage Format]]
- [[Security Model]]
