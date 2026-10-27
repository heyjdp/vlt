---
title: Unlock Flow
tags:
  - architecture
  - crypto
created: 2026-10-27
---

# Unlock Flow

## Current flow (after F10)

`App.showUnlockScreen` checks `store.Exists` first and shows either the unlock screen or the create-vault screen. `onUnlock` (existing vault) and `onCreate` (new vault) run in the background via `runner.background`. Their results return through `fyne.Do`, and a result for a screen that has since been replaced is discarded.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant A as App (UI thread)
    participant S as store
    A-)S: Exists(dir)? (background)
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
    A-)S: List(session ctx), background
    loop each of n files
        S->>S: X25519 decrypt (µs)
    end
    S--)A: fyne.Do(update list)
```

Since [[F01 Scrypt-wrapped identity]], unlock costs exactly one scrypt (measured by `TestOpen_RunsExactlyOneScrypt`), and listing is fast. See [[Scrypt Cost Model]].

> [!success] Off the UI thread since F10
> The unlock and create screens show a progress bar while scrypt runs. After unlock, all vault work uses a runner bound to the session context, so locking cancels it and drops late results. See [[F10 Keep vault IO off the UI goroutine]].

## Related
- [[ADR-001 Scrypt-wrapped X25519 identity]]
- [[F04 Confirm passphrase on vault creation]]
- [[F10 Keep vault IO off the UI goroutine]]
