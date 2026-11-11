# vlt — Age-based Secret Manager
## Design Document v0.1

---

## Overview

`vlt` is a cross-platform GUI secret manager using [age](https://age-encryption.org/) encryption. It replaces ad-hoc PGP-encrypted text file workflows with a structured, portable vault based on a directory of per-secret `.age` files, unlocked by a single passphrase.

Target platforms: **Linux, macOS, Windows, OpenBSD**

---

## Identity & Unlock

- **Mechanism:** a random age X25519 identity (the *vault key*) encrypts every secret; the vault key is stored in `vlt.age`, encrypted to the passphrase with age's scrypt recipient (`age.NewScryptRecipient`). See ADR-001 in the project vault (`vault-vlt/Decisions/`).
- Unlock runs exactly one scrypt derivation (decrypting `vlt.age`); secret decryption is X25519 and costs microseconds
- A passphrase-protected file whose scrypt work factor is above 2^20 (`crypto.MaxWorkFactor`; vlt writes 2^18) is refused before any derivation runs (`crypto.ErrWorkFactorTooHigh`), and age is given the same cap. Whoever can write `vlt.age` (a server, a shared folder) could otherwise make every unlock, and every file a migration reads, cost gigabytes of memory and many seconds
- No key material leaves the vault directory, and the only key on disk is passphrase-encrypted
- The vault key lives in process memory (memguard) and is zeroed on lock or session timeout
- Changing the passphrase atomically rewrites only `vlt.age`
- `vlt.json` records the format version (2); format-1 vaults (secrets encrypted directly to the passphrase) are migrated on first unlock, crash-safely
- The vault directory is fully portable — copy it anywhere and unlock with the same passphrase

---

## Directory Layout

The vault is a directory of `.age` files organised by group. The default vault location follows OS conventions:

| OS      | Default Path                              |
|---------|-------------------------------------------|
| Linux   | `$XDG_DATA_HOME/vlt/vault/` (default `~/.local/share/vlt/vault/`) |
| macOS   | `~/Library/Application Support/vlt/vault/`|
| Windows | `%APPDATA%\vlt\vault\`                    |
| OpenBSD | `$XDG_DATA_HOME/vlt/vault/` (default `~/.local/share/vlt/vault/`) |

On Linux and OpenBSD a relative `XDG_DATA_HOME` is ignored, and a vault already in `~/.local/share/vlt/vault/` is kept when the XDG location holds none.

### Vault Structure

```
vault/
├── vlt.age
├── vlt.json
├── default/
│   └── wifi.age
├── personal-finance/
│   └── bank.age
└── work/
    ├── github-api-key.age
    ├── work-jira.age
    └── server-id-ed25519.age
```

- Groups are plain subdirectories named by the slugified group name; the envelope keeps the name as typed — no database, no index file
- Filenames are human-readable and correspond to the secret name (slugified)
- The structure is trivially Git-compatible

---

## Secret Envelope Format

Every `.age` file decrypts to a **JSON envelope**. All four secret types share this structure, differentiated by the `type` field.

### Common Fields

```json
{
  "version": 1,
  "type": "<password|record|file|key>",
  "name": "Human-readable name",
  "group": "Group name as typed",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["tag1", "tag2"],
  "notes": "Optional free-text notes"
}
```

### Type: `password`

For passwords, API keys, tokens, and other single-value text secrets.

```json
{
  "version": 1,
  "type": "password",
  "name": "GitHub API Key",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["github", "ci"],
  "notes": "Expires 2027-03-14",
  "value": "ghp_xxxxxxxxxxxx"
}
```

### Type: `record`

For structured login records with URL, username, password, and optional TOTP. `totp_secret` is the base32 secret a site shows when two-factor authentication is set up, or the `otpauth://totp/…` link from its QR code (which may set 6–8 digits, a period of 1–300 s and SHA-1, SHA-256 or SHA-512), stored trimmed as entered. The UI refuses to save a new or changed one it can't read (an unchanged one saved earlier is left alone, so the record's other fields can still be edited); an empty value means no TOTP.

```json
{
  "version": 1,
  "type": "record",
  "name": "Work Jira",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["work"],
  "notes": "",
  "username": "me@example.com",
  "password": "xxx",
  "url": "https://jira.example.com",
  "totp_secret": ""
}
```

### Type: `file`

For arbitrary binary files, SSH private keys, TLS certificates, etc. File content is base64-encoded inside the JSON envelope. Size limit: **10 MiB** per file secret.

```json
{
  "version": 1,
  "type": "file",
  "name": "Server ED25519 Key",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["ssh", "server"],
  "notes": "",
  "original_filename": "id_ed25519",
  "mime_type": "application/octet-stream",
  "encoding": "base64",
  "value": "LS0tLS1CRUdJTi..."
}
```

SSH keys and TLS certificates use the same `file` type. No tar-in-age complexity is required as files are small enough for base64-in-JSON.

---

## GUI Framework

**Choice: Go + Fyne**

| Criterion             | Go + Fyne                        | Python + PySide6                  |
|-----------------------|----------------------------------|-----------------------------------|
| Age library           | Native (`filippo.io/age`)        | `pyrage` wrapper                  |
| OpenBSD support       | ✅ Go + Fyne available via ports | ⚠️ Qt6 ports incomplete           |
| Distribution          | Single binary (links system OpenGL) | PyInstaller or bundled runtime |
| Cross-compilation     | Native build per OS (cgo); release packages built natively in CI | Complex             |
| Secure memory         | `memguard` or manual zeroing     | Harder to guarantee               |

### Key Dependencies

```
filippo.io/age          — encryption/decryption
fyne.io/fyne/v2         — cross-platform GUI
github.com/awnumar/memguard — secure in-memory identity storage
```

---

## Application Architecture

```
vlt/
├── cmd/vlt/
│   └── main.go              # Entry point, Fyne app bootstrap
├── internal/
│   ├── crypto/
│   │   ├── identity.go      # X25519 vault key (memguard); wrap/unwrap with passphrase
│   │   └── vault.go         # Encrypt/decrypt to the vault key
│   ├── store/
│   │   ├── store.go         # Secret CRUD, contents, copy
│   │   ├── layout.go        # Directory walker: which files are secrets; SecretID, GroupID
│   │   ├── vault.go         # Create/Open/ChangePassphrase, format marker
│   │   ├── migrate.go       # Crash-safe format-1 → format-2 migration
│   │   ├── atomic.go        # Atomic file writes
│   │   ├── location.go      # Vault locations (local, host:path, ssh://)
│   │   ├── fs.go            # Storage interface; local file system
│   │   ├── sshfs.go         # SFTP storage over the system's ssh
│   │   ├── lock.go          # vlt.lock (who has the vault open)
│   │   └── secret.go        # Secret types and JSON envelope schema
│   ├── totp/
│   │   └── totp.go          # RFC 6238 one-time codes
│   ├── update/
│   │   └── update.go        # Latest release from GitHub; version comparison
│   └── ui/
│       ├── app.go           # Fyne app and window bootstrap, screen switching
│       ├── session.go       # Vault session: open/waiting store, auto-lock
│       ├── about.go         # About dialog: build details, Copy Details, Report a Bug
│       ├── updates.go       # Check for Updates (on request; at start-up if enabled)
│       ├── totp.go          # One-time code panel for records
│       ├── unlock.go        # Passphrase entry screen (existing vault)
│       ├── create.go        # Create-vault screen (passphrase + confirmation, open existing)
│       ├── runner.go        # Background execution of vault I/O
│       ├── operations.go    # Operations with feedback: progress, re-entry, confirmations, file choosers
│       ├── picker.go        # File and folder choosers (Fyne's dialogs; a fake in tests)
│       ├── errmsg.go        # Errors in words for people (every error the UI shows)
│       ├── activity.go      # Input tracking for auto-lock
│       ├── clipboard.go     # Clipboard copy with auto-clear
│       ├── config.go        # Settings module: load, clamp, env lock, save; proposed/committed vault location
│       ├── list.go          # Secret browser (left panel)
│       ├── detail.go        # Secret view/edit (right panel)
│       ├── settings.go      # Vault path, lock timeout, clipboard TTL, update check
│       ├── style.go         # Cards, headings, empty states, type icons
│       ├── theme.go         # Teal accent, light/dark palettes, text sizes
│       └── icon.png         # App icon (256px), embedded
├── packaging/
│   ├── macos/               # vlt.app bundler: Info.plist template, build-app.sh
│   └── linux/               # AppImage build script, desktop entry, 256px icon
├── Icon.png                 # App icon (1024×1024); the 256px copies in internal/ui and packaging/linux come from it (sips -z 256 256 Icon.png --out …)
└── go.mod
```

### Package Responsibilities

**`internal/crypto`**
- `identity.go` — generates the X25519 vault key and holds it in a `memguard.LockedBuffer`; `WrapIdentity`/`UnwrapIdentity` encrypt it to and from the passphrase (scrypt); `DecryptWithPassphrase` reads `vlt.age` and legacy files, refusing a work factor above `MaxWorkFactor` before deriving; `Zero()` wipes on lock
- `vault.go` — `Encrypt(identity, envelope) ([]byte, error)` and `Decrypt(identity, ciphertext) ([]byte, error)` to the vault key

**`internal/store`**
- `secret.go` — defines `Secret`, `PasswordSecret`, `RecordSecret`, `FileSecret` structs; JSON marshalling/unmarshalling; filename slugification
- `store.go` — `Contents()`, `Get(name, group)`, `Create(secret)`, `Update(secret)`, `Delete(name, group)`, `CopyTo(location)` operating on the vault location. `CopyTo` copies `vlt.json`, then the secret files, and `vlt.age` last, checking its context before each file, so a copy stopped part-way (a lock, a lost connection) has no key file: `Exists` still reports a vault there (so nothing is created over it), and `Open` reports `ErrKeyMissing` instead of showing a vault with secrets missing. Copying to the same place again finishes such a copy: a destination without `vlt.age` or `vlt.migrating.age` whose secret files (and `vlt.json`, if present) are byte for byte this vault's holds nothing the copy would lose, so it is completed; any other destination holding a vault or secret files is refused with `ErrVaultExists`. `Create` never overwrites: it fails with `ErrSecretExists` if the slugified path is taken, as an `*ExistsError` carrying the secret already there (or, if that file can't be read, why), so import asks before replacing without reading the vault again. `Update` requires the stored secret to have exactly the same name
- `vault.go` — `Create`, `Open`, `Exists`, `ChangePassphrase`, `Close`; reads and writes `vlt.age` (wrapped vault key) and `vlt.json` (format marker)
- `migrate.go` — in-place, resumable migration of format-1 vaults on first unlock
- `layout.go` — the one rule for which files are secret files (`groupFolders`), used by `Contents`, `CopyTo`, `Exists` and migration alike: a secret file is a `.age` file in a group folder, a group folder is a top-level folder whose name does not start with a dot, and a folder holding `vlt.json` or `vlt.migrating.age` is another vault kept inside this one (a backup copy, say), never a group, so it is not listed, copied or migrated. `SecretID` (`Secret.ID()`) is the rule for which file a secret lives in, as a comparable value: names differing only in case or punctuation, and differently spelled labels of one group, give equal IDs. `GroupID` (`GroupIDOf(label)`, `SecretID.Group()`) identifies a group's folder, and `GroupID.Contains` matches a search against it. The UI compares these IDs (to update a row, keep a highlight, list one label per group, file a secret under an existing spelling) instead of slugifying names itself
- `atomic.go` — temp-file + fsync + rename writes used for every local vault file
- `fs.go` — `vaultFS`, the storage every other store file goes through. Its names are slash-separated paths relative to the vault directory, so the store never builds OS- or server-specific paths. It promises atomic writes (old or new contents, never partial), exclusive creates (never replace), an atomic rename, modes 0600/0700, and names sorted as `os.ReadDir` sorts them. `localFS` implements it with `atomic.go`. Directory walks skip dot-folders (`isGroupDir`, see `layout.go`): group directories are slugs, so a dot-folder is never a group
- `location.go` — `ParseLocation` reads a local path, `[user@]host:path` or `ssh://[user@]host[:port]/path`, and `Location.String` writes the form it reads back. Host and user may hold only letters, digits, `.`, `_` and `-` (not first; IPv6 hosts, which must parse as an IPv6 address, also `:` and `%`): ssh can substitute them into commands from the user's ssh config (`%h`, `%u`), and OpenSSH before 9.6 does not reject shell characters itself
- `sshfs.go` — `remoteFS`, the SFTP implementation. `dialSSH` runs the system's `ssh -T -x -a -o BatchMode=yes -o ClearAllForwardings=yes -o PermitLocalCommand=no -o RemoteCommand=none … -s -- host sftp` (with `ConnectTimeout`, `ServerAlive*`, and `WaitDelay` so a stuck ProxyJump child cannot hold up `Wait`; on Windows without a console window) and speaks SFTP over its pipes with `github.com/pkg/sftp`. `~/.ssh/config`, keys, the agent and `known_hosts` all apply, and vlt never handles SSH credentials; it passes no host-key options, so a config that sets `StrictHostKeyChecking=accept-new` or `no` is honoured. When ssh fails, its stderr is shown. Writes keep the local guarantees: temp files are created in `.vlt-tmp`, a private folder (made 0700, or tightened if loosened, once per session; temp files over an hour old are removed), so no other user can open one in the moment before its own 0600 mode is set (SFTP creates files with the server's umask). A temp file is fsynced when the server offers `fsync@openssh.com`, then renamed with `posix-rename@openssh.com` (atomic replace) or hard-linked with `hardlink@openssh.com` (exclusive create; SFTP gives no reason for a failed link, so the target is checked to report `ErrExist`, and where the server's file system cannot link, an exclusive `O_EXCL` create is the fallback, as locally). A server without `posix-rename` and `hardlink` is refused (`ErrServerUnsupported`). Directories cannot be fsynced over SFTP, so a crash of the server can undo the most recent rename (leaving the previous version), never leave a partial file. Listings are sorted by name and drop names that aren't a single path element, so a hostile server cannot steer a copy out of the destination. A dropped connection fails the operation that saw it (`ErrConnectionLost`, never retried, since a write may have landed) and the next operation reconnects; each session records its own extensions. `Store.Close` zeroes the key at once and closes the connection in the background, so a dead network never holds up locking
- `lock.go` — `AcquireLock` creates `vlt.lock` exclusively with this host, user, PID, time and a random token (retrying if the lock disappears in between). A lock held elsewhere is returned as `*InUseError`, which the UI shows before asking whether to take it over; so is one that exists but won't parse or that this user may not read (holder unknown). A dropped connection while reading it is retried (the next call reconnects); a lock that still can't be read (the connection stays down, an I/O error) is an ordinary error, which the UI logs before opening the vault, as for a failure to write the lock. A lock from this host whose process is gone (a crash), or from this process, is taken over silently; hosts are told apart by name only, so two computers with the same host name count as one. `Close` releases the lock by renaming it aside atomically, then checking the token: a lock that turns out to be someone else's is put back unless a newer one has appeared. The lock is advisory and holds no secrets; failing to write it is logged and does not stop an unlock
- `Contents` reads up to eight secret files in parallel (keeping directory order), which hides the per-file round trip to a server. A file it cannot read, decrypt or parse (or a group folder it cannot read) is logged and skipped, and named in `Contents.Unreadable`, so the UI can say so; a lost connection fails the whole listing rather than showing a partial one

**`internal/totp`**
- `totp.go` — `Parse` reads a record's TOTP secret: base32 (white space, including non-breaking spaces, and hyphens removed, upper-cased, padding optional) or an `otpauth://totp/` link. The scheme, type, parameter names and algorithm are case-insensitive. The label is not parsed (so a raw `%` or `#` in it is harmless); the query is read with `url.ParseQuery`. `secret` is required; `digits` is 6–8, `period` 1–300 s and `algorithm` SHA1/SHA256/SHA512, with defaults of 6, 30 s and SHA1. Other parameters (`issuer`, …) are ignored, except `encoder` (Steam Guard's `encoder=steam`), which is refused. HOTP links and other types are refused with a reason; the errors are written for the user and carry no package prefix. `Key.Code(t)` is RFC 6238 (HMAC, dynamic truncation, zero-padded), with the period counted from the epoch rounding down, so times before 1970 and after 2262 work. `Key.Remaining(t)` is the time left in the current period, and `Key.Wipe` zeroes the decoded secret. Standard library only

**`internal/update`**
- `update.go` — `Checker.Latest` makes one GET to `api.github.com/repos/heyjdp/vlt/releases/latest` (15 s timeout, the usual proxy variables honoured, redirects followed only over HTTPS to `api.github.com`, at most 1 MiB read) and returns the tag and the release notes, without control or format characters (such as bidirectional overrides) and cut to 2000 characters. The tag must be a plain `vX.Y.Z`; the release page link is built from it (`Release.PageURL`) rather than taken from the answer, so it can only point at vlt's own releases. 404 is `ErrNoRelease`; 429, and 403 with `X-RateLimit-Remaining: 0`, are `ErrRateLimited` (the unauthenticated API allows 60 requests an hour); any other 403 is reported as GitHub's answer. `IsNewer` compares the running version, as `git describe` names it, with a tag: `v0.1.0-5-gabc1234` (commits after v0.1.0) is older than v0.1.1, a pre-release such as `v0.2.0-rc1` is older than v0.2.0, `-dirty` is ignored, and a `dev` or tagless build cannot be compared

**`internal/ui`**
- `unlock.go` — passphrase entry for an existing vault, naming the vault's location (a local path in the home folder as `~/…`); `App` calls `store.Open`, takes the vault's lock, and transitions to the main view
- `create.go` — shown instead of `unlock.go` when `store.Exists` is false: displays the target directory, takes the passphrase twice (at least 12 characters excluding surrounding spaces; five or more random words recommended), then `App` calls `store.Create`. If `store.Exists` returns an error (directory unreadable), an error screen with Retry is shown instead of either screen. Unless `VLT_VAULT_DIR` sets the location, it also offers *Open Existing Vault…*: a folder picker whose choice is checked with `store.Exists` and refused if it holds no vault; otherwise `App` switches to it and shows the unlock screen, saving the location after the next successful unlock. It also offers *Use a Vault on a Server…*: a "Vault on a Server" dialog whose entry must parse as a remote location; *Use This Location* switches to it, showing its unlock screen if a vault is there and the create screen for it if not. The error screen says "Cannot reach the vault's server" for server errors and, unless `VLT_VAULT_DIR` sets the location or it already is the default, offers *Use the Vault on This Computer*
- `runner.go` — every blocking store call (unlock/create scrypt, the folder check for Open Existing Vault, list, save, delete, import, passphrase change, vault copy) runs off the Fyne main goroutine via `background`, which delivers the result with `fyne.Do`. Unlock and create show a progress bar with input disabled; what the user asks of the open vault goes through `operations` (below). After unlock, components share a runner bound to the session context: `Lock` cancels it, store calls receive it, and any result that arrives after the lock is dropped rather than shown or acted on. An unlock, create or open-existing result for a screen that has since been replaced is discarded (and any opened store closed). Saves, deletes and imports update the list row in place instead of re-listing (changes made during the first load are replayed onto it). Tests inject an inline or queued runner so app-level tests are deterministic
- `operations.go` — `operations`, the one module that runs what the user asks for, with feedback. `start(ops, &operation{…}, work, done)` runs work through `background` (so a result arriving after a lock is dropped), shows progress (status-bar text, or a dialog for the update check), disables the control that started it, refuses an operation whose kind (`opKind`) is already running with "Please wait for the current operation to finish" in the status bar (saving and deleting a secret share a kind, so they never interleave; changing the passphrase, moving the vault and switching vaults share another, so none cuts into a copy or a re-wrap of `vlt.age`; a second update check is ignored). A failure from `start` is shown with `showError(doing, err)` unless `failed` is set (the caller shows it itself: Settings under its form, a save explaining a name already taken). `startOwned` is `start` with a `release` for a result dropped at a lock: the export passes `crypto.Wipe`, so decoded plaintext that finishes just as the vault locks is wiped rather than lost. `now` applies the same refusal to an action with nothing to wait for (a switch, which locks at once). `confirm` asks before an action; the answer, and input in the dialog's content (the import's group form), counts as activity for the auto-lock, since dialogs are outside the tracked widget tree. `pickFile`, `pickFolder` and `pickSave` ask for a file to open, a folder or where to save, and count the answer as activity too: a cancel does nothing, a failure is shown, and an answer arriving after the session ended is dropped (a chosen file closed). `pickSave` answers exactly once, with nil when nothing is to be written (cancelled, failed, or the vault locked with the dialog open: `end`, called when the session is dropped, answers open requests, since the lock dismisses dialogs without an answer), so the export can wipe its plaintext in one place. The app keeps one for its whole life (update checks, which also run while locked, and the release dialog) and makes one per session, bound to its context, for everything done to the open vault: save, delete, import, export, passphrase change, vault copy. The list's own load is not one: it keeps its in-flight state for replaying saves made meanwhile. Tests drive it on a queued runner
- `picker.go` — `picker`, the seam behind the file and folder choosers: `openFile`, `openFolder` and `saveFile` (which suggests a name and filters by its extension). `fynePicker` shows Fyne's dialogs; tests use a scripted picker whose requests the test answers or leaves open, so import, export, Settings' *Browse…* and the auto-lock's view of them are tested through their buttons rather than through the code behind the dialogs
- `list.go` — secret list; filters by search query; fires selection events. Under the list, a notice explains a failed load (with *Retry*) or counts the secret files and group folders `Contents` skipped (with *Details…* listing their paths). *Retry* is disabled while a load runs: a second load would drop the first's replay of saves made meanwhile. `Upsert`/`Remove` update rows in place, keep the current filter, and keep the highlight on the selected secret (moving it if its row moves, clearing it if the secret is deleted or filtered out)
- `detail.go` — renders the correct form for the secret type; its copy buttons call the app's `clipboardClearer`. The form sits in a vertical scroll so long records never clip
- `theme.go` / `style.go` — `VltTheme` sets a teal accent (the icon's) on neutral light and dark palettes, rounded inputs and text sizes; `style.go` builds every screen from plain containers (cards, headings, empty states), never wrapping widgets, so the activity tracker and the lock-time blanking of entries still reach every field. A theme test checks WCAG contrast for body text, secondary text (including read-only entries) and accent-button text in both variants
- `clipboard.go` — one `clipboardClearer` per app: per-copy countdown, clears only vlt's own value (SHA-256 check), cleared on lock
- `settings.go` — vault path picker, auto-lock on/off and timeout slider, clipboard TTL slider. *Change Passphrase*, *Move Vault* and *Switch Vault* are one kind of operation: while the passphrase is changed or the vault copied, its button is disabled and the others wait (the status bar says so), also from a Settings screen opened again. Each change goes to `Settings` (`config.go`), which clamps and saves it; a slider's value is applied and saved when its change ends (a drag released, a tap, a key press), and its label follows it meanwhile. *Move Vault* and *Switch Vault* only propose the new location; it is committed once a vault opens there. Settings provided by environment variables (`Settings.SetByEnv`) are disabled and labelled
- `config.go` — `Settings`, the settings module: the vault location, auto-lock timeout, clipboard TTL and start-up update check (plus the last release that check announced). `LoadSettings` resolves each from env var → saved preference → default and clamps it (auto-lock 30 s–30 min or off; clipboard TTL 5–120 s). Its setters clamp, refuse a setting an environment variable fixes (logging that the change was ignored), and save to Fyne's preferences, so an environment value is never persisted; `SetByEnv` names the variable that fixes a setting, which Settings and the create screen ask before disabling a control. The vault location is a `store.Location`, compared by value, so the same place written two ways is the same location. It is *proposed* (`ProposeVault`: the location the app shows and opens, set by a switch, a move, *Open Existing Vault…*, a server location or *Use the Vault on This Computer*) and *committed* (`CommitVault`, which `App` calls after every successful unlock or create: it saves the location unless `VLT_VAULT_DIR` set it), so a mistyped location never sticks, while the default location, which on Linux depends on `XDG_DATA_HOME`, is saved too and stays the same across launches. A location from `VLT_VAULT_DIR` or the saved settings that does not parse is kept as written (`UnusableVault`), so the error screen shows it and why. `DefaultVault` is the default local location when it may be offered (there is one, it is not the current location, and `VLT_VAULT_DIR` does not fix the location). Settings is used on the main goroutine only; tests use the test app's in-memory preferences. `MigratePreferences` carries the vault location, auto-lock timeout and clipboard TTL over from vlt 0.1.0, whose app ID was the placeholder `io.yourorg.vlt` (Fyne keeps settings per app ID), once (it then sets a marker, so a later start never copies an old value over a newer one), and only while no vlt setting is saved under the current ID `io.github.heyjdp.vlt`
- `about.go` — the About dialog, opened from the status bar's *About* button (present on every screen) and the *About* menu item (*vlt → About vlt* on macOS, *Help → About* elsewhere). `aboutDetails` passes `debug.ReadBuildInfo` to `readBuildDetails`, which takes the VCS revision and time and the Fyne version from it, plus the version (`main.version`, via `NewApp`) and the `runtime` platform and Go version; the vault is described only as local or on a server, and locked or unlocked. *Copy Details* puts that text on the clipboard; *Report a Bug…* opens `https://github.com/heyjdp/vlt/issues/new` with it in the body
- `totp.go` — a saved record with a TOTP secret shows a *One-Time Code* panel above its form: the code grouped for reading (`123 456`), "Changes in N s", and *Copy Code*, which goes through the clipboard clearer. It updates at each whole second through the app's timer factory, with a generation counter so a tick queued for an older panel does nothing; any re-render of the detail panel (`setObjects`) and `clear` on lock stop it. The record form's TOTP field is checked with `totp.Parse` before saving (`formField.check`), and saving a changed secret re-renders the form, if it is still showing that record, so the panel appears, changes or goes. Stopping the ticking (re-render or lock) also wipes the decoded key (`Key.Wipe`) and blanks the code label, since Fyne's renderer cache can keep the panel reachable; HMAC's internal copies of the key are out of reach
- `updates.go` — *Help → Check for Updates…* and Settings' *Check Now* run the check in the background behind a progress dialog, then say there is a newer release (its notes, and *Open Release Page*), that this build is up to date, or, for a development build, which release is the latest; a failure is shown as an error. A second request while one runs is ignored, and a result arriving after a lock (the session's `Gen`/`Current` shows it ended) is dropped, so no dialog from the session outlives it. `NewApp` passes the real checker to `startUpdateChecks`, which, if `Settings.CheckUpdates` is on (`checkUpdates` preference, off by default, set in Settings), checks once at start-up. Only a release newer than this build and than the last one announced (`Settings.AnnouncedRelease`, the `updateNotified` preference) is shown, and only once the vault is unlocked, so the notice never takes the focus from the passphrase entry; nothing is shown after a manual check, and failures are only logged. Other constructors keep a checker that returns `errNoUpdateCheck`, so tests never reach the network. vlt never downloads or installs anything
- `session.go` — the vault session, the one module that knows whether a vault is open. It holds the open store, a store waiting on the "Vault Already Open" question, the session context and the auto-lock countdown with its warning, all under its own mutex, so it can be called from any goroutine. It works on any store that can be closed and report when it has released its lock (`sessionStore`), so tests can use a store that never finishes. When the auto-lock timeout passes, it closes the store, zeroing the key, at once on the timer's goroutine; only the screen change waits for the main goroutine, which may be busy (an open menu, a resize). `Begin` opens it on a store and returns the context that `End` cancels; `End` zeroes the key, closes the store and stops the countdown; `Activity` restarts the countdown, re-reading the timeout; `Hold`/`Take` keep a store waiting, closing it if the timeout passes first; `Gen`/`Current` tell work started earlier whether the session has ended since; `Close` ends everything and waits a bounded time for remote stores to release their locks. Its callbacks (warning, timeout, expiry) run through `fyne.Do` and only for timers still current: activity and ending the session silence the auto-lock and warning timers already firing, and taking a waiting store silences its timer. A timeout notice for a session that has since been ended another way is dropped. `Begin` ends a session still open, and `Hold` closes a store already waiting. Tests drive it with real temporary stores and hand-fired timers, without a window
- `errmsg.go` — every error the UI shows goes through `errorMessage(doing, err)` (a string, for labels and notices) or `showError(window, doing, err)` (a dialog). `doing` says what failed ("Couldn't move the vault"); the error says why. The store, crypto and update packages wrap errors with where they failed (`store.CopyTo "…": `) for the log, so `errorMessage` describes the error itself rather than its text: the wrong passphrase, cancellation and a timeout by meaning (`errors.Is`); a file error (`*fs.PathError`, `*os.LinkError`) as what couldn't be done to which path and why ("can't create the folder /x: permission denied"; the server backend returns its failures as `*fs.PathError` too, since pkg/sftp's errors name no file); anything else by walking the chain and dropping the text each wrapper puts before the error it wraps, keeping what it adds after (a server's name and what ssh printed, "the file … may be incomplete", "(in vlt.json)"). A single cause is found from the end of its wrapper's text, so a prefix repeating it is still dropped. So a package may add a wrapper without the UI changing, provided its `pkg.Func:` prefix comes before a `%w` and context people need (a file, a mode, another secret's name) comes after it; errors are never written with the prefix inside a single string, nor with text wrapped around a cause. `isServerError` (also here) chooses the server heading and screen, not the wording
- `app.go` — wires everything together: chooses the screen (check, unlock, create, error, main view) and reacts to the session. After a vault opens or is created it takes the lock; if another copy of vlt holds it, the opened store waits in the session (`Hold`) behind a "Vault Already Open" dialog: *Open Anyway* takes the lock over, *Cancel* closes the store, and no answer within the auto-lock timeout closes it too. `Lock` ends the session and returns to the unlock screen; after an auto-lock, the session has already ended (and the key is zeroed) when it calls `Lock` on the main goroutine. Quitting closes the open or waiting store and waits up to 3 seconds for a remote one to release its lock

---

## UX Features

### Clipboard Safety
- Copying any secret value clears the clipboard after a configurable TTL (default: **30 seconds**); the TTL is read at each copy, so a Settings change applies to the next copy
- A countdown is shown in the status bar while a value is in the clipboard
- Implemented by one `clipboardClearer` per app (`clipboard.go`), driven by one-second timers. Each copy starts a new countdown generation, so an earlier copy can never cut a later one short
- On expiry the clipboard is cleared only if it still holds the value vlt copied (compared by SHA-256, so no second plaintext copy is kept). Content copied from elsewhere in the meantime is left alone
- Locking the vault or quitting clears vlt's value immediately and stops the countdown (quit is intercepted on window close and via the Quit menu item, because the system clipboard outlives the process). An empty clipboard read, which Fyne returns when the OS clipboard is locked by another program, is treated as vlt's value and cleared

### Session Lock
- Auto-lock after N minutes of inactivity (default: **5 minutes**)
- On lock: the `memguard.LockedBuffer` holding the vault key is zeroed and destroyed (best-effort; see Security Considerations)
- Passphrase must be re-entered to resume; no in-memory state is preserved across lock cycles
- The inactivity timer resets on user input in the main view and detail/settings panel: typing in an entry, pressing a button, selecting a list row or moving a slider (`activity.go` wraps these widgets' callbacks, because Fyne has no global input hook), plus key presses when no widget has focus. Widgets inside dialogs are not instrumented by the app's tracker; instead `operations` reports the answer to a file chooser or confirmation, and input in a confirmation's content (the import's group form). Pointer movement, scrolling and split dragging do not count
- With a timeout of at least 60 seconds, a dialog warns 30 seconds before locking: *Stay unlocked* (or any other input) restarts the countdown, *Lock now* locks immediately. The lock itself always happens if the user is away. Timer callbacks carry a generation number, so a timer replaced by fresh activity never warns or locks

### Search
- Instant filter across all decrypted secret names and tags within the current unlocked session
- File content and notes are not searched
- Implemented client-side on the in-memory secret list — no disk reads during search

### File Import / Export
- **Import:** file picker → read up to 10 MiB (in the background; any read error aborts the import, and files over the limit are rejected) → MIME detect, base64 encode, SHA-256 → choose group → wrap in `file` envelope → encrypt → save (`Create`; when its `*ExistsError` holds a secret of the same name, ask, then `Update`; a differently named secret or an unreadable file there is never replaced) → completion dialog shows the SHA-256
- **Export:** base64 decode (in the background) → choose path → write and close (in the background; on Unix a local file is written to a new 0600 temp file in the same folder, fsynced and renamed over it (then the folder is fsynced, best effort), because the save dialog has already created it with `os.Create` (the user's umask) and someone may have opened it since; a planted file or symlink is replaced, never written through, and a failed export removes only the temp file. If the user may not add files to the folder, the file is made 0600 and written in place, and if it can't be made 0600 (another user's file) nothing is written; any other failure to create the temp file fails the export. A crash before the rename leaves the 0600 temp file (`.vlt-export-*`) behind. Windows writes in place, since its file modes are only a read-only flag; a failed write or close is reported and the partial file deleted where possible). The decoded plaintext is owned by one function (`exportDecoded`) and wiped there after writing, on cancel, on error, and if the vault locks first (also if it locks while decoding); an export confirmed after a lock writes nothing and closes the file
- Locking dismisses every open dialog (Fyne draws dialogs, file pickers included, inside the window), so no dialog outlives the session
- MIME type is detected at import time using Go's `net/http.DetectContentType`

### Git Integration (Optional)
- On startup, detect if the vault directory is inside a git repository (`git rev-parse`)
- If detected, offer a toggle in Settings: "Commit after each write"
- When enabled, `vlt` runs `git add <file> && git commit -m "vlt: update <secret-name>"` after every save/delete
- This is not a hard dependency; the app functions fully without git

---

## OpenBSD Notes

- Fyne requires OpenGL — install `mesa-dri` via `pkg_add` before running
- Build on OpenBSD itself with `go build`: Fyne needs cgo, a C compiler and mesa, so it cannot be cross-compiled from another OS
- `pledge(2)` / `unveil(2)` integration is deferred to a later phase due to Fyne's use of internal threads and OpenGL context
- No system keychain exists on OpenBSD — the passphrase-only model is the correct choice and requires no OS-specific keychain integration on any platform

---

## Build & Distribution

Fyne needs cgo, so vlt is built natively on each target OS (CI uses Linux, macOS and Windows runners). CI also packages a macOS app on a macOS runner and a Linux AppImage on Ubuntu 22.04, and publishes both as a GitHub release for `v*` tags.

```bash
go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt
```

`make package-mac` builds an ad-hoc signed `vlt.app` for macOS 13 or later with `packaging/macos/build-app.sh`, using only tools that ship with macOS. `make package-linux` builds an AppImage with `packaging/linux/build-appimage.sh`. Windows packages are not built yet.

---

## Development Phases

### Phase 1 — Core (`crypto` + `store`, no GUI)
- [ ] `go.mod` with `filippo.io/age`, `memguard`
- [ ] `crypto/identity.go` — passphrase → identity, zero on lock
- [ ] `crypto/vault.go` — encrypt/decrypt helpers
- [ ] `store/secret.go` — envelope types, JSON schema
- [ ] `store/store.go` — CRUD on vault directory
- [ ] CLI smoke test: encrypt a secret, list it, decrypt it

### Phase 2 — GUI Skeleton
- [ ] Fyne app bootstrap
- [ ] Unlock screen with passphrase entry
- [ ] Secret list panel (left)
- [ ] Detail view for `password` and `record` types (right)
- [ ] Basic add/edit/delete flow

### Phase 3 — File Secrets
- [ ] File import via file picker
- [ ] File export to disk
- [ ] MIME detection
- [ ] 10 MiB size limit enforcement

### Phase 4 — Polish
- [x] Clipboard TTL with status bar countdown
- [x] Auto-lock with inactivity timer
- [ ] Search / filter
- [x] Settings screen (vault path, lock timeout, clipboard TTL)
- [ ] Optional git commit on write

---

## Security Considerations

- The passphrase is never stored on disk; on disk, only the passphrase-encrypted vault key (`vlt.age`) and secrets encrypted to it exist

**Memory protection is best-effort.** Go strings are immutable and cannot be wiped, the garbage collector may copy memory, and Fyne keeps widget text as strings. vlt therefore limits *how long* and *where* secrets are reachable rather than promising they are erased:

- The passphrase is read from the unlock (or change-passphrase) entry and the entry is cleared immediately, on every path (`SetText` also clears the entry's undo history). It is used once to unwrap `vlt.age` and is not kept for the session
- The vault key's encoded form is held in a `memguard.LockedBuffer` (mlock'd, guard pages) and destroyed on lock and exit. age's parsed `X25519Identity` is kept alongside it for the session (parsing once, rather than per operation, avoids a fresh heap copy of the key for every secret); it and one transient string copy remain on the Go heap until garbage-collected
- Decrypted secrets are held, as Go strings, by the secret list and the detail panel while the vault is unlocked. On lock, vlt empties every loaded secret struct and blanks the detail form's entries (Fyne's renderer cache can keep old widgets reachable for a minute or more after they leave the window, so emptying beats merely dropping), then drops its references to the list, detail panel, main view and activity tracker, dismisses open dialogs (wiping a pending export buffer), and clears the clipboard if it still holds the value vlt copied. The strings are reclaimed by the garbage collector, not overwritten; background work still in flight at the lock keeps its own copy until it completes and its result is dropped
- Decrypted JSON and file buffers (`[]byte`) that vlt owns are zeroed once parsed, encoded or written (including every secret read while listing the vault). Intermediate copies made inside `io.ReadAll`, age and `encoding/json` are not
- A vault on an SSH server is reached through the system's ssh client, so authentication, host-key checking and transport encryption are OpenSSH's. vlt runs it in batch mode: it never prompts and has no password support, and an unknown host key is refused unless the user's ssh config accepts new keys (`StrictHostKeyChecking accept-new` or `no`). Secrets and the wrapped vault key are age-encrypted before they are sent; `vlt.json` (the format number) and `vlt.lock` (host, user, PID, time) are plain text. The server learns the slugified group and secret names, file sizes and times. Whoever has the server's files can guess passphrases offline against `vlt.age`, as with any copy of the vault; whoever controls the server can also delete files or roll any file back to an older version, which vlt cannot detect (age has no rollback protection). It cannot alter a file undetected, since age authenticates each one, nor pass one secret's file off as another's: a secret is read only from the path its own name and group give (ignoring case), so a file copied over another secret or into another group is refused like a damaged one. Opening such a file fails; the list leaves it out and counts it under the list, with its path in *Details…*
- A record's TOTP secret is stored in the same vault, under the same passphrase, as its password, so whoever unlocks the vault has both factors. That is the convenience vlt offers; keep two-factor secrets in a separate authenticator if the second factor must survive a leaked vault passphrase. Codes are shown in plain text while the record is open; a copied code is cleared from the clipboard like any secret
- The update check is the only network access besides a server vault. It runs only when the user asks or has turned on the start-up check, sends one unauthenticated HTTPS GET to GitHub (revealing the user's IP address and that they run vlt), and never downloads or runs code. Its result is shown as text and a link to vlt's own release page, built from a tag that must match `vX.Y.Z`
- Two copies of vlt using one vault (two computers sharing a server vault, or a synced folder) are safe for different secrets, and creating a secret never overwrites one; for the same secret the last save wins. `vlt.lock` warns about this but cannot prevent it
- A vault on a network share or cloud-synced folder (rather than over SFTP) depends on that file system's rename and link semantics; that is the user's responsibility
- All vault writes go through a temp-file + fsync + rename + directory-fsync helper, so a crash or full disk never leaves a truncated `.age` file (directory fsync is skipped on Windows). Atomicity is per file, so multi-file operations are ordered so any interrupted prefix leaves the vault readable: passphrase change rewrites only `vlt.age`, and format-1 migration writes the new key to a staging file before touching secrets and swaps `vlt.age` only after every secret is re-encrypted
- vlt creates vault directories with mode `0700` and files with `0600` (Unix), and exported files with `0600` too (on Windows they take the folder's ACL). A directory that already existed keeps its mode, and vlt does not re-check or repair permissions later, so permissions widened by other tools stay widened

