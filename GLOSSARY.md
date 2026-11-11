# vlt

vlt is a desktop secret manager: one person keeps passwords, logins and files in an encrypted vault on their computer or on their own server.

## Vault and its contents

**Vault**:
The folder that holds a person's encrypted secrets and their vault key, on this computer or on a server.
_Avoid_: database, store (in user-facing text), repository

**Vault key**:
The random key every secret is encrypted to; it is itself encrypted to the passphrase.
_Avoid_: identity, master key

**Passphrase**:
What the person chooses when creating a vault and types to unlock it; vlt never stores or keeps it.
_Avoid_: password (that is a kind of secret), master password

**Secret**:
One named, encrypted item in a vault: a password, a record or a file.
_Avoid_: entry, item, credential

**Record**:
A secret for signing in to a site: URL, username, password and, optionally, a TOTP secret.
_Avoid_: login, account

**Group**:
A named collection of secrets inside a vault, such as "Work" or "Personal".
_Avoid_: category, folder (for the group itself; it is stored as one)

**One-time code**:
The short-lived code made from a record's TOTP secret, as an authenticator app shows it.
_Avoid_: OTP, token, 2FA code

## Using a vault

**Session**:
The time between unlocking a vault and locking it, during which its secrets can be read.
_Avoid_: login, connection

**Unlock / Lock**:
Starting and ending a session. Locking forgets the vault key and the shown secrets; it happens on request, after inactivity (auto-lock) or on quit.
_Avoid_: log in/out; "close" for the session

**Open**:
To choose a vault to use, or, of another copy of vlt, to have it unlocked ("open elsewhere").
_Avoid_: using it to mean unlock

**Auto-lock**:
Locking that happens by itself after a period without input, usually preceded by a warning.
_Avoid_: timeout (on its own), sleep

**Open elsewhere**:
A vault another copy of vlt has unlocked, as recorded in its lock file; vlt asks before showing it, unless that copy was on this computer and has stopped running.
_Avoid_: locked (that means the session ended), in use

**Lock file**:
The plain-text note in a vault saying which copy of vlt has it unlocked. It warns; it never keeps anyone out.
_Avoid_: mutex; "lock" on its own in user-facing text

**Waiting vault**:
A vault opened with the passphrase but not yet shown, while the person decides whether to go on although it is open elsewhere; no session has started.
_Avoid_: pending store (in user-facing text)
