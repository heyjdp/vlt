---
title: Unlock Flow
tags:
  - architecture
  - crypto
created: 2026-10-27
---

# Unlock Flow

## Current flow

`App.onUnlock` (`internal/ui/app.go:75`) runs on the Fyne main goroutine while holding `App.mu`.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant A as App (UI thread)
    participant S as Store
    User->>A: passphrase
    A->>A: NewIdentity
    A->>S: IsNew?
    alt no vlt.age
        S->>S: write sentinel
        Note right of S: no confirm ⚠ F04
    else vlt.age exists
        S->>S: decrypt sentinel (scrypt)
    end
    A-)S: List(ctx), goroutine
    loop each of n files
        S->>S: decrypt (scrypt)
    end
    S--)A: fyne.Do(update list)
```

> [!danger] The loop is the problem
> Every decrypt in the loop runs a full scrypt derivation (about 256 MiB of memory, order of a second). Opening the vault costs $n$ scrypts, and so does every list refresh after a save, which runs on the **UI goroutine**. See [[Scrypt Cost Model]].

## Proposed flow (after [[F01 Scrypt-wrapped identity]])

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant A as App
    participant V as Vault service
    User->>A: passphrase
    A-)V: Unlock(ctx) in background
    V->>V: read vlt.age, 1× scrypt
    Note right of V: yields X25519 identity
    loop each of n files
        V->>V: X25519 + ChaCha20 (µs)
    end
    V--)A: fyne.Do(show main view)
```

There's exactly one scrypt per unlock. The sentinel is replaced by the wrapped key file, so a wrong passphrase still fails fast on that one file.

## Related
- [[ADR-001 Scrypt-wrapped X25519 identity]]
- [[F04 Confirm passphrase on vault creation]]
- [[F10 Keep vault IO off the UI goroutine]]
