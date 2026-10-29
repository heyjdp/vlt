---
title: Unlock Flow
tags:
  - architecture
  - crypto
created: 2026-10-27
---

# Unlock Flow

## Current flow (after F10)

`App.showUnlockScreen` checks `store.Exists` first and shows either the unlock screen or the create-vault screen. The passphrase entry is cleared as soon as it's read, and the passphrase is used once to unwrap `vlt.age` and then dropped. `onUnlock` (existing vault) and `onCreate` (new vault) run in the background via `runner.background`. Their results return through `fyne.Do`, and a result for a screen that has since been replaced is discarded.

For a vault on a server, `Exists` and `Open` each connect over the system ssh; the store keeps the connection until it is closed. Once a vault opens (or is created), the app takes `vlt.lock`: if another copy of vlt holds it, a dialog says who and since when, and *Open Anyway* takes it over while *Cancel* closes the store ([[F26 Vault lock file]], [[F27 Server vaults in the UI]]). If the `Exists` check cannot reach the server, an error screen shows *Retry* and, unless `VLT_VAULT_DIR` sets the location or it already is the default, *Use the Vault on This Computer*. A server error while unlocking is shown on the unlock screen. An unanswered "Vault Already Open" question closes the vault after the auto-lock timeout.

The create screen also offers *Use a Vault on a Server…* and *Open Existing Vault…* (unless `VLT_VAULT_DIR` sets the location). A server location is used as entered: its unlock screen if a vault is there, otherwise the create screen for it. For *Open Existing Vault…*, the chosen folder is checked with `store.Exists` in the background. A folder without a vault is refused with an error. Otherwise the app switches to that folder and shows its unlock screen, and the location is saved only after the vault unlocks.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant A as App (UI thread)
    participant S as store
    A-)S: Exists(dir)? (background)
    alt no vault
        opt Open Existing Vault…
            User->>A: folder
            A-)S: Exists(folder)? (background)
            Note over A: vault found → unlock screen for that folder
        end
        User->>A: passphrase ×2 (create screen, ≥12 chars, must match)
        S->>S: Create: new X25519 key, wrap (scrypt)
    else vault exists
        User->>A: passphrase
        S->>S: Open: unwrap vlt.age (1× scrypt)
        opt format-1 vault
            S->>S: migrate (1× scrypt per secret, once)
        end
    end
    A-)S: AcquireLock (background)
    opt vlt.lock held by another copy
        A->>User: Vault Already Open: Open Anyway / Cancel
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
