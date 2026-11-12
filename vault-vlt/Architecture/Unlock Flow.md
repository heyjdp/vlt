---
title: Unlock Flow
tags:
  - architecture
  - crypto
created: 2026-10-27
---

# Unlock Flow

## Current flow (after F10)

Opening a vault goes through one module, the `opener` (`internal/ui/opener.go`, [[F50 Opening a vault]]); `App` only maps what it reports to screens. `App.showUnlockScreen` asks the opener to `Look` at the location (`store.Exists`) and shows either the unlock screen or the create-vault screen. The passphrase entry is cleared as soon as it's read, and the passphrase is used once to unwrap `vlt.age` and then dropped. The opener's `Unlock` (existing vault) and `Create` (new vault) share one path: they run in the background via `runner.background` with the screen busy, return through `fyne.Do`, and report one outcome: the session has begun, the vault is waiting because it is open elsewhere, or why it did not open (wrong passphrase, vault gone, leftovers only, key file missing, another error). Whenever `App` replaces the screen, or vlt quits, it calls `Abandon`: a result still to come for the old screen is dropped, and a store opened or created meanwhile is let go.

For a vault on a server, `Exists` and `Open` each connect over the system ssh; the store keeps the connection until it is closed. `Open` and `Create` take `vlt.lock` before returning the store; if another copy of vlt holds it, the store says so (`OpenElsewhere`), the opened store becomes the Waiting vault, held by the opener; a dialog says who and since when, and *Open Anyway* takes the lock over (`TakeOver`) and only then begins the session, while *Cancel* lets the store go ([[F26 Vault lock file]], [[F27 Server vaults in the UI]], [[F49 Store owns its lock file]]). If the `Exists` check cannot reach the server, an error screen shows *Retry* and, unless `VLT_VAULT_DIR` sets the location or it already is the default, *Use the Vault on This Computer*. A server error while unlocking is shown on the unlock screen. An unanswered "Vault Already Open" question lets the Waiting vault go after the auto-lock timeout (its key zeroed at once, on the timer's goroutine), and so does quitting; every store the opener lets go is closed through the session, off the main goroutine, and quitting waits a bounded time for its release.

The create screen also offers *Use a Vault on a Server…* and *Open Existing Vault…* (unless `VLT_VAULT_DIR` sets the location). A server location is used as entered: its unlock screen if a vault is there, otherwise the create screen for it. For *Open Existing Vault…*, the opener checks the chosen folder with `store.Exists` in the background (`CheckFolder`). A folder without a vault is refused with an error. Otherwise the app switches to that folder and shows its unlock screen, and the location is saved only after the vault unlocks.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant A as App (UI thread)
    participant O as opener
    participant S as store
    A->>O: Look(dir)
    O-)S: Exists(dir)? (background)
    alt no vault
        opt Open Existing Vault…
            User->>A: folder
            A->>O: CheckFolder(folder)
            O-)S: Exists(folder)? (background)
            Note over A: vault found → unlock screen for that folder
        end
        User->>A: passphrase ×2 (create screen, ≥12 chars, must match)
        A->>O: Create(dir, passphrase)
        O-)S: Create (background)
        S->>S: Create: new X25519 key, wrap (scrypt), take vlt.lock
    else vault exists
        User->>A: passphrase
        A->>O: Unlock(dir, passphrase)
        O-)S: Open (background)
        S->>S: Open: unwrap vlt.age (1× scrypt)
        opt format-1 vault
            S->>S: migrate (1× scrypt per secret, once)
        end
        S->>S: take vlt.lock
    end
    alt vlt.lock held by another copy (OpenElsewhere)
        O->>A: waiting (holder); opener holds the Waiting vault
        A->>User: Vault Already Open: Open Anyway / Cancel
        A->>O: OpenAnyway
        O-)S: TakeOver (background)
        O->>A: opened (session begun)
    else not open elsewhere
        O->>A: opened (session begun)
    end
    A-)S: Contents(session ctx), background
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
- [[F50 Opening a vault]]
