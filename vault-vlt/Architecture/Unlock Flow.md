---
title: Unlock Flow
tags:
  - architecture
  - crypto
created: 2026-10-27
---

# Unlock Flow

## Current flow (after F01)

`App.showUnlockScreen` checks `store.Exists` first and shows either the unlock screen or the create-vault screen. `onUnlock` (existing vault) and `onCreate` (new vault) run on the Fyne main goroutine while holding `App.mu`.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant A as App (UI thread)
    participant S as store
    A->>S: Exists(dir)?
    alt no vault
        User->>A: passphrase ×2 (create screen, ≥12 chars, must match)
        S->>S: Create: new X25519 key, wrap (scrypt)
    else vault exists
        User->>A: passphrase
        S->>S: Open: unwrap vlt.age (1× scrypt)
        opt format-1 vault
            S->>S: migrate (1× scrypt per secret, once)
        end
    end
    A-)S: List(ctx), goroutine
    loop each of n files
        S->>S: X25519 decrypt (µs)
    end
    S--)A: fyne.Do(update list)
```

Since [[F01 Scrypt-wrapped identity]], unlock costs exactly one scrypt (measured by `TestOpen_RunsExactlyOneScrypt`), and listing is fast. See [[Scrypt Cost Model]].

> [!warning] Still on the UI thread
> `Open` (one scrypt, or a whole migration on first unlock of a format-1 vault) still runs on the main goroutine, so the window freezes for about a second, or longer during migration. Moving it to the background is [[F10 Keep vault IO off the UI goroutine]].

## Target flow (after F10)

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant A as App
    participant V as store
    User->>A: passphrase
    A-)V: Open(ctx) in background
    V->>V: read vlt.age, 1× scrypt
    Note right of V: yields X25519 identity
    V--)A: fyne.Do(show main view)
```

## Related
- [[ADR-001 Scrypt-wrapped X25519 identity]]
- [[F04 Confirm passphrase on vault creation]]
- [[F10 Keep vault IO off the UI goroutine]]
